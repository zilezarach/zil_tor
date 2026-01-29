package annasarchive

import (
	"context"
	"crypto/md5"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"sync"
	"time"

	"github.com/PuerkitoBio/goquery"
	"github.com/zilezarach/zil_tor-api/internal/models"
	"go.uber.org/zap"
)

// AnnasArchiveIndexer scrapes Anna's Archive for book metadata and download links
type AnnasArchiveIndexer struct {
	baseURL       string
	logger        *zap.Logger
	client        *http.Client
	downloadPool  chan struct{}
	cacheMutex    sync.RWMutex
	urlCache      map[string]string
	downloadCache *DownloadURLCache
}

// AnnasArchiveEntry represents a book entry from Anna's Archive
type AnnasArchiveEntry struct {
	Title       string
	Author      string
	Publisher   string
	Thumbnail   string
	Link        string
	MD5         string
	Info        string
	Format      string
	Mirror      string
	Description string
}

// Download Cache to avoid re-solving
type DownloadURLCache struct {
	urls map[string]*CachedDownloadURL
	mu   sync.RWMutex
	ttl  time.Duration
}

type CachedDownloadURL struct {
	URL     string
	Created time.Time
}

// NewAnnasArchiveIndexer creates a new Anna's Archive indexer
func NewAnnasArchiveIndexer(logger *zap.Logger) *AnnasArchiveIndexer {
	return &AnnasArchiveIndexer{
		baseURL: "https://annas-archive.li",
		logger:  logger,
		client: &http.Client{
			Timeout: 45 * time.Second,
			Transport: &http.Transport{
				MaxIdleConns:        100,
				MaxIdleConnsPerHost: 10,
				IdleConnTimeout:     90 * time.Second,
			},
		},
		downloadPool: make(chan struct{}, 5),
		urlCache:     make(map[string]string),
	}
}

func (a *AnnasArchiveIndexer) Name() string {
	return "AnnasArchive"
}

// Search performs a search on Anna's Archive
func (a *AnnasArchiveIndexer) Search(ctx context.Context, query string, limit int) ([]models.TorrentResult, error) {
	searchURL := a.buildSearchURL(query, "", "", "", true)

	a.logger.Info("Searching Anna's Archive",
		zap.String("query", query),
		zap.String("url", searchURL))

	// Fetch search results
	req, err := http.NewRequestWithContext(ctx, "GET", searchURL, nil)
	if err != nil {
		return nil, err
	}

	req.Header.Set("User-Agent", "Mozilla/5.0 (Windows NT 10.0; Win64; x64) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/128.0.0.0 Safari/537.36")
	req.Header.Set("Accept", "text/html,application/xhtml+xml,application/xml;q=0.9,*/*;q=0.8")

	resp, err := a.client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("failed to fetch search results: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("unexpected status code: %d", resp.StatusCode)
	}

	// Parse HTML
	doc, err := goquery.NewDocumentFromReader(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("failed to parse HTML: %w", err)
	}

	// Parse entries
	entries := a.parseSearchResults(doc, "")

	// Convert to TorrentResult
	results := make([]models.TorrentResult, 0, len(entries))
	for i, entry := range entries {
		if i >= limit {
			break
		}

		result := models.TorrentResult{
			Title:       fmt.Sprintf("%s - %s", entry.Title, entry.Author),
			MagnetURI:   entry.Link,
			InfoHash:    entry.MD5,
			Size:        a.extractSize(entry.Info),
			SizeBytes:   parseSizeString(a.extractSize(entry.Info)),
			Category:    "Books",
			Source:      a.Name(),
			PublishDate: time.Now(),
			Extra: map[string]string{
				"download_type": "direct",
				"authors":       entry.Author,
				"publisher":     entry.Publisher,
				"format":        entry.Format,
				"md5":           entry.MD5,
				"thumbnail":     entry.Thumbnail,
				"info":          entry.Info,
			},
		}
		results = append(results, result)
	}

	a.logger.Info("Anna's Archive search complete",
		zap.String("query", query),
		zap.Int("results", len(results)))

	return results, nil
}

