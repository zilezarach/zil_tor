package adapter

import (
	"time"

	"github.com/zilezarach/zil_tor-api/internal/models"
)

// JackettResult adapts our TorrentResult to Jackett's format
type JackettResult struct {
	Title                string    `json:"Title"`
	Link                 string    `json:"Link,omitempty"`
	MagnetURI            string    `json:"MagnetUri,omitempty"`
	InfoHash             string    `json:"InfoHash,omitempty"`
	Size                 int64     `json:"Size"`
	Seeders              int       `json:"Seeders"`
	Leechers             int       `json:"Leechers"`
	Category             string    `json:"CategoryDesc,omitempty"`
	PublishDate          time.Time `json:"PublishDate"`
	Tracker              string    `json:"Tracker,omitempty"`
	Details              string    `json:"Details,omitempty"`
	DownloadVolumeFactor float64   `json:"DownloadVolumeFactor"`
	UploadVolumeFactor   float64   `json:"UploadVolumeFactor"`
}

// JackettResponse is the response format compatible with Jackett
type JackettResponse struct {
	Results []JackettResult `json:"Results"`
}

// ToJackettFormat converts our results to Jackett format
func ToJackettFormat(results []models.TorrentResult) JackettResponse {
	jackettResults := make([]JackettResult, 0, len(results))

	for _, r := range results {
		jr := JackettResult{
			Title:                r.Title,
			MagnetURI:            r.MagnetURI,
			InfoHash:             r.InfoHash,
			Size:                 r.SizeBytes,
			Seeders:              r.Seeders,
			Leechers:             r.Leechers,
			Category:             r.Category,
			PublishDate:          r.PublishDate,
			Tracker:              r.Source,
			DownloadVolumeFactor: 1.0,
			UploadVolumeFactor:   1.0,
		}

		// Set Link (prefer magnet, fallback to extra.detail_url or extra.mirror)
		if r.MagnetURI != "" {
			jr.Link = r.MagnetURI
		} else if detailURL, ok := r.Extra["detail_url"]; ok {
			jr.Link = detailURL
		} else if mirror, ok := r.Extra["mirror"]; ok {
			jr.Link = mirror
		}

		jackettResults = append(jackettResults, jr)
	}

	return JackettResponse{
		Results: jackettResults,
	}
}
