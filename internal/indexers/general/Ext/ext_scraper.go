package Ext

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/cookiejar"
	"net/url"
	"regexp"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/PuerkitoBio/goquery"
	"github.com/zilezarach/zil_tor-api/internal/bypass"
	"github.com/zilezarach/zil_tor-api/internal/models"
	"go.uber.org/zap"
)

type ExTIndexer struct {
	apiURL     string
	client     *bypass.HybridClient
	logger     *zap.Logger
	httpClient *http.Client
}

type AjaxParams struct {
	TorrentID string
	SessID    string
	HMAC      string
	Timestamp string
	Token     string
}

func (y *ExTIndexer) Name() string {
	return "Ext"
}

func ExTGenIndexer(client *bypass.HybridClient, logger *zap.Logger) *ExTIndexer {
	return &ExTIndexer{
		apiURL:     "https://ext.to",
		client:     client,
		logger:     logger,
		httpClient: &http.Client{Timeout: 30 * time.Second},
	}
}

func (idx *ExTIndexer) Search(ctx context.Context, query string, limit int) ([]models.TorrentResult, error) {
	searchURL := fmt.Sprintf("%s/browse/?q=%s&with_adult=0", idx.apiURL, url.QueryEscape(query))
	idx.logger.Info("Searching the ExT index", zap.String("url", searchURL))
	resp, err := idx.client.Get(ctx, searchURL)
	if err != nil {
		return nil, fmt.Errorf("failed to fetch search resp %w", err)
	}
	defer resp.Body.Close()
	bodyBytes, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("failed to read response body: %w", err)
	}
	bodyStr := string(bodyBytes)
	idx.logger.Debug("Response Recieved", zap.Int("status", resp.StatusCode), zap.Int("body_length", len(bodyStr)), zap.String("body_preview", bodyStr[:min(200, len(bodyStr))]))
	doc, err := goquery.NewDocumentFromReader(strings.NewReader(bodyStr))
	if err != nil {
		return nil, fmt.Errorf("failed to parse HTML: %w", err)
	}
	results := make([]models.TorrentResult, 0)
	detailURLs := make([]string, 0)
	doc.Find(".table tbody tr").Each(func(i int, s *goquery.Selection) {
		if len(detailURLs) >= limit {
			return
		}
		titleLink := s.Find(`td.text-left a[href^=""]`).First()
		title := strings.TrimSpace(titleLink.Text())
		detailPath, exists := titleLink.Attr("href")
		if !exists || title == "" {
			return
		}
		seedersText := strings.TrimSpace(s.Find("td.hide-on-mob span.text-success").First().Text())
		leechersText := strings.TrimSpace(s.Find("td.hide-on-mob span.text-danger").First().Text())
		sizeText := strings.TrimSpace(s.Find("td.nowrap-td.hide-on-mob span").Eq(1).Text())
		sizeByters := parseSize(sizeText)
		seeders, _ := strconv.Atoi(seedersText)
		leechers, _ := strconv.Atoi(leechersText)
		detailURL := idx.apiURL + detailPath
		detailURLs = append(detailURLs, detailURL)
		results = append(results, models.TorrentResult{
			Title:       title,
			Size:        sizeText,
			SizeBytes:   sizeByters,
			Seeders:     seeders,
			Leechers:    leechers,
			Source:      idx.Name(),
			Category:    detectCategory(title),
			PublishDate: time.Now(),
			Extra: map[string]string{
				"detail_url": detailURL,
			},
		})
	})
	idx.logger.Info("Fetched detail URLs, now fetching magnet links", zap.Int("count", len(detailURLs)))
	// Collect magnet links
	results = idx.fetchAllMagnets(ctx, detailURLs, results)
	idx.logger.Info(" Ext Search complete", zap.Int("total_results", len(results)))
	return results, nil
}

func parseSize(size string) int64 {
	parts := strings.Fields(size)
	if len(parts) != 2 {
		return 0
	}

	value, err := strconv.ParseFloat(parts[0], 64)
	if err != nil {
		return 0
	}

	unit := strings.ToUpper(parts[1])
	switch unit {
	case "KB":
		return int64(value * 1024)
	case "MB":
		return int64(value * 1024 * 1024)
	case "GB":
		return int64(value * 1024 * 1024 * 1024)
	case "TB":
		return int64(value * 1024 * 1024 * 1024 * 1024)
	default:
		return 0
	}
}