// buildSearchURL constructs the search URL with parameters
func (a *AnnasArchiveIndexer) buildSearchURL(query, content, sort, fileType string, enableFilters bool) string {
	// url.QueryEscape is safer than manual string replacement for special characters
	escapedQuery := url.QueryEscape(query)

	if !enableFilters {
		return fmt.Sprintf("%s/search?q=%s", a.baseURL, escapedQuery)
	}

	// Explicitly adding page=1 and display= as seen in your target URL
	return fmt.Sprintf("%s/search?index=&page=1&sort=%s&display=&q=%s&content=%s&ext=%s",
		a.baseURL, sort, escapedQuery, content, fileType)
}

// parseSearchResults extracts book entries from search results HTML
func (a *AnnasArchiveIndexer) parseSearchResults(doc *goquery.Document, fileType string) []AnnasArchiveEntry {
	entries := make([]AnnasArchiveEntry, 0)

	// Find book containers
	doc.Find("div.flex.pt-3.pb-3.border-b").Each(func(i int, container *goquery.Selection) {
		// Extract main link (title)
		mainLink := container.Find("a.line-clamp-\\[3\\].js-vim-focus")
		if mainLink.Length() == 0 {
			return
		}

		href, exists := mainLink.Attr("href")
		if !exists {
			return
		}

		title := strings.TrimSpace(mainLink.Text())
		link := a.baseURL + href
		md5 := a.getMD5FromURL(href)

		// Extract thumbnail
		thumbnail, _ := container.Find("a[href^=\"/md5/\"] img").Attr("src")

		// Extract author and publisher using sequential traversal
		author := "unknown"
		publisher := "unknown"

		// Find author link (first link after main title)
		authorLink := a.findNextSearchLink(mainLink)
		if authorLink != nil {
			authorText := strings.TrimSpace(authorLink.Text())
			// Remove icon prefix if present
			if strings.Contains(authorText, "icon-") {
				parts := strings.Split(authorText, " ")
				if len(parts) > 1 {
					author = strings.TrimSpace(strings.Join(parts[1:], " "))
				}
			} else {
				author = authorText
			}

			// Find publisher link (next link after author)
			publisherLink := a.findNextSearchLink(authorLink)
			if publisherLink != nil {
				publisher = strings.TrimSpace(publisherLink.Text())
			}
		}

		// Extract info
		info := strings.TrimSpace(container.Find("div.text-gray-800").Text())

		// Determine format
		format := a.getFormat(info)

		// Filter by file type if specified
		if fileType != "" {
			if !strings.Contains(strings.ToLower(info), strings.ToLower(fileType)) {
				return
			}
		} else {
			// Check if it has a valid format
			if !regexp.MustCompile(`(?i)(PDF|EPUB|CBR|CBZ)`).MatchString(info) {
				return
			}
		}

		entry := AnnasArchiveEntry{
			Title:     title,
			Author:    author,
			Publisher: publisher,
			Thumbnail: thumbnail,
			Link:      link,
			MD5:       md5,
			Info:      info,
			Format:    format,
		}

		entries = append(entries, entry)
	})

	return entries
}

// findNextSearchLink finds the next sibling that's a search link
func (a *AnnasArchiveIndexer) findNextSearchLink(element *goquery.Selection) *goquery.Selection {
	next := element.Next()
	for next.Length() > 0 {
		if next.Is("a") {
			href, exists := next.Attr("href")
			if exists && strings.HasPrefix(href, "/search?q=") {
				return next
			}
		}
		next = next.Next()
	}
	return nil
}

// GetBookInfo fetches detailed book information
func (a *AnnasArchiveIndexer) GetBookInfo(ctx context.Context, bookURL string) (*AnnasArchiveEntry, error) {
	req, err := http.NewRequestWithContext(ctx, "GET", bookURL, nil)
	if err != nil {
		return nil, err
	}

	req.Header.Set("User-Agent", "Mozilla/5.0 (Windows NT 10.0; Win64; x64) AppleWebKit/537.36")
	req.Header.Set("Accept", "text/html,application/xhtml+xml,application/xml;q=0.9,*/*;q=0.8")
	req.Header.Set("Referer", a.baseURL)

	resp, err := a.client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("failed to fetch book info: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("unexpected status code: %d", resp.StatusCode)
	}

	doc, err := goquery.NewDocumentFromReader(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("failed to parse HTML: %w", err)
	}

	return a.parseBookInfo(doc, bookURL)
}

