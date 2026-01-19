package indexers

import (
	"context"

	"github.com/zilezarach/zil_tor-api/internal/models"
)

type Indexer interface {
	Name() string
	Search(ctx context.Context, query string, limit int) ([]models.TorrentResult, error)
	HealthCheck(ctx context.Context) error
	SupportsCategory(category string) bool
}
