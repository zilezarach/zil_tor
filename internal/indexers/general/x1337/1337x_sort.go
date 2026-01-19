package x1337

import (
	"fmt"
	"net/url"
)

// 1337x sort options
type Sort1337x string

const (
	Sort1337xTime     Sort1337x = "time"
	Sort1337xSize     Sort1337x = "size"
	Sort1337xSeeders  Sort1337x = "seeders"
	Sort1337xLeechers Sort1337x = "leechers"
)

// Order for sorting
type Order1337x string

const (
	Order1337xAsc  Order1337x = "asc"
	Order1337xDesc Order1337x = "desc"
)

// Build1337xSortedURL builds URL with sorting parameters
func Build1337xSortedURL(baseURL, query string, page int, sortBy Sort1337x, order Order1337x) string {
	if sortBy == "" {
		sortBy = Sort1337xSeeders // Default to seeders
	}
	if order == "" {
		order = Order1337xDesc // Default to descending
	}

	// Format: /sort-search/query/sortBy/order/page/
	return fmt.Sprintf("%s/sort-search/%s/%s/%s/%d/",
		baseURL,
		url.PathEscape(query),
		sortBy,
		order,
		page,
	)
}