func extractTorrentID(detailURL string) string {
	// Remove trailing slash
	detailURL = strings.TrimSuffix(detailURL, "/")

	// Extract the numeric ID at the end
	// URL format: https://ext.to/title-words-here-NUMERIC_ID/
	re := regexp.MustCompile(`-(\d+)/?$`)
	matches := re.FindStringSubmatch(detailURL)
	if len(matches) > 1 {
		return matches[1]
	}

	return ""
}

func (idx *ExTIndexer) fetchAllMagnets(ctx context.Context, detailURLs []string, results []models.TorrentResult) []models.TorrentResult {
	var wg sync.WaitGroup
	var mu sync.Mutex

	// Use a channel to limit concurrent operations
	type job struct {
		index int
		url   string
	}

	jobs := make(chan job, len(detailURLs))
	for i, url := range detailURLs {
		jobs <- job{index: i, url: url}
	}
	close(jobs)

	successCount := 0
	failCount := 0

	// Spawn worker goroutines
	numWorkers := 3 // Process 3 at a time
	for w := 0; w < numWorkers; w++ {
		wg.Add(1)
		go func(workerID int) {
			defer wg.Done()

			for job := range jobs {
				// Check if context is cancelled
				select {
				case <-ctx.Done():
					idx.logger.Warn("Worker stopping due to context cancellation",
						zap.Int("worker", workerID))
					return
				default:
				}

				// Create independent context for this fetch
				fetchCtx, cancel := context.WithTimeout(context.Background(), 90*time.Second)

				torrentIDStr := extractTorrentID(job.url)
				if torrentIDStr == "" {
					idx.logger.Warn("Failed to extract torrent ID",
						zap.Int("worker", workerID),
						zap.String("url", job.url))
					cancel()
					mu.Lock()
					failCount++
					mu.Unlock()
					continue
				}

				torrentID, err := strconv.Atoi(torrentIDStr)
				if err != nil {
					idx.logger.Warn("Invalid torrent ID",
						zap.Int("worker", workerID),
						zap.String("id", torrentIDStr))
					cancel()
					mu.Lock()
					failCount++
					mu.Unlock()
					continue
				}

				idx.logger.Debug("Worker fetching magnet",
					zap.Int("worker", workerID),
					zap.Int("index", job.index),
					zap.String("url", job.url))

				magnet, err := idx.fetchExtMagnet(fetchCtx, job.url, torrentID)
				cancel()

				if err != nil {
					idx.logger.Warn("Magnet fetch failed",
						zap.Int("worker", workerID),
						zap.Int("index", job.index),
						zap.String("url", job.url),
						zap.Error(err))
					mu.Lock()
					failCount++
					mu.Unlock()
					continue
				}

				idx.logger.Info("Successfully fetched magnet",
					zap.Int("worker", workerID),
					zap.Int("index", job.index),
					zap.String("magnet_preview", magnet[:min(100, len(magnet))]))

				if magnet != "" && job.index < len(results) {
					mu.Lock()
					results[job.index].MagnetURI = magnet
					if hash := extractInfoHash(magnet); hash != "" {
						results[job.index].InfoHash = hash
					}
					successCount++
					mu.Unlock()
				}
			}
		}(w)
	}

	wg.Wait()

	idx.logger.Info("Magnet fetching complete",
		zap.Int("successful", successCount),
		zap.Int("failed", failCount),
		zap.Int("total", len(detailURLs)))

	return results
}

// Helper function to extract info hash from magnet link
func extractInfoHash(magnet string) string {
	// Extract from magnet:?xt=urn:btih:HASH
	re := regexp.MustCompile(`btih:([a-fA-F0-9]{40}|[A-Z2-7]{32})`)
	matches := re.FindStringSubmatch(magnet)
	if len(matches) > 1 {
		return strings.ToUpper(matches[1])
	}
	return ""
}

