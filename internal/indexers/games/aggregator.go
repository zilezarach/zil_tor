package games

import (
	"context"
	"fmt"
	"strings"
	"sync"

	"github.com/zilezarach/zil_tor-api/internal/bypass" // Updated to match your path
	"github.com/zilezarach/zil_tor-api/internal/indexers/games/dodi"
	"github.com/zilezarach/zil_tor-api/internal/indexers/games/fitgirl"
	"github.com/zilezarach/zil_tor-api/internal/models"
	"go.uber.org/zap"
)

// RepackAggregator combines multiple repack sources
type RepackAggregator struct {
	indexers []RepackIndexer
	logger   *zap.Logger
}

// RepackIndexer interface for repack sources
type RepackIndexer interface {
	Name() string
	Search(ctx context.Context, query string, limit int) ([]models.TorrentResult, error)
	GetLatestReleases(ctx context.Context, limit int) ([]models.TorrentResult, error)
	GetPopularReleases(ctx context.Context, limit int) ([]models.TorrentResult, error)
	HealthCheck(ctx context.Context) error // Added to interface for better HealthCheck logic
}

// NewRepackAggregator creates a new repack aggregator
func NewRepackAggregator(solverClient *bypass.HybridClient, logger *zap.Logger) *RepackAggregator {
	return &RepackAggregator{
		indexers: []RepackIndexer{
			// Using the actual constructor names from your dodi/fitgirl packages
			fitgirl.FitgirlGenIndexer(solverClient, logger),
			dodi.DODIGenIndexer(solverClient, logger),
		},
		logger: logger,
	}
}

func (r *RepackAggregator) Name() string {
	return "GameRepacks"
}

func (r *RepackAggregator) Search(ctx context.Context, query string, limit int) ([]models.TorrentResult, error) {
	results := make([]models.TorrentResult, 0)
	resultsChan := make(chan []models.TorrentResult, len(r.indexers))

	var wg sync.WaitGroup

	// Search all indexers concurrently
	for _, indexer := range r.indexers {
		wg.Add(1)
		go func(idx RepackIndexer) {
			defer wg.Done()

			r.logger.Debug("Searching repack source",
				zap.String("source", idx.Name()),
				zap.String("query", query))

			indexerResults, err := idx.Search(ctx, query, limit)
			if err != nil {
				r.logger.Error("Repack search failed",
					zap.String("source", idx.Name()),
					zap.Error(err))
				resultsChan <- []models.TorrentResult{}
				return
			}

			resultsChan <- indexerResults
		}(indexer)
	}

	// Close channel when done
	go func() {
		wg.Wait()
		close(resultsChan)
	}()

	// Collect results
	for indexerResults := range resultsChan {
		results = append(results, indexerResults...)
	}

	// Sort by seeders
	sortByQuality(results)

	// Limit results
	if len(results) > limit {
		results = results[:limit]
	}

	return results, nil
}

func (r *RepackAggregator) HealthCheck(ctx context.Context) error {
	var errors []string
	for _, indexer := range r.indexers {
		if err := indexer.HealthCheck(ctx); err != nil {
			errors = append(errors, fmt.Sprintf("%s: %v", indexer.Name(), err))
		}
	}

	if len(errors) == len(r.indexers) {
		return fmt.Errorf("all repack indexers unhealthy: %s", strings.Join(errors, "; "))
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

func (r *RepackAggregator) GetLatestReleases(ctx context.Context, limit int) ([]models.TorrentResult, error) {
	results := make([]models.TorrentResult, 0)

	for _, indexer := range r.indexers {
		releases, err := indexer.GetLatestReleases(ctx, limit/len(r.indexers))
		if err != nil {
			r.logger.Warn("Failed to get latest releases",
				zap.String("source", indexer.Name()),
				zap.Error(err))
			continue
		}
		results = append(results, releases...)
	}

	return results, nil
}

func (r *RepackAggregator) SupportsCategory(category string) bool {
	return strings.EqualFold(category, "games") ||
		strings.EqualFold(category, "pc games") ||
		strings.EqualFold(category, "repacks")
}
