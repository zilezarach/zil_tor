package x1337

import (
	"context"
	"fmt"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/PuerkitoBio/goquery"
	"github.com/zilezarach/zil_tor-api/internal/bypass"
	"github.com/zilezarach/zil_tor-api/internal/models"
	"go.uber.org/zap"
)

// Advanced1337xIndexer with full py1337x-like features
type Advanced1337xIndexer struct {
	*x1337Indexer
}

func NewAdvanced1337xIndexer(solverClient *bypass.HybridClient, logger *zap.Logger) *Advanced1337xIndexer {
	return &Advanced1337xIndexer{
		x1337Indexer: X1337GenIndexer(solverClient, logger),
	}
}

// SearchWithOptions provides advanced search with sorting and category
func (idx *Advanced1337xIndexer) SearchWithOptions(
	ctx context.Context,
	query string,
	limit int,
	category string,
	sortBy Sort1337x,
	order Order1337x,
) ([]models.TorrentResult, error) {
	var searchURL string

	if category != "" {
		// Category search
		cat1337x := MapCategoryTo1337x(category)
		searchURL = Build1337xCategoryURL(idx.apiURL, query, cat1337x, 1)
	} else if sortBy != "" {
		// Sorted search
		searchURL = Build1337xSortedURL(idx.apiURL, query, 1, sortBy, order)
	} else {
		// Regular search
		searchURL = fmt.Sprintf("%s/search/%s/1/", idx.apiURL, query)
	}

	idx.logger.Info("Advanced 1337x search",
		zap.String("url", searchURL),
		zap.String("category", category),
		zap.String("sort", string(sortBy)))

	// Use the existing search logic
	resp, err := idx.client.Get(ctx, searchURL)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	doc, err := goquery.NewDocumentFromReader(resp.Body)
	if err != nil {
		return nil, err
	}

	return idx.parseSearchResults(ctx, doc, limit)
}

// Trending gets trending torrents
func (idx *Advanced1337xIndexer) Trending(ctx context.Context, category string) ([]models.TorrentResult, error) {
	url := fmt.Sprintf("%s/trending", idx.apiURL)
	if category != "" {
		cat1337x := MapCategoryTo1337x(category)
		if cat1337x != "" {
			url = fmt.Sprintf("%s/trending/w/%s/", idx.apiURL, strings.ToLower(cat1337x))
		}
	}

	resp, err := idx.client.Get(ctx, url)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	doc, err := goquery.NewDocumentFromReader(resp.Body)
	if err != nil {
		return nil, err
	}

	return idx.parseSearchResults(ctx, doc, 50)
}

// Top gets top 100 torrents
func (idx *Advanced1337xIndexer) Top(ctx context.Context, category string) ([]models.TorrentResult, error) {
	url := fmt.Sprintf("%s/top-100", idx.apiURL)
	if category != "" {
		cat1337x := MapCategoryTo1337x(category)
		if cat1337x != "" {
			url = fmt.Sprintf("%s/top-100-%s", idx.apiURL, strings.ToLower(cat1337x))
		}
	}

	resp, err := idx.client.Get(ctx, url)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	doc, err := goquery.NewDocumentFromReader(resp.Body)
	if err != nil {
		return nil, err
	}

	return idx.parseSearchResults(ctx, doc, 100)
}

// GetTorrentInfo gets detailed information about a specific torrent
func (idx *Advanced1337xIndexer) GetTorrentInfo(ctx context.Context, torrentURL string) (*models.TorrentInfo, error) {
	resp, err := idx.client.Get(ctx, torrentURL)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	doc, err := goquery.NewDocumentFromReader(resp.Body)
	if err != nil {
		return nil, err
	}

	info := &models.TorrentInfo{
		URL: torrentURL,
	}

	// Extract title
	info.Title = strings.TrimSpace(doc.Find("h1").First().Text())

	// Extract magnet link
	doc.Find("a[href^='magnet:']").Each(func(i int, s *goquery.Selection) {
		if href, exists := s.Attr("href"); exists {
			info.MagnetURI = href
			info.InfoHash = extractInfoHash(href)
		}
	})

	// Extract details
	doc.Find(".torrent-detail-page ul.list li").Each(func(i int, s *goquery.Selection) {
		text := s.Text()
		if strings.Contains(text, "Category") {
			info.Category = strings.TrimSpace(strings.Split(text, ":")[1])
		} else if strings.Contains(text, "Type") {
			info.Type = strings.TrimSpace(strings.Split(text, ":")[1])
		} else if strings.Contains(text, "Language") {
			info.Language = strings.TrimSpace(strings.Split(text, ":")[1])
		} else if strings.Contains(text, "Total size") {
			info.Size = strings.TrimSpace(strings.Split(text, ":")[1])
		} else if strings.Contains(text, "Seeders") {
			fmt.Sscanf(text, "Seeders: %d", &info.Seeders)
		} else if strings.Contains(text, "Leechers") {
			fmt.Sscanf(text, "Leechers: %d", &info.Leechers)
		} else if strings.Contains(text, "Date uploaded") {
			dateStr := strings.TrimSpace(strings.Split(text, ":")[1])
			info.UploadDate = parseUploadDate(dateStr)
		}
	})

	// Extract description
	info.Description = strings.TrimSpace(doc.Find("#description").Text())

	return info, nil
}

// parseSearchResults is a helper to parse search result pages
func (idx *Advanced1337xIndexer) parseSearchResults(ctx context.Context, doc *goquery.Document, limit int) ([]models.TorrentResult, error) {
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

		detailURL := fmt.Sprintf("%s%s", idx.apiURL, detailPath)
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

	// Fetch magnet links
	for i, detailURL := range detailURLs {
		magnet, err := idx.fetchMagnetLink(ctx, detailURL)
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

func parseUploadDate(dateStr string) time.Time {
	// Try parsing different formats
	formats := []string{
		"Jan. 2, 2006",
		"Jan 2, 2006",
		"2006-01-02",
	}

	for _, format := range formats {
		if t, err := time.Parse(format, dateStr); err == nil {
			return t
		}
	}

	return time.Now()
}

// Helper functions
func extractInfoHash(magnetLink string) string {
	re := regexp.MustCompile(`btih:([a-fA-F0-9]{40})`)
	matches := re.FindStringSubmatch(magnetLink)
	if len(matches) > 1 {
		return strings.ToUpper(matches[1])
	}
	return ""
}

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
