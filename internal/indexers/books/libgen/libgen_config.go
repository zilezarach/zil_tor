package libgen

import (
	"encoding/json"
	"net/http"
	"time"

	"go.uber.org/zap"
)

const ConfigURL = "https://raw.githubusercontent.com/obsfx/libgen-downloader/configuration/config.v3.json"

type LibGenConfig struct {
	LatestVersion string         `json:"latest_version"`
	Mirrors       []LibGenMirror `json:"mirrors"`
}

type LibGenMirrorConfig struct {
	Src  string `json:"src"`
	Type string `json:"type"`
}

// FetchLibGenConfig fetches the latest LibGen mirror configuration
func FetchLibGenConfig() (*LibGenConfig, error) {
	client := &http.Client{
		Timeout: 10 * time.Second,
	}

	resp, err := client.Get(ConfigURL)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	var configData struct {
		LatestVersion string               `json:"latest_version"`
		Mirrors       []LibGenMirrorConfig `json:"mirrors"`
	}

	if err := json.NewDecoder(resp.Body).Decode(&configData); err != nil {
		return nil, err
	}

	// Convert to internal format
	mirrors := make([]LibGenMirror, 0, len(configData.Mirrors))
	for _, m := range configData.Mirrors {
		mirrors = append(mirrors, LibGenMirror{
			URL:  m.Src,
			Type: m.Type,
		})
	}

	return &LibGenConfig{
		LatestVersion: configData.LatestVersion,
		Mirrors:       mirrors,
	}, nil
}

// UpdateLibGenScraperWithConfig updates the scraper with latest mirrors
func UpdateLibGenScraperWithConfig(scraper *LibGenScraperIndexer) error {
	config, err := FetchLibGenConfig()
	if err != nil {
		return err
	}

	scraper.mirrors = config.Mirrors
	scraper.logger.Info("Updated LibGen mirrors",
		zap.Int("count", len(config.Mirrors)),
		zap.String("version", config.LatestVersion))

	return nil
}