func (idx *ExTIndexer) fetchExtMagnet(
	ctx context.Context,
	detailURL string,
	torrentID int,
) (string, error) {
	// Step 1: Fetch detail page via HybridClient (should solve CF if needed)
	detailResp, err := idx.client.Get(ctx, detailURL)
	if err != nil {
		idx.logger.Warn("Failed to get detail page", zap.Error(err))
		return "", err
	}
	defer detailResp.Body.Close()

	bodyBytes, err := io.ReadAll(detailResp.Body)
	if err != nil {
		return "", err
	}
	bodyStr := string(bodyBytes)

	pageToken, csrfToken := extractTokensFromHTML(bodyStr)
	if pageToken == "" || csrfToken == "" {
		idx.logger.Warn("Tokens missing from detail page",
			zap.String("preview", bodyStr[:min(400, len(bodyStr))]))
		return "", fmt.Errorf("pageToken or csrfToken not found")
	}

	timestamp := strconv.FormatInt(time.Now().Unix(), 10)

	// Compute HMAC (empty key, as per JS)
	dataToSign := fmt.Sprintf("%d|%s|%s", torrentID, timestamp, pageToken)
	hash := sha256.Sum256([]byte(dataToSign))
	hmacHex := hex.EncodeToString(hash[:])

	// Step 2: Build form data
	form := url.Values{}
	form.Set("torrent_id", strconv.Itoa(torrentID))
	form.Set("download_type", "magnet")
	form.Set("timestamp", timestamp)
	form.Set("sessid", csrfToken) // ← important: sessid = csrfToken
	form.Set("hmac", hmacHex)

	postBody := strings.NewReader(form.Encode())

	ajaxURL := idx.apiURL + "/ajax/getTorrentMagnet.php"

	// Step 3: Use HybridClient.Post instead of raw client
	headers := map[string]string{
		"Content-Type":     "application/x-www-form-urlencoded",
		"Referer":          detailURL, // ← very important
		"Origin":           idx.apiURL,
		"X-Requested-With": "XMLHttpRequest",
		"Accept":           "application/json, text/javascript, */*; q=0.01",
		"Accept-Language":  "en-US,en;q=0.9",
	}

	postResp, err := idx.client.Post(ctx, ajaxURL, postBody, headers)
	if err != nil {
		idx.logger.Warn("POST via HybridClient failed", zap.Error(err))
		return "", fmt.Errorf("POST failed: %w", err)
	}
	defer postResp.Body.Close()

	if postResp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(postResp.Body)
		idx.logger.Warn("Bad status on magnet AJAX",
			zap.Int("code", postResp.StatusCode),
			zap.String("body_preview", string(body)[:300]))
		return "", fmt.Errorf("ajax status %d", postResp.StatusCode)
	}

	// Parse JSON response
	var result struct {
		Success bool   `json:"success"`
		URL     string `json:"url"`
		Error   string `json:"error"`
	}
	if err := json.NewDecoder(postResp.Body).Decode(&result); err != nil {
		return "", fmt.Errorf("json decode failed: %w", err)
	}

	if !result.Success {
		return "", fmt.Errorf("server error: %s", result.Error)
	}

	if result.URL == "" || !strings.HasPrefix(result.URL, "magnet:") {
		return "", fmt.Errorf("invalid magnet: %s", result.URL)
	}

	idx.logger.Info("Magnet fetched successfully", zap.String("magnet_preview", result.URL[:80]))
	return result.URL, nil
}

// HELPERS
func (x *ExTIndexer) HealthCheck(ctx context.Context) error {
	ctx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	req, err := x.client.Get(ctx, x.apiURL)
	if err != nil {
		return err
	}
	defer req.Body.Close()
	if req.StatusCode != 200 {
		return errors.New("ExT indexer health check failed")
	}
	return nil
}

// convertCookies converts bypass.Cookie to http.Cookie
func convertCookies(cookies []*bypass.Cookie) []*http.Cookie {
	httpCookies := make([]*http.Cookie, len(cookies))
	for i, c := range cookies {
		httpCookies[i] = &http.Cookie{
			Name:     c.Name,
			Value:    c.Value,
			Domain:   c.Domain,
			Path:     c.Path,
			Expires:  c.Expires,
			HttpOnly: c.HttpOnly,
			Secure:   c.Secure,
		}
	}
	return httpCookies
}

func detectCategory(title string) string {
	titleLower := strings.ToLower(title)

	if regexp.MustCompile(`s\d{2}e\d{2}|season\s+\d+`).MatchString(titleLower) {
		return "TV"
	}
	if regexp.MustCompile(`\d{4}.*(?:1080p|720p|2160p|bluray|webrip)`).MatchString(titleLower) {
		return "Movies"
	}
	if regexp.MustCompile(`(?:repack|game|crack|codex|skidrow)`).MatchString(titleLower) {
		return "Games"
	}
	if regexp.MustCompile(`(?:pdf|epub|mobi|azw3|book)`).MatchString(titleLower) {
		return "Books"
	}

	return "Other"
}

func newJarWithCookies(cookies []*http.Cookie) *cookiejar.Jar {
	jar, _ := cookiejar.New(nil)
	u, _ := url.Parse("https://ext.to")
	jar.SetCookies(u, cookies)
	return jar
}

func (idx *ExTIndexer) SupportsCategory(category string) bool {
	return true
}
