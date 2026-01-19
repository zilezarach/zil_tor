package libgen

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/PuerkitoBio/goquery"
	"github.com/zilezarach/zil_tor-api/internal/models"
	"go.uber.org/zap"
)

// LibGenScraperIndexer scrapes LibGen HTML pages for direct download links
type LibGenScraperIndexer struct {
	mirrors      []LibGenMirror
	logger       *zap.Logger
	client       *http.Client
	downloadPool chan struct{}
	cacheMutex   sync.RWMutex
	urlCache     map[string]string
}

// LibGenMirror represents a LibGen mirror
type LibGenMirror struct {
	URL  string
	Type string
}

// LibGenEntry represents a book entry from LibGen
type LibGenEntry struct {
	ID          string
	Authors     string
	Title       string
	Publisher   string
	Year        string
	Pages       string
	Language    string
	Size        string
	Extension   string
	Mirror      string
	MD5         string
	DownloadURL string
}

// DownloadProgress tracks download progress
type DownloadProgress struct {
	TotalBytes      int64
	DownloadedBytes int64
	StartTime       time.Time
	Speed           float64
}

// NewLibGenScraperIndexer creates a new LibGen scraper with download worker pool
func NewLibGenScraperIndexer(logger *zap.Logger) *LibGenScraperIndexer {
	// Default mirrors from config
	mirrors := []LibGenMirror{
		{URL: "https://libgen.li", Type: "libgen-plus"},
		{URL: "https://libgen.vg", Type: "libgen-plus"},
		{URL: "https://libgen.gl", Type: "libgen-plus"},
		{URL: "https://libgen.is", Type: "libgen-plus"},
	}

	return &LibGenScraperIndexer{
		mirrors: mirrors,
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

func (l *LibGenScraperIndexer) Name() string {
	return "LibGen"
}

func (l *LibGenScraperIndexer) Search(ctx context.Context, query string, limit int) ([]models.TorrentResult, error) {
	// Find working mirror
	workingMirror, err := l.findWorkingMirror(ctx)
	if err != nil {
		return nil, fmt.Errorf("no working mirrors found: %w", err)
	}

	l.logger.Info("Using LibGen mirror",
		zap.String("mirror", workingMirror.URL))

	// Build search URL
	searchURL := l.getSearchURL(workingMirror.URL, query, 1, limit)

	l.logger.Info("Searching LibGen",
		zap.String("query", query),
		zap.String("url", searchURL))

	// Fetch search results
	req, err := http.NewRequestWithContext(ctx, "GET", searchURL, nil)
	if err != nil {
		return nil, err
	}

	req.Header.Set("User-Agent", "Mozilla/5.0 (Windows NT 10.0; Win64; x64) AppleWebKit/537.36")

	resp, err := l.client.Do(req)
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
	entries := l.parseEntries(doc, workingMirror.URL)

	// Convert entries to results (no need for concurrent fetching since we just generate URLs)
	results := make([]models.TorrentResult, 0, len(entries))

	for i, entry := range entries {
		if i >= limit {
			break
		}

		// Generate download URL directly from MD5
		downloadURL := entry.Mirror
		if entry.MD5 != "" {
			// Use libgen.pw format which is most reliable
			downloadURL = fmt.Sprintf("https://libgen.pw/book/%s", entry.MD5)
		}

		result := models.TorrentResult{
			Title:       fmt.Sprintf("%s - %s (%s)", entry.Title, entry.Authors, entry.Year),
			MagnetURI:   downloadURL,
			InfoHash:    entry.MD5,
			Size:        entry.Size,
			SizeBytes:   parseSizeString(entry.Size),
			Category:    "Books",
			Source:      l.Name(),
			PublishDate: parseYear(entry.Year),
			Extra: map[string]string{
				"download_type": "direct",
				"authors":       entry.Authors,
				"publisher":     entry.Publisher,
				"pages":         entry.Pages,
				"language":      entry.Language,
				"extension":     entry.Extension,
				"md5":           entry.MD5,
				"mirror":        entry.Mirror,
			},
		}

		results = append(results, result)
	}

	l.logger.Info("LibGen search complete",
		zap.String("query", query),
		zap.Int("results", len(results)))

	return results, nil
}

// DownloadFile downloads a file from LibGen with progress tracking
func (l *LibGenScraperIndexer) DownloadFile(
	ctx context.Context,
	url string,
	destPath string,
	progressCallback func(progress DownloadProgress),
) error {
	// Acquire slot from download pool
	select {
	case l.downloadPool <- struct{}{}:
		defer func() { <-l.downloadPool }()
	case <-ctx.Done():
		return ctx.Err()
	}

	l.logger.Info("Starting download",
		zap.String("url", url),
		zap.String("destination", destPath))

	// Create request
	req, err := http.NewRequestWithContext(ctx, "GET", url, nil)
	if err != nil {
		return err
	}

	req.Header.Set("User-Agent", "Mozilla/5.0 (Windows NT 10.0; Win64; x64) AppleWebKit/537.36")

	// Execute request
	resp, err := l.client.Do(req)
	if err != nil {
		return fmt.Errorf("download request failed: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("download failed with status: %d", resp.StatusCode)
	}

	// Get content length
	totalBytes := resp.ContentLength

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
	progress := DownloadProgress{
		TotalBytes: totalBytes,
		StartTime:  time.Now(),
	}

	buffer := make([]byte, 32*1024) // 32KB buffer
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
				progress.DownloadedBytes = downloadedBytes

				// Calculate speed
				elapsed := time.Since(progress.StartTime).Seconds()
				if elapsed > 0 {
					progress.Speed = float64(downloadedBytes) / elapsed
				}

				// Call progress callback
				if progressCallback != nil {
					progressCallback(progress)
				}
			}

			if err == io.EOF {
				l.logger.Info("Download completed",
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

// GetCachedDownloadURL retrieves a cached download URL or fetches a new one
func (l *LibGenScraperIndexer) GetCachedDownloadURL(ctx context.Context, baseURL, md5 string) (string, error) {
	// Check cache first
	l.cacheMutex.RLock()
	if cachedURL, found := l.urlCache[md5]; found {
		l.cacheMutex.RUnlock()
		l.logger.Debug("Using cached download URL", zap.String("md5", md5))
		return cachedURL, nil
	}
	l.cacheMutex.RUnlock()

	// Fetch new URL
	url, err := l.getDownloadURL(ctx, baseURL, md5)
	if err != nil {
		return "", err
	}

	// Cache it
	l.cacheMutex.Lock()
	l.urlCache[md5] = url
	l.cacheMutex.Unlock()

	return url, nil
}

// findWorkingMirror tests mirrors and returns the first working one
func (l *LibGenScraperIndexer) findWorkingMirror(ctx context.Context) (*LibGenMirror, error) {
	// Try mirrors in parallel for faster detection
	type mirrorResult struct {
		mirror *LibGenMirror
		err    error
	}

	resultChan := make(chan mirrorResult, len(l.mirrors))
	testCtx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()

	for i := range l.mirrors {
		mirror := &l.mirrors[i]
		go func(m *LibGenMirror) {
			req, err := http.NewRequestWithContext(testCtx, "HEAD", m.URL, nil)
			if err != nil {
				resultChan <- mirrorResult{nil, err}
				return
			}

			resp, err := l.client.Do(req)
			if err != nil {
				l.logger.Debug("Mirror failed", zap.String("mirror", m.URL), zap.Error(err))
				resultChan <- mirrorResult{nil, err}
				return
			}
			resp.Body.Close()

			if resp.StatusCode == http.StatusOK {
				resultChan <- mirrorResult{m, nil}
			} else {
				resultChan <- mirrorResult{nil, fmt.Errorf("status %d", resp.StatusCode)}
			}
		}(mirror)
	}

	// Return first working mirror
	for i := 0; i < len(l.mirrors); i++ {
		result := <-resultChan
		if result.err == nil && result.mirror != nil {
			return result.mirror, nil
		}
	}

	return nil, fmt.Errorf("no working mirrors available")
}

// getSearchURL builds the search URL
func (l *LibGenScraperIndexer) getSearchURL(baseURL, query string, page, pageSize int) string {
	u, _ := url.Parse(baseURL)
	u.Path = "/index.php"

	q := u.Query()
	q.Set("req", query)
	q.Set("page", strconv.Itoa(page))
	q.Set("res", strconv.Itoa(pageSize))
	u.RawQuery = q.Encode()

	return u.String()
}

// parseEntries extracts book entries from the HTML table
func (l *LibGenScraperIndexer) parseEntries(doc *goquery.Document, baseURL string) []LibGenEntry {
	entries := make([]LibGenEntry, 0)

	// Find the main table
	table := doc.Find("#tablelibgen > tbody")
	if table.Length() == 0 {
		l.logger.Warn("Table not found in HTML")
		return entries
	}

	// Parse each row
	table.Find("tr").Each(func(i int, row *goquery.Selection) {
		cells := row.Find("td")
		if cells.Length() < 9 {
			return // Skip invalid rows
		}

		// Extract title (first cell, excluding NOBR elements)
		titleCell := cells.Eq(0)
		titleParts := make([]string, 0)
		titleCell.Contents().Each(func(j int, node *goquery.Selection) {
			if goquery.NodeName(node) != "nobr" {
				text := strings.TrimSpace(node.Text())
				if text != "" {
					titleParts = append(titleParts, text)
				}
			}
		})
		title := strings.Join(titleParts, " / ")

		// Extract authors (split by semicolon)
		authors := strings.TrimSpace(cells.Eq(1).Text())
		authorList := strings.Split(authors, ";")
		cleanAuthors := make([]string, 0)
		for _, author := range authorList {
			if trimmed := strings.TrimSpace(author); trimmed != "" {
				cleanAuthors = append(cleanAuthors, trimmed)
			}
		}
		authorsStr := strings.Join(cleanAuthors, ", ")

		// Extract other fields
		publisher := strings.TrimSpace(cells.Eq(2).Text())
		year := strings.TrimSpace(cells.Eq(3).Text())
		language := strings.TrimSpace(cells.Eq(4).Text())
		pages := strings.TrimSpace(cells.Eq(5).Text())
		size := strings.TrimSpace(cells.Eq(6).Text())
		extension := strings.TrimSpace(cells.Eq(7).Text())

		// Extract mirror link and MD5
		mirrorCell := cells.Eq(8)
		mirrorLink := ""
		md5 := ""

		mirrorCell.Find("a").Each(func(j int, a *goquery.Selection) {
			if href, exists := a.Attr("href"); exists {
				mirrorLink = href
				// Extract MD5 from URL
				if strings.Contains(href, "md5=") {
					parts := strings.Split(href, "md5=")
					if len(parts) > 1 {
						md5 = strings.Split(parts[1], "&")[0]
					}
				}
			}
		})

		// Make mirror link absolute
		if mirrorLink != "" && !strings.HasPrefix(mirrorLink, "http") {
			mirrorLink = baseURL + mirrorLink
		}

		entry := LibGenEntry{
			ID:        fmt.Sprintf("libgen_%d", i),
			Authors:   authorsStr,
			Title:     title,
			Publisher: publisher,
			Year:      year,
			Pages:     pages,
			Language:  language,
			Size:      size,
			Extension: extension,
			Mirror:    mirrorLink,
			MD5:       md5,
		}

		entries = append(entries, entry)
	})

	return entries
}

// getDownloadURL fetches the actual download URL from the detail page
func (l *LibGenScraperIndexer) getDownloadURL(ctx context.Context, baseURL, md5 string) (string, error) {
	if md5 == "" {
		return "", fmt.Errorf("MD5 is empty")
	}

	// Try multiple mirrors in order of preference
	mirrors := []string{
		"https://libgen.li",
		"https://libgen.is",
		"https://libgen.st",
	}

	var lastErr error

	for _, mirror := range mirrors {
		// Build the ads.php URL - this is where the download link is
		adsURL := fmt.Sprintf("%s/ads.php?md5=%s", mirror, md5)

		l.logger.Debug("Fetching download URL from ads page",
			zap.String("md5", md5),
			zap.String("mirror", mirror),
			zap.String("url", adsURL))

		// Fetch the ads page
		req, err := http.NewRequestWithContext(ctx, "GET", adsURL, nil)
		if err != nil {
			lastErr = err
			continue
		}

		req.Header.Set("User-Agent", "Mozilla/5.0 (Windows NT 10.0; Win64; x64) AppleWebKit/537.36")
		req.Header.Set("Accept", "text/html,application/xhtml+xml,application/xml;q=0.9,*/*;q=0.8")
		req.Header.Set("Referer", mirror)

		resp, err := l.client.Do(req)
		if err != nil {
			l.logger.Debug("Mirror failed", zap.String("mirror", mirror), zap.Error(err))
			lastErr = err
			continue
		}
		defer resp.Body.Close()

		if resp.StatusCode != http.StatusOK {
			lastErr = fmt.Errorf("ads page returned status %d", resp.StatusCode)
			continue
		}

		// Parse the HTML
		doc, err := goquery.NewDocumentFromReader(resp.Body)
		if err != nil {
			lastErr = err
			continue
		}

		// Method 1: Look for get.php links (most common pattern)
		// Format: https://libgen.li/get.php?md5=XXX&key=YYY
		var downloadURL string

		doc.Find("a[href*='get.php']").Each(func(i int, s *goquery.Selection) {
			if downloadURL != "" {
				return
			}
			if href, exists := s.Attr("href"); exists {
				// Make absolute URL if relative
				if !strings.HasPrefix(href, "http") {
					if strings.HasPrefix(href, "/") {
						href = mirror + href
					} else {
						href = mirror + "/" + href
					}
				}
				// Validate it has both md5 and key parameters
				if strings.Contains(href, "md5=") && strings.Contains(href, "key=") {
					downloadURL = href
					l.logger.Debug("Found get.php download link",
						zap.String("mirror", mirror),
						zap.String("url", downloadURL))
				}
			}
		})

		// Method 2: Look for links with "GET" or "Download" text that contain get.php
		if downloadURL == "" {
			doc.Find("a").Each(func(i int, s *goquery.Selection) {
				if downloadURL != "" {
					return
				}

				text := strings.ToUpper(s.Text())
				href, exists := s.Attr("href")

				if exists && (strings.Contains(text, "GET") || strings.Contains(text, "DOWNLOAD")) {
					// Make absolute URL if relative
					if !strings.HasPrefix(href, "http") {
						if strings.HasPrefix(href, "/") {
							href = mirror + href
						} else {
							href = mirror + "/" + href
						}
					}

					// Check if it's a get.php link
					if strings.Contains(href, "get.php") && strings.Contains(href, "md5=") {
						downloadURL = href
						l.logger.Debug("Found download link via text search",
							zap.String("mirror", mirror),
							zap.String("text", text),
							zap.String("url", downloadURL))
					}
				}
			})
		}

		// Method 3: Look for Cloudflare-IPFS links (alternative download method)
		if downloadURL == "" {
			doc.Find("a[href*='cloudflare-ipfs.com'], a[href*='ipfs.io']").Each(func(i int, s *goquery.Selection) {
				if downloadURL != "" {
					return
				}
				if href, exists := s.Attr("href"); exists {
					downloadURL = href
					l.logger.Debug("Found IPFS download link",
						zap.String("mirror", mirror),
						zap.String("url", downloadURL))
				}
			})
		}

		// If we found a download URL, return it
		if downloadURL != "" {
			// Clean up the URL
			downloadURL = strings.TrimSpace(downloadURL)
			return downloadURL, nil
		}

		lastErr = fmt.Errorf("no download link found on ads page")
	}

	// All mirrors failed
	if lastErr != nil {
		return "", fmt.Errorf("failed to get download URL from all mirrors: %w", lastErr)
	}

	return "", fmt.Errorf("could not find download link")
}

func (l *LibGenScraperIndexer) HealthCheck(ctx context.Context) error {
	_, err := l.findWorkingMirror(ctx)
	return err
}

func (l *LibGenScraperIndexer) SupportsCategory(category string) bool {
	return strings.EqualFold(category, "books") ||
		strings.EqualFold(category, "ebooks")
}

// Helper functions
func parseSizeString(sizeStr string) int64 {
	sizeStr = strings.ToUpper(strings.TrimSpace(sizeStr))
	sizeStr = strings.Trim(sizeStr, "()")
	sizeStr = strings.Trim(sizeStr, "[]")

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

func parseYear(yearStr string) time.Time {
	yearStr = strings.TrimSpace(yearStr)
	var year int
	fmt.Sscanf(yearStr, "%d", &year)

	if year > 1000 && year < 3000 {
		return time.Date(year, 1, 1, 0, 0, 0, 0, time.UTC)
	}

	return time.Now()
}

func (l *LibGenScraperIndexer) GetMirrors() []LibGenMirror {
	return l.mirrors
}

// ClearURLCache clears the cached download URLs
func (l *LibGenScraperIndexer) ClearURLCache() {
	l.cacheMutex.Lock()
	l.urlCache = make(map[string]string)
	l.cacheMutex.Unlock()
	l.logger.Info("Download URL cache cleared")
}
