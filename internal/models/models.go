package models

import "time"

// TorrentResult represents a single torrent result
type TorrentResult struct {
	Title       string            `json:"title"`
	MagnetURI   string            `json:"magnet_uri"`
	InfoHash    string            `json:"info_hash"`
	Size        string            `json:"size"`
	SizeBytes   int64             `json:"size_bytes"`
	Seeders     int               `json:"seeders"`
	Leechers    int               `json:"leechers"`
	Category    string            `json:"category"`
	Source      string            `json:"source"`
	PublishDate time.Time         `json:"publish_date"`
	UploadDate  time.Time         `json:"upload_date"`
	Quality     string            `json:"quality,omitempty"`
	Resolution  string            `json:"resolution,omitempty"`
	Codec       string            `json:"codec,omitempty"`
	Extra       map[string]string `json:"extra,omitempty"`
}

// TorrentInfo represents detailed information about a torrent
type TorrentInfo struct {
	URL         string     `json:"url"`
	Title       string     `json:"title"`
	MagnetURI   string     `json:"magnet_uri"`
	InfoHash    string     `json:"info_hash"`
	Category    string     `json:"category"`
	Type        string     `json:"type"`
	Language    string     `json:"language"`
	Size        string     `json:"size"`
	SizeBytes   int64      `json:"size_bytes"`
	Seeders     int        `json:"seeders"`
	Leechers    int        `json:"leechers"`
	UploadDate  time.Time  `json:"upload_date"`
	Uploader    string     `json:"uploader"`
	Description string     `json:"description"`
	Files       []FileInfo `json:"files,omitempty"`
}

// FileInfo represents a file within a torrent
type FileInfo struct {
	Name string `json:"name"`
	Size string `json:"size"`
}

// SearchResponse represents the API response
type SearchResponse struct {
	Query      string          `json:"query"`
	Results    []TorrentResult `json:"results"`
	TotalFound int             `json:"total_found"`
	SearchTime float64         `json:"search_time_ms"`
	Sources    []string        `json:"sources"`
	CacheHit   bool            `json:"cache_hit"`
	Category   string          `json:"category,omitempty"`
}

// IndexerInfo represents indexer information
type IndexerInfo struct {
	Name           string   `json:"name"`
	Enabled        bool     `json:"enabled"`
	Categories     []string `json:"categories,omitempty"`
	RequiresSolver bool     `json:"requires_solver"`
	Healthy        bool     `json:"healthy"`
	Features       []string `json:"features,omitempty"`
}

// HealthStatus represents system health
type HealthStatus struct {
	Status        string          `json:"status"`
	HealthyCount  int             `json:"healthy_count"`
	TotalIndexers int             `json:"total_indexers"`
	CacheEnabled  bool            `json:"cache_enabled"`
	SolverEnabled bool            `json:"solver_enabled,omitempty"`
	Indexers      map[string]bool `json:"indexers"`
	Uptime        string          `json:"uptime"`
}

// Stats represents indexer statistics
type Stats struct {
	Source       string  `json:"source"`
	TotalResults int     `json:"total_results"`
	AvgSeeders   float64 `json:"avg_seeders"`
	ResponseTime float64 `json:"response_time_ms"`
	Healthy      bool    `json:"healthy"`
	LastChecked  string  `json:"last_checked"`
}
