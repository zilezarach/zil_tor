package x1337

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net/url"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/PuerkitoBio/goquery"
	"github.com/zilezarach/zil_tor-api/internal/bypass"
	"github.com/zilezarach/zil_tor-api/internal/models"
	"go.uber.org/zap"
)

type x1337Indexer struct {
	apiURL string
	client *bypass.HybridClient
	logger *zap.Logger
}

func (y *x1337Indexer) Name() string {
	return "1337x"
}

func X1337GenIndexer(client *bypass.HybridClient, logger *zap.Logger) *x1337Indexer {
	return &x1337Indexer{
		apiURL: "https://www.1337x.to",
		client: client,
		logger: logger,
	}
}

func (idx *x1337Indexer) Search(ctx context.Context, query string, limit int) ([]models.TorrentResult, error) {
	searchURL := fmt.Sprintf("%s/search/%s/1/", idx.apiURL, url.PathEscape(query))
	idx.logger.Info("Searching the 1337x index", zap.String("url", searchURL))
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

	idx.logger.Debug("Response received",
		zap.Int("status", resp.StatusCode),
		zap.Int("body_length", len(bodyStr)),
		zap.String("body_preview", bodyStr[:min(200, len(bodyStr))]))
	doc, err := goquery.NewDocumentFromReader(strings.NewReader(bodyStr))
	if err != nil {
		return nil, fmt.Errorf("failed to parse HTML: %w", err)
	}

	results := make([]models.TorrentResult, 0)
	detailURLs := make([]string, 0)

	// Parse search results to get detail page URLs
	doc.Find(".table-list tbody tr").Each(func(i int, s *goquery.Selection) {
		if len(detailURLs) >= limit {
			return
		}

		titleLink := s.Find(`td.coll-1.name a[href^="/torrent/"]`).First()
		title := strings.TrimSpace(titleLink.Text())
		detailPath, exists := titleLink.Attr("href")
		if !exists || title == "" {
			return
		}

		seedersText := strings.TrimSpace(s.Find("td.coll-2.seeds").Text())
		leechersText := strings.TrimSpace(s.Find("td.coll-3.leeches").Text())

		seeders, _ := strconv.Atoi(seedersText)
		leechers, _ := strconv.Atoi(leechersText)

		sizeCell := s.Find("td.coll-4.size")
		sizeStr := strings.TrimSpace(
			sizeCell.Clone().
				Children().
				Remove().
				End().
				Text(),
		)

		detailURL := idx.apiURL + detailPath
		detailURLs = append(detailURLs, detailURL)

		results = append(results, models.TorrentResult{
			Title:       title,
			Size:        sizeStr,
			SizeBytes:   parseSizeString(sizeStr),
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

	// Now fetch magnet links from detail pages
	idx.logger.Info("Fetching magnet links from detail pages",
		zap.Int("count", len(detailURLs)))

	results = idx.fetchAllMagnets(ctx, detailURLs, results)

	idx.logger.Info("1337x search complete",
		zap.String("query", query),
		zap.Int("results", len(results)))

	return results, nil
}

func (idx *x1337Indexer) fetchAllMagnets(ctx context.Context, detailURLs []string, results []models.TorrentResult) []models.TorrentResult {
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
				idx.logger.Debug("Magnet fetch failed", zap.String("url", url), zap.Error(err))
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

	idx.logger.Info("All magnets fetched", zap.Int("success", successCount))
	return results
}

func (idx *x1337Indexer) fetchMagnetLink(ctx context.Context, detailURL string) (string, error) {
	// Use the hybrid client - it will use cached session if available
	resp, err := idx.client.Get(ctx, detailURL)
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

func (x *x1337Indexer) HealthCheck(ctx context.Context) error {
	ctx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()

	req, err := x.client.Get(ctx, x.apiURL)
	if err != nil {
		return err
	}
	defer req.Body.Close()

	if req.StatusCode != 200 {
		return errors.New("1337x health check failed")
	}

	return nil
}

func (idx *x1337Indexer) SupportsCategory(category string) bool {
	return true
}