// parseBookInfo extracts detailed book information from detail page
func (a *AnnasArchiveIndexer) parseBookInfo(doc *goquery.Document, bookURL string) (*AnnasArchiveEntry, error) {
	main := doc.Find("div.main-inner")
	if main.Length() == 0 {
		return nil, fmt.Errorf("unable to find main content")
	}

	// Extract mirror link (slow_download link)
	mirror := ""
	main.Find("ul.list-inside a[href*=\"/slow_download/\"]").Each(func(i int, s *goquery.Selection) {
		if i == 0 {
			if href, exists := s.Attr("href"); exists {
				mirror = a.baseURL + href
			}
		}
	})

	// Extract title
	titleElement := main.Find("div.font-semibold.text-2xl")
	if titleElement.Length() == 0 {
		return nil, fmt.Errorf("unable to find title")
	}
	title := strings.TrimSpace(titleElement.Text())
	// Remove any HTML tags
	title = regexp.MustCompile(`<[^>]*>`).ReplaceAllString(title, "")

	// Extract author
	author := "unknown"
	authorLink := main.Find("a[href^=\"/search?q=\"].text-base")
	if authorLink.Length() > 0 {
		author = strings.TrimSpace(authorLink.Text())
	}

	// Extract publisher
	publisher := "unknown"
	publisherLink := a.findNextSearchLink(authorLink)
	if publisherLink != nil {
		publisher = strings.TrimSpace(publisherLink.Text())
	}

	// Extract thumbnail
	thumbnail, _ := main.Find("div[id^=\"list_cover_\"] img").Attr("src")

	// Extract info
	info := strings.TrimSpace(main.Find("div.text-gray-800").Text())

	// Extract description
	description := ""
	main.Find("div.js-md5-top-box-description div.text-xs.text-gray-500.uppercase").Each(func(i int, s *goquery.Selection) {
		if strings.ToLower(strings.TrimSpace(s.Text())) == "description" {
			descElement := s.Next()
			if descElement.Length() > 0 {
				description = strings.TrimSpace(descElement.Text())
			}
		}
	})

	format := a.getFormat(info)
	md5 := a.getMD5FromURL(bookURL)

	return &AnnasArchiveEntry{
		Title:       title,
		Author:      author,
		Publisher:   publisher,
		Thumbnail:   thumbnail,
		Link:        bookURL,
		MD5:         md5,
		Info:        info,
		Format:      format,
		Mirror:      mirror,
		Description: description,
	}, nil
}

// getMD5FromURL extracts MD5 hash from URL
func (a *AnnasArchiveIndexer) getMD5FromURL(urlStr string) string {
	u, err := url.Parse(urlStr)
	if err != nil {
		return ""
	}

	segments := strings.Split(strings.Trim(u.Path, "/"), "/")
	if len(segments) > 0 {
		return segments[len(segments)-1]
	}
	return ""
}

func NewDownloadURLCache(ttl time.Duration) *DownloadURLCache {
	cache := &DownloadURLCache{
		urls: make(map[string]*CachedDownloadURL),
		ttl:  ttl,
	}

	// Cleanup goroutine
	go func() {
		ticker := time.NewTicker(10 * time.Minute)
		defer ticker.Stop()
		for range ticker.C {
			cache.cleanup()
		}
	}()

	return cache
}

func (c *DownloadURLCache) Get(md5 string) (string, bool) {
	c.mu.RLock()
	defer c.mu.RUnlock()

	cached, exists := c.urls[md5]
	if !exists {
		return "", false
	}

	if time.Since(cached.Created) > c.ttl {
		return "", false
	}

	return cached.URL, true
}

