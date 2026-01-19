package fitgirl

import (
	"context"
	"errors"
	"fmt"
	"io"
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

type FitgirlIndexer struct {
	ApiURL         string
	SearchPar      string
	Client         *bypass.HybridClient
	Logger         *zap.Logger
	IsValidRelease func(string) bool
}

func (y *FitgirlIndexer) Name() string {
	return "Fitgirl-repacks"
}

func FitgirlGenIndexer(client *bypass.HybridClient, logger *zap.Logger) *FitgirlIndexer {
	return &FitgirlIndexer{
		ApiURL:    "https://www.1337x.to",
		Client:    client,
		Logger:    logger,
		SearchPar: "Fitgirl",
		IsValidRelease: func(title string) bool {
			return strings.Contains(strings.ToLower(title), "fitgirl")
		},
	}
}

func (idx *FitgirlIndexer) Search(ctx context.Context, query string, limit int) ([]models.TorrentResult, error) {
	searchQuery := fmt.Sprintf("%s %s", idx.SearchPar, query)
	searchURL := fmt.Sprintf("%s/search/%s/1/", idx.ApiURL, url.PathEscape(searchQuery))
	idx.Logger.Info("Searching Fitgirl-repacks...", zap.String("url", searchURL))
	resp, err := idx.Client.Get(ctx, searchURL)
	if err != nil {
		return nil, fmt.Errorf("Failed to fetch search resp %w", err)
	}
	defer resp.Body.Close()
	bodyBytes, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("failed to read response body %w", err)
	}
	bodyStr := string(bodyBytes)
	idx.Logger.Debug("Response received",
		zap.Int("status", resp.StatusCode),
		zap.Int("body_length", len(bodyStr)),
		zap.String("body_preview", bodyStr[:min(200, len(bodyStr))]))
	doc, err := goquery.NewDocumentFromReader(strings.NewReader(bodyStr))
	if err != nil {
		return nil, fmt.Errorf("failed to parse HTML: %w", err)
	}

	results := make([]models.TorrentResult, 0)
	detailURLs := make([]string, 0)
	doc.Find(".table-list tbody tr").Each(func(i int, s *goquery.Selection) {
		if len(detailURLs) >= limit {
			return
		}

		titleLink := s.Find(".name a").Last()
		title := strings.TrimSpace(titleLink.Text())
		detailPath, exists := titleLink.Attr("href")
		if !exists || title == "" {
			return
		}
		if !idx.IsValidRelease(title) {
			return
		}

		// Get basic info
		seedersText := strings.TrimSpace(s.Find(".seeds").Text())
		leechersText := strings.TrimSpace(s.Find(".leeches").Text())
		sizeText := strings.TrimSpace(s.Find(".size").Text())

		seeders, _ := strconv.Atoi(seedersText)
		leechers, _ := strconv.Atoi(leechersText)

		// Parse size
		sizeParts := strings.Fields(sizeText)
		sizeStr := "Unknown"
		if len(sizeParts) >= 2 {
			sizeStr = sizeParts[0] + " " + sizeParts[1]
		}

		detailURL := fmt.Sprintf("%s%s", idx.ApiURL, detailPath)
		detailURLs = append(detailURLs, detailURL)

		// Extract game metadata from title
		metadata := extractFitGirlMetadata(title)

		results = append(results, models.TorrentResult{
			Title:       title,
			Size:        sizeStr,
			SizeBytes:   parseSizeString(sizeStr),
			Seeders:     seeders,
			Leechers:    leechers,
			Source:      idx.Name(),
			Category:    "Games",
			PublishDate: time.Now(),
			Extra: map[string]string{
				"detail_url":         detailURL,
				"repack_type":        "FitGirl",
				"game_name":          metadata.GameName,
				"version":            metadata.Version,
				"includes_dlcs":      fmt.Sprintf("%t", metadata.IncludesDLCs),
				"selective_download": fmt.Sprintf("%t", metadata.SelectiveDownload),
				"languages":          strings.Join(metadata.Languages, ", "),
			},
		})
	})

	// Fetch magnet links
	idx.Logger.Info("Fetching magnet links",
		zap.Int("count", len(detailURLs)))

	for i, detailURL := range detailURLs {
		magnet, err := idx.fetchMagnetLink(ctx, detailURL)
		if err != nil {
			idx.Logger.Warn("Failed to fetch magnet link",
				zap.String("url", detailURL),
				zap.Error(err))
			continue
		}

		if magnet != "" {
			results[i].MagnetURI = magnet
			results[i].InfoHash = extractInfoHash(magnet)
		}
	}

	idx.Logger.Info("Fetching magnet links from detail pages",
		zap.Int("count", len(detailURLs)))

	results = idx.fetchAllMagnets(ctx, detailURLs, results)

	idx.Logger.Info("Fitgirl search complete",
		zap.String("query", query),
		zap.Int("results", len(results)))

	return results, nil
}

func (idx *FitgirlIndexer) fetchAllMagnets(ctx context.Context, detailURLs []string, results []models.TorrentResult) []models.TorrentResult {
	var wg sync.WaitGroup
	var mu sync.Mutex

	sem := make(chan struct{}, 20)
	successCount := 0

	for i, detailURL := range detailURLs {
		wg.Add(1)
		go func(i int, url string) {
			defer wg.Done()

			select {
			case sem <- struct{}{}:
				defer func() { <-sem }()
			case <-ctx.Done():
				return
			}

			reqCtx, cancel := context.WithTimeout(ctx, 90*time.Second)
			defer cancel()

			magnet, err := idx.fetchMagnetLink(reqCtx, url)
			if err != nil {
				idx.Logger.Debug("Magnet fetch failed", zap.String("url", url), zap.Error(err))
				return
			}

			if magnet != "" && i < len(results) {
				mu.Lock()
				results[i].MagnetURI = magnet
				results[i].InfoHash = extractInfoHash(magnet)
				successCount++
				mu.Unlock()
			}
		}(i, detailURL)
	}

	wg.Wait()

	idx.Logger.Info("All magnets fetched", zap.Int("success", successCount))
	return results
}

