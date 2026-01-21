package yts

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/zilezarach/zil_tor-api/internal/models"
	"go.uber.org/zap"
)

type YTSIndexer struct {
	apiURL string
	client *http.Client
	logger *zap.Logger
}

type YTSResponse struct {
	Status        string `json:"status"`
	StatusMessage string `json:"status_message"`
	Data          struct {
		MovieCount int `json:"movie_count"`
		Limit      int `json:"limit"`
		PageNumber int `json:"page_number"`
		Movies     []struct {
			ID              int      `json:"id"`
			URL             string   `json:"url"`
			ImdbCode        string   `json:"imdb_code"`
			Title           string   `json:"title"`
			TitleEnglish    string   `json:"title_english"`
			TitleLong       string   `json:"title_long"`
			Year            int      `json:"year"`
			Rating          float64  `json:"rating"`
			Runtime         int      `json:"runtime"`
			Genres          []string `json:"genres"`
			Summary         string   `json:"summary"`
			DescriptionFull string   `json:"description_full"`
			Language        string   `json:"language"`
			MpaRating       string   `json:"mpa_rating"`
			Torrents        []struct {
				URL              string `json:"url"`
				Hash             string `json:"hash"`
				Quality          string `json:"quality"`
				Type             string `json:"type"`
				Seeds            int    `json:"seeds"`
				Peers            int    `json:"peers"`
				Size             string `json:"size"`
				SizeBytes        int64  `json:"size_bytes"`
				DateUploaded     string `json:"date_uploaded"`
				DateUploadedUnix int64  `json:"date_uploaded_unix"`
			} `json:"torrents"`
		} `json:"movies"`
	} `json:"data"`
}

func ListMovies(logger *zap.Logger) *YTSIndexer {
	return &YTSIndexer{
		apiURL: "https://yts.bz/api/v2/list_movies.json",
		client: &http.Client{Timeout: 30 * time.Second},
		logger: logger,
	}
}

func (y *YTSIndexer) Name() string {
	return "YTS"
}

func (y *YTSIndexer) Search(ctx context.Context, query string, limit int) ([]models.TorrentResult, error) {
	searchURL := fmt.Sprintf("%s?query_term=%s&limit=%d&sort_by=seeders",
		y.apiURL,
		url.QueryEscape(query),
		limit,
	)

	y.logger.Info("Searching YTS", zap.String("query", query))

	req, err := http.NewRequestWithContext(ctx, "GET", searchURL, nil)
	if err != nil {
		return nil, err
	}

	resp, err := y.client.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	var apiResp YTSResponse
	if err := json.NewDecoder(resp.Body).Decode(&apiResp); err != nil {
		return nil, fmt.Errorf("failed to decode response: %w", err)
	}

	if apiResp.Status != "ok" {
		return nil, fmt.Errorf("API error: %s", apiResp.StatusMessage)
	}

	results := make([]models.TorrentResult, 0)

	for _, movie := range apiResp.Data.Movies {
		for _, torrent := range movie.Torrents {
			// Build magnet link
			magnetLink := fmt.Sprintf("magnet:?xt=urn:btih:%s&dn=%s&tr=udp://open.demonii.com:1337/announce&tr=udp://tracker.openbittorrent.com:80&tr=udp://tracker.coppersurfer.tk:6969&tr=udp://glotorrents.pw:6969/announce&tr=udp://tracker.opentrackr.org:1337/announce&tr=udp://torrent.gresille.org:80/announce&tr=udp://p4p.arenabg.com:1337&tr=udp://tracker.leechers-paradise.org:6969",
				torrent.Hash,
				url.QueryEscape(movie.TitleLong),
			)

			title := fmt.Sprintf("%s (%d) [%s] [%s]",
				movie.TitleEnglish,
				movie.Year,
				torrent.Quality,
				torrent.Type,
			)

			results = append(results, models.TorrentResult{
				Title:       title,
				MagnetURI:   magnetLink,
				InfoHash:    torrent.Hash,
				Size:        torrent.Size,
				SizeBytes:   torrent.SizeBytes,
				Seeders:     torrent.Seeds,
				Leechers:    torrent.Peers,
				Quality:     torrent.Quality,
				Resolution:  torrent.Quality,
				Source:      y.Name(),
				Category:    "Movies",
				PublishDate: parseUnixTime(torrent.DateUploadedUnix),
				Extra: map[string]string{
					"imdb":    movie.ImdbCode,
					"rating":  fmt.Sprintf("%.1f", movie.Rating),
					"runtime": fmt.Sprintf("%d min", movie.Runtime),
					"genres":  strings.Join(movie.Genres, ", "),
					"year":    fmt.Sprintf("%d", movie.Year),
					"type":    torrent.Type,
				},
			})
		}
	}

	y.logger.Info("YTS search complete",
		zap.String("query", query),
		zap.Int("results", len(results)))

	return results, nil
}