func (c *DownloadURLCache) Set(md5, url string) {
	c.mu.Lock()
	defer c.mu.Unlock()

	c.urls[md5] = &CachedDownloadURL{
		URL:     url,
		Created: time.Now(),
	}
}

func (c *DownloadURLCache) cleanup() {
	c.mu.Lock()
	defer c.mu.Unlock()

	now := time.Now()
	for key, cached := range c.urls {
		if now.Sub(cached.Created) > c.ttl {
			delete(c.urls, key)
		}
	}
}

func (a *AnnasArchiveIndexer) GetCachedDownloadURL(ctx context.Context, md5 string, solverClient interface{}) (string, error) {
	// Check cache first
	if a.downloadCache != nil {
		if url, found := a.downloadCache.Get(md5); found {
			a.logger.Debug("Download URL cache hit", zap.String("md5", md5))
			return url, nil
		}
	}

	// Not in cache, need to solve
	bookURL := fmt.Sprintf("https://annas-archive.li/md5/%s", md5)
	bookInfo, err := a.GetBookInfo(ctx, bookURL)
	if err != nil {
		return "", fmt.Errorf("failed to get book info: %w", err)
	}

	if bookInfo.Mirror == "" {
		return "", fmt.Errorf("no download mirror found")
	}

	// The actual solving happens in main.go's solveAnnasArchiveDownload
	return bookInfo.Mirror, nil
}

// DownloadFile downloads a file from Anna's Archive with progress tracking
func (a *AnnasArchiveIndexer) DownloadFile(
	ctx context.Context,
	md5 string,
	destPath string,
	progressCallback func(downloaded, total int64),
) error {
	// Acquire slot from download pool
	select {
	case a.downloadPool <- struct{}{}:
		defer func() { <-a.downloadPool }()
	case <-ctx.Done():
		return ctx.Err()
	}

	// First, get the book detail page to extract mirrors
	bookURL := fmt.Sprintf("%s/md5/%s", a.baseURL, md5)
	bookInfo, err := a.GetBookInfo(ctx, bookURL)
	if err != nil {
		return fmt.Errorf("failed to get book info: %w", err)
	}

	// Get download mirrors
	mirrors, err := a.getDownloadMirrors(ctx, bookInfo)
	if err != nil {
		return fmt.Errorf("failed to get download mirrors: %w", err)
	}

	if len(mirrors) == 0 {
		return fmt.Errorf("no download mirrors available")
	}

	// Reorder mirrors (prioritize IPFS and reliable HTTPS)
	orderedMirrors := a.reorderMirrors(mirrors)

	// Find working mirror
	workingMirror, err := a.findWorkingMirror(ctx, orderedMirrors)
	if err != nil {
		return fmt.Errorf("no working mirrors available: %w", err)
	}

	a.logger.Info("Starting download from Anna's Archive",
		zap.String("md5", md5),
		zap.String("mirror", workingMirror),
		zap.String("destination", destPath))

	// Download from working mirror
	return a.downloadFromMirror(ctx, workingMirror, destPath, progressCallback)
}

// getDownloadMirrors extracts all available download mirrors for a book
func (a *AnnasArchiveIndexer) getDownloadMirrors(ctx context.Context, bookInfo *AnnasArchiveEntry) ([]string, error) {
	mirrors := make([]string, 0)

	// Primary mirror from slow_download link
	if bookInfo.Mirror != "" {
		mirrors = append(mirrors, bookInfo.Mirror)
	}

	// Fetch the book detail page to get additional mirrors
	req, err := http.NewRequestWithContext(ctx, "GET", bookInfo.Link, nil)
	if err != nil {
		return mirrors, err
	}

	req.Header.Set("User-Agent", "Mozilla/5.0 (Windows NT 10.0; Win64; x64) AppleWebKit/537.36")
	resp, err := a.client.Do(req)
	if err != nil {
		return mirrors, err
	}
	defer resp.Body.Close()

	doc, err := goquery.NewDocumentFromReader(resp.Body)
	if err != nil {
		return mirrors, err
	}

	// Extract IPFS mirrors
	doc.Find("a[href*='ipfs']").Each(func(i int, s *goquery.Selection) {
		if href, exists := s.Attr("href"); exists {
			mirrors = append(mirrors, href)
		}
	})

	// Extract fast download links
	doc.Find("a[href*='/fast_download/']").Each(func(i int, s *goquery.Selection) {
		if href, exists := s.Attr("href"); exists {
			fullURL := href
			if !strings.HasPrefix(href, "http") {
				fullURL = a.baseURL + href
			}
			mirrors = append(mirrors, fullURL)
		}
	})

	// Extract external mirrors (libgen.li, etc.)
	doc.Find("a[href*='libgen']").Each(func(i int, s *goquery.Selection) {
		if href, exists := s.Attr("href"); exists {
			if strings.HasPrefix(href, "http") {
				mirrors = append(mirrors, href)
			}
		}
	})

	return mirrors, nil
}

