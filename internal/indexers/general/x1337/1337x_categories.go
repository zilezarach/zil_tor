package x1337

import (
	"fmt"
	"net/url"
	"strings"
)

// 1337x category mappings
const (
	Category1337xMovies      = "Movies"
	Category1337xTV          = "TV"
	Category1337xGames       = "Games"
	Category1337xMusic       = "Music"
	Category1337xApps        = "Apps"
	Category1337xAnime       = "Anime"
	Category1337xDocumentary = "Documentaries"
	Category1337xOther       = "Other"
)

// MapCategoryTo1337x maps generic category to 1337x category
func MapCategoryTo1337x(category string) string {
	categoryLower := strings.ToLower(category)

	switch {
	case strings.Contains(categoryLower, "movie"):
		return Category1337xMovies
	case strings.Contains(categoryLower, "tv") || strings.Contains(categoryLower, "television"):
		return Category1337xTV
	case strings.Contains(categoryLower, "game"):
		return Category1337xGames
	case strings.Contains(categoryLower, "music") || strings.Contains(categoryLower, "audio"):
		return Category1337xMusic
	case strings.Contains(categoryLower, "app") || strings.Contains(categoryLower, "software"):
		return Category1337xApps
	case strings.Contains(categoryLower, "anime"):
		return Category1337xAnime
	case strings.Contains(categoryLower, "doc"):
		return Category1337xDocumentary
	default:
		return ""
	}
}

// Build1337xCategoryURL builds URL with category filter
func Build1337xCategoryURL(baseURL, query, category string, page int) string {
	if category != "" {
		// Format: /category-search/query/category/page/
		categoryPath := strings.ToLower(category)
		return fmt.Sprintf("%s/category-search/%s/%s/%d/",
			baseURL,
			url.PathEscape(query),
			categoryPath,
			page,
		)
	}

	// Default search without category
	return fmt.Sprintf("%s/search/%s/%d/",
		baseURL,
		url.PathEscape(query),
		page,
	)
}