func (y *YTSIndexer) HealthCheck(ctx context.Context) error {
	req, err := http.NewRequestWithContext(ctx, "GET", "https://yts.bz", nil)
	if err != nil {
		return err
	}

	resp, err := y.client.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("unhealthy: status %d", resp.StatusCode)
	}

	return nil
}

func (y *YTSIndexer) SupportsCategory(category string) bool {
	return strings.EqualFold(category, "movies")
}

// Helper functions
func formatBytes(sizeStr string) string {
	size := parseInt64(sizeStr)
	if size == 0 {
		return sizeStr
	}

	const unit = 1024
	if size < unit {
		return fmt.Sprintf("%d B", size)
	}

	div, exp := int64(unit), 0
	for n := size / unit; n >= unit; n /= unit {
		div *= unit
		exp++
	}

	return fmt.Sprintf("%.1f %cB", float64(size)/float64(div), "KMGTPE"[exp])
}

func parseInt64(s string) int64 {
	var val int64
	fmt.Sscanf(s, "%d", &val)
	return val
}

func parseYear(yearStr string) time.Time {
	var year int
	fmt.Sscanf(yearStr, "%d", &year)
	if year > 0 && year < 3000 {
		return time.Date(year, 1, 1, 0, 0, 0, 0, time.UTC)
	}
	return time.Now()
}

func parseUnixTime(unix int64) time.Time {
	if unix > 0 {
		return time.Unix(unix, 0)
	}
	return time.Now()
}

func parseSizeString(sizeStr string) int64 {
	sizeStr = strings.ToUpper(strings.TrimSpace(sizeStr))

	var size float64
	var unit string
	fmt.Sscanf(sizeStr, "%f%s", &size, &unit)

	multiplier := int64(1)
	switch {
	case strings.Contains(unit, "KB"):
		multiplier = 1024
	case strings.Contains(unit, "MB"):
		multiplier = 1024 * 1024
	case strings.Contains(unit, "GB"):
		multiplier = 1024 * 1024 * 1024
	case strings.Contains(unit, "TB"):
		multiplier = 1024 * 1024 * 1024 * 1024
	}

	return int64(size * float64(multiplier))
}

func extractHashFromPath(path string) string {
	// Extract hash or ID from path
	parts := strings.Split(path, "/")
	if len(parts) > 0 {
		lastPart := parts[len(parts)-1]
		// Remove extension if present
		if idx := strings.Index(lastPart, "."); idx > 0 {
			return lastPart[:idx]
		}
		return lastPart
	}
	return ""
}

func detectCategory(title string) string {
	titleLower := strings.ToLower(title)

	if strings.Contains(titleLower, "s0") || strings.Contains(titleLower, "season") {
		return "TV"
	}
	if strings.Contains(titleLower, "game") || strings.Contains(titleLower, "repack") {
		return "Games"
	}
	if strings.Contains(titleLower, "book") || strings.Contains(titleLower, "pdf") || strings.Contains(titleLower, "epub") {
		return "Books"
	}

	return "Other"
}