// reorderMirrors prioritizes mirrors (IPFS first, then reliable HTTPS)
func (a *AnnasArchiveIndexer) reorderMirrors(mirrors []string) []string {
	ipfsMirrors := make([]string, 0)
	httpsMirrors := make([]string, 0)

	for _, mirror := range mirrors {
		mirrorLower := strings.ToLower(mirror)

		// Skip unreliable mirrors
		if strings.Contains(mirrorLower, "annas-archive.li") ||
			strings.Contains(mirrorLower, "1lib.sk") {
			continue
		}

		// Prioritize IPFS
		if strings.Contains(mirrorLower, "ipfs") {
			ipfsMirrors = append(ipfsMirrors, mirror)
		} else {
			httpsMirrors = append(httpsMirrors, mirror)
		}
	}

	// Return IPFS first, then HTTPS
	return append(ipfsMirrors, httpsMirrors...)
}

// findWorkingMirror tests mirrors and returns the first working one
func (a *AnnasArchiveIndexer) findWorkingMirror(ctx context.Context, mirrors []string) (string, error) {
	if len(mirrors) == 0 {
		return "", fmt.Errorf("no mirrors provided")
	}

	// If only one mirror, wait briefly and return it
	if len(mirrors) == 1 {
		time.Sleep(2 * time.Second)
		return mirrors[0], nil
	}

	// Test mirrors concurrently
	type mirrorResult struct {
		mirror string
		err    error
	}

	resultChan := make(chan mirrorResult, len(mirrors))
	testCtx, cancel := context.WithTimeout(ctx, 15*time.Second)
	defer cancel()

	for _, mirror := range mirrors {
		go func(m string) {
			req, err := http.NewRequestWithContext(testCtx, "HEAD", m, nil)
			if err != nil {
				resultChan <- mirrorResult{"", err}
				return
			}

			req.Header.Set("User-Agent", "Mozilla/5.0 (Windows NT 10.0; Win64; x64) AppleWebKit/537.36")

			resp, err := a.client.Do(req)
			if err != nil {
				resultChan <- mirrorResult{"", err}
				return
			}
			defer resp.Body.Close()

			if resp.StatusCode == http.StatusOK {
				resultChan <- mirrorResult{m, nil}
			} else {
				resultChan <- mirrorResult{"", fmt.Errorf("status %d", resp.StatusCode)}
			}
		}(mirror)
	}

	// Return first working mirror
	for i := 0; i < len(mirrors); i++ {
		result := <-resultChan
		if result.err == nil && result.mirror != "" {
			return result.mirror, nil
		}
	}

	return "", fmt.Errorf("no working mirrors found")
}

