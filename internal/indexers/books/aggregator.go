package books

import (
	"context"
	"fmt"
	"strings"
	"sync"

	"github.com/zilezarach/zil_tor-api/internal/indexers"
	"github.com/zilezarach/zil_tor-api/internal/indexers/books/annasarchive"
	"github.com/zilezarach/zil_tor-api/internal/indexers/books/libgen"
	"github.com/zilezarach/zil_tor-api/internal/models"
	"go.uber.org/zap"
)

// BookAggregator aggregates results from all book indexers (LibGen + Anna's Archive)
type BookAggregator struct {
	libgen *libgen.LibGenScraperIndexer
	annas  *annasarchive.AnnasArchiveIndexer
	logger *zap.Logger
}

// NewBookAggregator creates a new book aggregator
func NewBookAggregator(logger *zap.Logger) *BookAggregator {
	// Create LibGen indexer
	libgenIndexer := libgen.NewLibGenScraperIndexer(logger)
	if err := libgen.UpdateLibGenScraperWithConfig(libgenIndexer); err != nil {
		logger.Warn("Failed to update LibGen mirrors, using defaults", zap.Error(err))
	}

	// Create Anna's Archive indexer
	annasIndexer := annasarchive.NewAnnasArchiveIndexer(logger)

	return &BookAggregator{
		libgen: libgenIndexer,
		annas:  annasIndexer,
		logger: logger,
	}
}

// Name returns the aggregator name
func (b *BookAggregator) Name() string {
	return "BooksAll"
}

// SupportsCategory checks if this aggregator supports the category
func (b *BookAggregator) SupportsCategory(category string) bool {
	lower := strings.ToLower(category)
	return lower == "books" || lower == "ebooks" || lower == ""
}

// Search searches all book indexers concurrently
func (b *BookAggregator) Search(ctx context.Context, query string, limit int) ([]models.TorrentResult, error) {
	type searchResult struct {
		source  string
		results []models.TorrentResult
		err     error
	}

	resultsChan := make(chan searchResult, 2)
	var wg sync.WaitGroup

	// Search LibGen
	wg.Add(1)
	go func() {
		defer wg.Done()
		b.logger.Debug("Searching LibGen",
			zap.String("query", query),
			zap.Int("limit", limit))

		results, err := b.libgen.Search(ctx, query, limit)
		resultsChan <- searchResult{
			source:  "LibGen",
			results: results,
			err:     err,
		}
	}()

	// Search Anna's Archive
	wg.Add(1)
	go func() {
		defer wg.Done()
		b.logger.Debug("Searching Anna's Archive",
			zap.String("query", query),
			zap.Int("limit", limit))

		results, err := b.annas.Search(ctx, query, limit)
		resultsChan <- searchResult{
			source:  "AnnasArchive",
			results: results,
			err:     err,
		}
	}()

	// Close channel when all searches complete
	go func() {
		wg.Wait()
		close(resultsChan)
	}()

	// Collect results
	allResults := make([]models.TorrentResult, 0, limit*2)
	errors := make([]string, 0)

	for res := range resultsChan {
		if res.err != nil {
			b.logger.Warn("Book search failed",
				zap.String("source", res.source),
				zap.Error(res.err))
			errors = append(errors, fmt.Sprintf("%s: %v", res.source, res.err))
			continue
		}

		b.logger.Info("Book search completed",
			zap.String("source", res.source),
			zap.Int("results", len(res.results)))

		allResults = append(allResults, res.results...)
	}

	// If all indexers failed
	if len(errors) == 2 {
		return nil, fmt.Errorf("all book indexers failed: %s", strings.Join(errors, "; "))
	}

	// Remove duplicates based on MD5 (stored in InfoHash field)
	allResults = deduplicateBooks(allResults)

	// Sort by relevance
	sortBooksByRelevance(allResults, query)

	// Limit results
	if len(allResults) > limit {
		allResults = allResults[:limit]
	}

	b.logger.Info("Book aggregator search complete",
		zap.String("query", query),
		zap.Int("total_results", len(allResults)),
		zap.Int("errors", len(errors)))

	return allResults, nil
}

// HealthCheck checks if any book indexer is healthy
func (b *BookAggregator) HealthCheck(ctx context.Context) error {
	errors := make([]string, 0)

	// Check LibGen
	if err := b.libgen.HealthCheck(ctx); err != nil {
		errors = append(errors, fmt.Sprintf("LibGen: %v", err))
	}

	// Check Anna's Archive
	if err := b.annas.HealthCheck(ctx); err != nil {
		errors = append(errors, fmt.Sprintf("AnnasArchive: %v", err))
	}

	// If all indexers are unhealthy
	if len(errors) == 2 {
		return fmt.Errorf("all book indexers unhealthy: %s", strings.Join(errors, "; "))
	}

	return nil
}

// GetLibGen returns the LibGen indexer
func (b *BookAggregator) GetLibGen() indexers.Indexer {
	return b.libgen
}

// GetAnnasArchive returns the Anna's Archive indexer
func (b *BookAggregator) GetAnnasArchive() indexers.Indexer {
	return b.annas
}

// deduplicateBooks removes duplicate books based on MD5 hash
func deduplicateBooks(results []models.TorrentResult) []models.TorrentResult {
	seen := make(map[string]bool)
	unique := make([]models.TorrentResult, 0, len(results))

	for _, result := range results {
		// Use InfoHash (which contains MD5 for books) as unique identifier
		if result.InfoHash == "" {
			// If no MD5, keep it anyway
			unique = append(unique, result)
			continue
		}

		if !seen[result.InfoHash] {
			seen[result.InfoHash] = true
			unique = append(unique, result)
		}
	}

	return unique
}

// sortBooksByRelevance sorts books by relevance to the search query
func sortBooksByRelevance(results []models.TorrentResult, query string) {
	queryLower := strings.ToLower(query)
	queryWords := strings.Fields(queryLower)

	type scoredResult struct {
		result models.TorrentResult
		score  int
		index  int
	}

	scored := make([]scoredResult, len(results))
	for i, result := range results {
		score := 0
		titleLower := strings.ToLower(result.Title)

		// Exact match
		if titleLower == queryLower {
			score += 1000
		}

		// Contains full query
		if strings.Contains(titleLower, queryLower) {
			score += 500
		}

		// Matching words
		for _, word := range queryWords {
			if len(word) > 2 && strings.Contains(titleLower, word) {
				score += 100
			}
		}

		// Prefer newer books
		if !result.UploadDate.IsZero() {
			year := result.UploadDate.Year()
			if year > 2000 {
				score += (year - 2000)
			}
		}

		// Prefer certain formats
		ext := strings.ToLower(result.Category)
		switch ext {
		case "epub":
			score += 20
		case "pdf":
			score += 15
		case "mobi":
			score += 10
		}

		scored[i] = scoredResult{result: result, score: score, index: i}
	}

	// Simple bubble sort
	for i := 0; i < len(scored)-1; i++ {
		for j := i + 1; j < len(scored); j++ {
			if scored[i].score < scored[j].score ||
				(scored[i].score == scored[j].score && scored[i].index > scored[j].index) {
				scored[i], scored[j] = scored[j], scored[i]
			}
		}
	}

	for i, sr := range scored {
		results[i] = sr.result
	}
}

func parseYear(yearStr string) (int, error) {
	var year int
	_, err := fmt.Sscanf(yearStr, "%d", &year)
	return year, err
}
