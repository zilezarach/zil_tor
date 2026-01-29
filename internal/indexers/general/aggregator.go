package general

import (
	"context"
	"fmt"
	"strings"
	"sync"

	"github.com/zilezarach/zil_tor-api/internal/bypass"
	"github.com/zilezarach/zil_tor-api/internal/indexers/general/Ext"
	"github.com/zilezarach/zil_tor-api/internal/indexers/general/x1337"
	"github.com/zilezarach/zil_tor-api/internal/models"
	"go.uber.org/zap"
)

type GeneralAggregator struct {
	indexers []GeneralIndexers
	logger   *zap.Logger
}

type GeneralIndexers interface {
	Name() string
	Search(ctx context.Context, query string, limit int) ([]models.TorrentResult, error)
	HealthCheck(ctx context.Context) error
}

func NewGeneralAggregator(solverClient *bypass.HybridClient, logger *zap.Logger) *GeneralAggregator {
	return &GeneralAggregator{
		indexers: []GeneralIndexers{
			x1337.X1337GenIndexer(solverClient, logger),
			Ext.ExTGenIndexer(solverClient, logger),
		},
		logger: logger,
	}
}

func (r *GeneralAggregator) Name() string {
	return "General Indexers"
}

func (r *GeneralAggregator) Search(ctx context.Context, query string, limit int) ([]models.TorrentResult, error) {
	results := make([]models.TorrentResult, 0)
	resultsChan := make(chan []models.TorrentResult, len(r.indexers))
	var wg sync.WaitGroup

	for _, indexer := range r.indexers {
		wg.Add(1)
		go func(idx GeneralIndexers) {
			defer wg.Done()
			r.logger.Debug("Searching through general indexers", zap.String("source", idx.Name()), zap.String("query", query))
			indexerResults, err := idx.Search(ctx, query, limit)
			if err != nil {
				r.logger.Error("General Search failed", zap.String("source", idx.Name()), zap.Error(err))
				resultsChan <- []models.TorrentResult{}
				return
			}
			resultsChan <- indexerResults
		}(indexer)
	}
	go func() {
		wg.Wait()
		close(resultsChan)
	}()
	for indexerResults := range resultsChan {
		results = append(results, indexerResults...)
	}
	// sort by Seeders
	sortByQuality(results)
	if len(results) > limit {
		results = results[:limit]
	}

	return results, nil
}

func (r *GeneralAggregator) HealthCheck(ctx context.Context) error {
	var errors []string
	for _, indexer := range r.indexers {
		if err := indexer.HealthCheck(ctx); err != nil {
			errors = append(errors, fmt.Sprintf("%s: %v", indexer.Name(), err))
		}
	}
	if len(errors) == len(r.indexers) {
		return fmt.Errorf("All General Aggregator Failed: %s", strings.Join(errors, ";"))
	}
	return nil
}

func sortByQuality(results []models.TorrentResult) {
	for i := 0; i < len(results)-1; i++ {
		for j := i + 1; j < len(results); j++ {
			if results[i].Seeders < results[j].Seeders {
				results[i], results[j] = results[j], results[i]
			}
		}
	}
}

func (idx *GeneralAggregator) SupportsCategory(category string) bool {
	return true
}