// downloadFromMirror downloads file from a specific mirror with progress tracking
func (a *AnnasArchiveIndexer) downloadFromMirror(
	ctx context.Context,
	mirrorURL string,
	destPath string,
	progressCallback func(downloaded, total int64),
) error {
	req, err := http.NewRequestWithContext(ctx, "GET", mirrorURL, nil)
	if err != nil {
		return err
	}

	req.Header.Set("User-Agent", "Mozilla/5.0 (Windows NT 10.0; Win64; x64) AppleWebKit/537.36")
	req.Header.Set("Connection", "Keep-Alive")

	resp, err := a.client.Do(req)
	if err != nil {
		return fmt.Errorf("download request failed: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("download failed with status: %d", resp.StatusCode)
	}

	// Create destination file
	if err := os.MkdirAll(filepath.Dir(destPath), 0755); err != nil {
		return fmt.Errorf("failed to create directory: %w", err)
	}

	file, err := os.Create(destPath)
	if err != nil {
		return fmt.Errorf("failed to create file: %w", err)
	}
	defer file.Close()

	// Download with progress tracking
	totalBytes := resp.ContentLength
	buffer := make([]byte, 32*1024)
	var downloadedBytes int64

	for {
		select {
		case <-ctx.Done():
			return ctx.Err()
		default:
			n, err := resp.Body.Read(buffer)
			if n > 0 {
				if _, writeErr := file.Write(buffer[:n]); writeErr != nil {
					return fmt.Errorf("failed to write to file: %w", writeErr)
				}
				downloadedBytes += int64(n)

				// Call progress callback
				if progressCallback != nil {
					progressCallback(downloadedBytes, totalBytes)
				}
			}

			if err == io.EOF {
				a.logger.Info("Download completed",
					zap.String("file", destPath),
					zap.Int64("bytes", downloadedBytes))
				return nil
			}

			if err != nil {
				return fmt.Errorf("download error: %w", err)
			}
		}
	}
}

// VerifyChecksum verifies downloaded file MD5 checksum
func (a *AnnasArchiveIndexer) VerifyChecksum(filePath, expectedMD5 string) (bool, error) {
	file, err := os.Open(filePath)
	if err != nil {
		return false, err
	}
	defer file.Close()

	hash := md5.New()
	if _, err := io.Copy(hash, file); err != nil {
		return false, err
	}

	actualMD5 := fmt.Sprintf("%x", hash.Sum(nil))
	return strings.EqualFold(actualMD5, expectedMD5), nil
}

// getFormat determines file format from info string
func (a *AnnasArchiveIndexer) getFormat(info string) string {
	infoLower := strings.ToLower(info)
	if strings.Contains(infoLower, "pdf") {
		return "pdf"
	} else if strings.Contains(infoLower, "cbr") {
		return "cbr"
	} else if strings.Contains(infoLower, "cbz") {
		return "cbz"
	}
	return "epub"
}

// extractSize extracts size information from info string
func (a *AnnasArchiveIndexer) extractSize(info string) string {
	// Look for patterns like "10.5 MB" or "1.2 GB"
	re := regexp.MustCompile(`(\d+\.?\d*)\s*(KB|MB|GB|TB)`)
	matches := re.FindStringSubmatch(info)
	if len(matches) >= 3 {
		return fmt.Sprintf("%s %s", matches[1], matches[2])
	}
	return ""
}

// HealthCheck verifies the service is accessible
func (a *AnnasArchiveIndexer) HealthCheck(ctx context.Context) error {
	req, err := http.NewRequestWithContext(ctx, "HEAD", a.baseURL, nil)
	if err != nil {
		return err
	}

	resp, err := a.client.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("health check failed with status: %d", resp.StatusCode)
	}

	return nil
}

// SupportsCategory checks if the indexer supports a given category
func (a *AnnasArchiveIndexer) SupportsCategory(category string) bool {
	return strings.EqualFold(category, "books") ||
		strings.EqualFold(category, "ebooks")
}

// Helper function to parse size strings (reuse from libgen)
func parseSizeString(sizeStr string) int64 {
	sizeStr = strings.ToUpper(strings.TrimSpace(sizeStr))

	var size float64
	var unit string
	fmt.Sscanf(sizeStr, "%f %s", &size, &unit)

	multiplier := int64(1)
	switch {
	case strings.HasPrefix(unit, "KB"):
		multiplier = 1024
	case strings.HasPrefix(unit, "MB"):
		multiplier = 1024 * 1024
	case strings.HasPrefix(unit, "GB"):
		multiplier = 1024 * 1024 * 1024
	case strings.HasPrefix(unit, "TB"):
		multiplier = 1024 * 1024 * 1024 * 1024
	}

	return int64(size * float64(multiplier))
}