func (idx *FitgirlIndexer) fetchMagnetLink(ctx context.Context, detailURL string) (string, error) {
	// Use the hybrid client - it will use cached session if available
	resp, err := idx.Client.Get(ctx, detailURL)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()

	doc, err := goquery.NewDocumentFromReader(resp.Body)
	if err != nil {
		return "", err
	}

	var magnet string
	doc.Find("a[href^='magnet:']").EachWithBreak(func(i int, s *goquery.Selection) bool {
		if href, exists := s.Attr("href"); exists {
			magnet = href
			return false
		}
		return true
	})

	if magnet == "" {
		return "", fmt.Errorf("no magnet link found")
	}

	return magnet, nil
}

// Helpers
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

func (f *FitgirlIndexer) GetLatestReleases(ctx context.Context, limit int) ([]models.TorrentResult, error) {
	// Search for recent FitGirl releases sorted by time
	searchURL := fmt.Sprintf("%s/sort-search/%s/time/desc/1/",
		f.ApiURL,
		url.PathEscape(f.SearchPar))

	f.Logger.Info("Fetching latest FitGirl releases", zap.String("url", searchURL))

	resp, err := f.Client.Get(ctx, searchURL)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	doc, err := goquery.NewDocumentFromReader(resp.Body)
	if err != nil {
		return nil, err
	}

	return f.ParseResults(ctx, doc, limit)
}

// GetPopularReleases gets popular FitGirl releases
func (f *FitgirlIndexer) GetPopularReleases(ctx context.Context, limit int) ([]models.TorrentResult, error) {
	// Search for FitGirl releases sorted by seeders
	searchURL := fmt.Sprintf("%s/sort-search/%s/seeders/desc/1/",
		f.ApiURL,
		url.PathEscape(f.SearchPar))

	f.Logger.Info("Fetching popular FitGirl releases", zap.String("url", searchURL))

	resp, err := f.Client.Get(ctx, searchURL)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	doc, err := goquery.NewDocumentFromReader(resp.Body)
	if err != nil {
		return nil, err
	}

	return f.ParseResults(ctx, doc, limit)
}

// parseResults parses search results from HTML
func (f *FitgirlIndexer) ParseResults(ctx context.Context, doc *goquery.Document, limit int) ([]models.TorrentResult, error) {
	results := make([]models.TorrentResult, 0)
	detailURLs := make([]string, 0)

	doc.Find(".table-list tbody tr").Each(func(i int, s *goquery.Selection) {
		if len(detailURLs) >= limit {
			return
		}

		titleLink := s.Find(".name a").Last()
		title := strings.TrimSpace(titleLink.Text())
		detailPath, exists := titleLink.Attr("href")
		if !exists || title == "" {
			return
		}

		if !isFitGirlRelease(title) {
			return
		}

		seedersText := strings.TrimSpace(s.Find(".seeds").Text())
		leechersText := strings.TrimSpace(s.Find(".leeches").Text())
		sizeText := strings.TrimSpace(s.Find(".size").Text())

		seeders, _ := strconv.Atoi(seedersText)
		leechers, _ := strconv.Atoi(leechersText)

		sizeParts := strings.Fields(sizeText)
		sizeStr := "Unknown"
		if len(sizeParts) >= 2 {
			sizeStr = sizeParts[0] + " " + sizeParts[1]
		}

		detailURL := fmt.Sprintf("%s%s", f.ApiURL, detailPath)
		detailURLs = append(detailURLs, detailURL)

		metadata := extractFitGirlMetadata(title)

		results = append(results, models.TorrentResult{
			Title:       title,
			Size:        sizeStr,
			SizeBytes:   parseSizeString(sizeStr),
			Seeders:     seeders,
			Leechers:    leechers,
			Source:      f.Name(),
			Category:    "Games",
			PublishDate: time.Now(),
			Extra: map[string]string{
				"detail_url":         detailURL,
				"repack_type":        "FitGirl",
				"game_name":          metadata.GameName,
				"version":            metadata.Version,
				"includes_dlcs":      fmt.Sprintf("%t", metadata.IncludesDLCs),
				"selective_download": fmt.Sprintf("%t", metadata.SelectiveDownload),
				"languages":          strings.Join(metadata.Languages, ", "),
			},
		})
	})

	// Fetch magnet links concurrently
	for i, detailURL := range detailURLs {
		magnet, err := f.fetchMagnetLink(ctx, detailURL)
		if err != nil {
			continue
		}

		if magnet != "" {
			results[i].MagnetURI = magnet
			results[i].InfoHash = extractInfoHash(magnet)
		}
	}

	return results, nil
}

func extractInfoHash(magnetLink string) string {
	re := regexp.MustCompile(`btih:([a-fA-F0-9]{40})`)
	matches := re.FindStringSubmatch(magnetLink)
	if len(matches) > 1 {
		return strings.ToUpper(matches[1])
	}
	return ""
}

func (x *FitgirlIndexer) HealthCheck(ctx context.Context) error {
	ctx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()

	req, err := x.Client.Get(ctx, x.ApiURL)
	if err != nil {
		return err
	}
	defer req.Body.Close()

	if req.StatusCode != 200 {
		return errors.New("1337x health check failed")
	}

	return nil
}

func (f *FitgirlIndexer) SupportsCategory(category string) bool {
	return strings.EqualFold(category, "games") ||
		strings.EqualFold(category, "pc games")
}
