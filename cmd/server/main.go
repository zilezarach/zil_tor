package main

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"os"
	"os/signal"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"syscall"
	"time"

	"github.com/PuerkitoBio/goquery"
	"github.com/gin-gonic/gin"
	"github.com/zilezarach/zil_tor-api/internal/bypass"
	"github.com/zilezarach/zil_tor-api/internal/cache"
	"github.com/zilezarach/zil_tor-api/internal/indexers"
	"github.com/zilezarach/zil_tor-api/internal/indexers/books"
	"github.com/zilezarach/zil_tor-api/internal/indexers/books/annasarchive"
	"github.com/zilezarach/zil_tor-api/internal/indexers/books/libgen"
	"github.com/zilezarach/zil_tor-api/internal/indexers/games"
	"github.com/zilezarach/zil_tor-api/internal/indexers/games/dodi"
	"github.com/zilezarach/zil_tor-api/internal/indexers/games/fitgirl"
	"github.com/zilezarach/zil_tor-api/internal/indexers/general/x1337"
	"github.com/zilezarach/zil_tor-api/internal/indexers/movies/yts"
	"github.com/zilezarach/zil_tor-api/internal/logger"
	"github.com/zilezarach/zil_tor-api/internal/models"
	"go.uber.org/zap"
)

var startTime = time.Now()

type Config struct {
	Port            string
	CacheEnabled    bool
	Cachettl        time.Duration
	SolverEnabled   bool
	FlareSolverrURL string
	LogLevel        string
	APIKey          string
	MaxConcurrency  int
}

type Server struct {
	router       *gin.Engine
	logger       *zap.Logger
	config       *Config
	cache        *cache.Cache
	solver       *bypass.Solver
	solverClient *bypass.HybridClient
	indexers     map[string]indexers.Indexer
	// Worker pool for concurrent searches
	workerPool    chan struct{}
	downloadCache *sync.Map
}

type CachedDownload struct {
	URL     string
	Created time.Time
	MD5     string
}

func main() {
	// Optimize for performance
	runtime.GOMAXPROCS(runtime.NumCPU())

	log := logger.New("info")
	config := &Config{
		Port:            getEnv("Port", "9117"),
		CacheEnabled:    getEnv("CacheEnabled", "true") == "true",
		Cachettl:        300 * time.Second,
		SolverEnabled:   getEnv("SolverEnabled", "true") == "true",
		FlareSolverrURL: getEnv("FLARESOLVERR_URL", "http://localhost:8191"),
		LogLevel:        getEnv("LOG_LEVEL", "info"),
		MaxConcurrency:  50,
	}

	server := NewServer(config, log)

	srv := &http.Server{
		Addr:           ":" + config.Port,
		Handler:        server.router,
		ReadTimeout:    120 * time.Second,
		WriteTimeout:   120 * time.Second,
		MaxHeaderBytes: 1 << 20,
	}

	// Graceful shutdown
	go func() {
		sigint := make(chan os.Signal, 1)
		signal.Notify(sigint, os.Interrupt, syscall.SIGTERM)
		<-sigint

		log.Info("Shutting down server...")
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()

		if err := srv.Shutdown(ctx); err != nil {
			log.Fatal("Server forced shutdown", zap.Error(err))
		}
	}()

	log.Info("🚀 Server starting",
		zap.String("port", config.Port),
		zap.String("url", fmt.Sprintf("http://localhost:%s", config.Port)),
		zap.Bool("cache", config.CacheEnabled),
		zap.Int("max_concurrency", config.MaxConcurrency))

	if err := srv.ListenAndServe(); err != nil && err != http.ErrServerClosed {
		log.Fatal("Server failed", zap.Error(err))
	}
}

func NewServer(config *Config, logger *zap.Logger) *Server {
	gin.SetMode(gin.ReleaseMode)

	server := &Server{
		router:        gin.New(),
		config:        config,
		logger:        logger,
		indexers:      make(map[string]indexers.Indexer),
		workerPool:    make(chan struct{}, config.MaxConcurrency),
		downloadCache: &sync.Map{},
	}

	// Middleware
	server.router.Use(gin.Recovery())
	server.router.Use(func(c *gin.Context) {
		start := time.Now()
		c.Next()
		logger.Debug("Request",
			zap.String("method", c.Request.Method),
			zap.String("path", c.Request.URL.Path),
			zap.Int("status", c.Writer.Status()),
			zap.Duration("duration", time.Since(start)))
	})

	if config.CacheEnabled {
		server.cache = cache.NewCache(config.Cachettl)
		logger.Info("Cache enabled", zap.Duration("ttl", config.Cachettl))
	}

	if config.SolverEnabled {
		solverConfig := &bypass.Config{
			Timeout:      45 * time.Second, // Reduced from 60s
			MaxInstances: 5,                // Increased pool
			Headless:     true,
			BrowserPath:  "",
		}
		customSolver := bypass.NewSolver(solverConfig, logger)
		server.solverClient = bypass.NewHybridClient(
			customSolver,
			config.FlareSolverrURL,
			logger,
		)
		logger.Info("✅ Hybrid solver ready")
	}

	server.RegisterIndexers(server.solverClient)
	server.SetupRoutes()

	return server
}

func (s *Server) SetupRoutes() {
	api := s.router.Group("/api/v1")
	{
		api.GET("/search", s.handleSearch)
		api.GET("/indexers", s.handleIndexers)
		api.GET("/health", s.handleHealth)
		api.GET("/stats", s.handleStats)
		api.GET("/download/proxy", s.handleProxyDownload)
		api.GET("/download/url", s.handleDirectDownload)
	}
	games := api.Group("/games")
	{
		games.GET("fitgirl/latest", s.handleFitGirlLatest)
		games.GET("fitgirl/popular", s.handleFitGirlPopular)
		games.GET("dodi/latest", s.handleDODILatest)
		games.GET("dodi/popular", s.handleDODIPopular)
		games.GET("/repacks/search", s.handleRepacksSearch)
		games.GET("/repacks/latest", s.handleRepacksLatest)
	}
	books := api.Group("/books")
	{
		books.GET("libgen/search", s.handleLibGenSearch)
		books.GET("/libgen/mirrors", s.handleLibGenMirrors)
		books.GET("/libgen/health", s.handleLibGenHealth)
		books.GET("/libgen/download", s.handleDirectDownload)
		// AnnasArchive
		books.GET("/annas/search", s.handleAnnasArchiveSearch)
		books.GET("/annas/info", s.handleAnnasArchiveInfo)
		books.GET("/annas/download", s.handleAnnasArchiveDownload)
		books.GET("/annas/download/proxy", s.handleAnnasArchiveProxyDownload)
		// Aggregator use Both Source
		books.GET("/search", s.handleBooksSearch)
	}
	movies := api.Group("/movies")
	{
		// YTS
		movies.GET("/search", s.handleYTSSearch)
	}
}

func (s *Server) RegisterIndexers(solverClient *bypass.HybridClient) {
	s.indexers["YTS"] = yts.ListMovies(s.logger)
	s.logger.Info("YTS Indexers Loaded")
	s.indexers["1337x"] = x1337.X1337GenIndexer(solverClient, s.logger)
	s.logger.Info("1337x Indexers Loaded")
	s.indexers["Fitgirl"] = fitgirl.FitgirlGenIndexer(solverClient, s.logger)
	s.logger.Info("Fitgirl Indexers Loaded")
	s.indexers["DODI"] = dodi.DODIGenIndexer(solverClient, s.logger)
	s.logger.Info("Dodi indexers added")
	s.indexers["annas"] = annasarchive.NewAnnasArchiveIndexer(s.logger)
	s.logger.Info("Anna indexers Loaded")
	s.indexers["Repacks"] = games.NewRepackAggregator(solverClient, s.logger)
	libgenScraper := libgen.NewLibGenScraperIndexer(s.logger)
	if err := libgen.UpdateLibGenScraperWithConfig(libgenScraper); err != nil {
		s.logger.Warn("Failed to update LibGen mirrors, using defaults", zap.Error(err))
	}
	s.indexers["libgen"] = libgenScraper
	s.logger.Info("Libgen Indexers Loaded")
	bookComb := books.NewBookAggregator(s.logger)
	s.indexers["Books"] = bookComb
	s.indexers["libgen"] = bookComb.GetLibGen()
	s.indexers["annas"] = bookComb.GetAnnasArchive()
	s.logger.Info("📊 Indexers ready", zap.Int("count", len(s.indexers)))
}

// YTS handler
func (s *Server) handleYTSSearch(c *gin.Context) {
	query := c.Query("q")
	if query == "" {
		c.JSON(http.StatusBadRequest, gin.H{"error": "Query parameter 'q' is required"})
		return
	}

	limitStr := c.DefaultQuery("limit", "20")
	var limit int
	fmt.Sscanf(limitStr, "%d", &limit)

	// Retrieve the indexer from the map
	indexer, ok := s.indexers["YTS"]
	if !ok {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "YTS indexer not found"})
		return
	}

	// Use context from request for cancellation/timeout
	results, err := indexer.Search(c.Request.Context(), query, limit)
	if err != nil {
		s.logger.Error("YTS search failed", zap.Error(err))
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}

	c.JSON(http.StatusOK, gin.H{
		"query":   query,
		"count":   len(results),
		"results": results,
	})
}

// LibGen handler
func (s *Server) handleLibGenSearch(c *gin.Context) {
	query := c.Query("query")
	if query == "" {
		c.JSON(http.StatusBadRequest, gin.H{
			"error":   "query parameter required",
			"example": "/api/v1/books/search?query=golang&limit=10",
		})
		return
	}

	limit := 25
	if l := c.Query("limit"); l != "" {
		fmt.Sscanf(l, "%d", &limit)
		if limit > 100 {
			limit = 100 // Cap at 100
		}
	}

	// Check cache first
	cacheKey := fmt.Sprintf("libgen:%s:%d", query, limit)
	if s.config.CacheEnabled {
		if cached, found := s.cache.Get(cacheKey); found {
			s.logger.Info("LibGen cache hit", zap.String("query", query))
			c.JSON(http.StatusOK, gin.H{
				"query":   query,
				"results": cached,
				"cached":  true,
				"source":  "LibGen",
			})
			return
		}
	}

	// Retrieve LibGen indexer
	libgenIndexer, ok := s.indexers["libgen"]
	if !ok {
		c.JSON(http.StatusNotFound, gin.H{
			"error": "LibGen indexer not available",
		})
		return
	}

	// Search with timeout
	ctx, cancel := context.WithTimeout(c.Request.Context(), 45*time.Second)
	defer cancel()

	start := time.Now()
	results, err := libgenIndexer.Search(ctx, query, limit)
	searchTime := time.Since(start).Seconds() * 1000

	if err != nil {
		s.logger.Error("LibGen search failed",
			zap.String("query", query),
			zap.Error(err))
		c.JSON(http.StatusInternalServerError, gin.H{
			"error": fmt.Sprintf("Search failed: %v", err),
			"query": query,
		})
		return
	}

	// Cache results
	if s.config.CacheEnabled && len(results) > 0 {
		s.cache.Set(cacheKey, results)
	}

	s.logger.Info("LibGen search complete",
		zap.String("query", query),
		zap.Int("results", len(results)),
		zap.Float64("time_ms", searchTime))

	c.JSON(http.StatusOK, gin.H{
		"query":          query,
		"results":        results,
		"count":          len(results),
		"search_time_ms": searchTime,
		"source":         "LibGen",
		"cached":         false,
	})
}

// Annas's Archive handler
func (s *Server) handleAnnasArchiveSearch(c *gin.Context) {
	query := c.Query("query")
	if query == "" {
		c.JSON(http.StatusBadRequest, gin.H{
			"error":   "query parameter required",
			"example": "/api/v1/books/annas/search?query=golang&limit=10",
		})
		return
	}

	limit := 25
	if l := c.Query("limit"); l != "" {
		fmt.Sscanf(l, "%d", &limit)
		if limit > 100 {
			limit = 100
		}
	}

	annasIndexer, ok := s.indexers["annas"]
	if !ok {
		c.JSON(http.StatusNotFound, gin.H{
			"error": "Anna's Archive indexer not available",
		})
		return
	}

	ctx, cancel := context.WithTimeout(c.Request.Context(), 45*time.Second)
	defer cancel()

	start := time.Now()
	results, err := annasIndexer.Search(ctx, query, limit)
	searchTime := time.Since(start).Seconds() * 1000

	if err != nil {
		s.logger.Error("Anna's Archive search failed",
			zap.String("query", query),
			zap.Error(err))
		c.JSON(http.StatusInternalServerError, gin.H{
			"error": fmt.Sprintf("Search failed: %v", err),
		})
		return
	}

	c.JSON(http.StatusOK, gin.H{
		"query":          query,
		"results":        results,
		"count":          len(results),
		"search_time_ms": searchTime,
		"source":         "AnnasArchive",
	})
}

func (s *Server) handleAnnasArchiveInfo(c *gin.Context) {
	md5 := c.Query("md5")
	if md5 == "" {
		c.JSON(http.StatusBadRequest, gin.H{
			"error": "md5 parameter required",
		})
		return
	}

	annasIndexer, ok := s.indexers["annas"]
	if !ok {
		c.JSON(http.StatusNotFound, gin.H{
			"error": "Anna's Archive indexer not available",
		})
		return
	}

	scraper := annasIndexer.(*annasarchive.AnnasArchiveIndexer)

	ctx, cancel := context.WithTimeout(c.Request.Context(), 30*time.Second)
	defer cancel()

	bookURL := fmt.Sprintf("https://annas-archive.li/md5/%s", md5)
	bookInfo, err := scraper.GetBookInfo(ctx, bookURL)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{
			"error": fmt.Sprintf("Failed to get book info: %v", err),
		})
		return
	}

	c.JSON(http.StatusOK, bookInfo)
}

func (s *Server) handleAnnasArchiveDownload(c *gin.Context) {
	md5 := c.Query("md5")
	if md5 == "" {
		c.JSON(http.StatusBadRequest, gin.H{
			"error": "md5 parameter required",
		})
		return
	}

	// Check cache first
	if cached, ok := s.downloadCache.Load(md5); ok {
		cachedDL := cached.(*CachedDownload)
		// Cache valid for 30 minutes
		if time.Since(cachedDL.Created) < 30*time.Minute {
			s.logger.Debug("Download URL cache hit", zap.String("md5", md5))
			c.JSON(http.StatusOK, gin.H{
				"md5":          md5,
				"download_url": cachedDL.URL,
				"cached":       true,
			})
			return
		}
		// Expired, remove from cache
		s.downloadCache.Delete(md5)
	}

	annasIndexer, ok := s.indexers["annas"]
	if !ok {
		c.JSON(http.StatusNotFound, gin.H{
			"error": "Anna's Archive indexer not available",
		})
		return
	}

	scraper := annasIndexer.(*annasarchive.AnnasArchiveIndexer)

	// Get book info to find the slow_download mirror
	ctx, cancel := context.WithTimeout(c.Request.Context(), 90*time.Second)
	defer cancel()

	bookURL := fmt.Sprintf("https://annas-archive.li/md5/%s", md5)
	bookInfo, err := scraper.GetBookInfo(ctx, bookURL)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{
			"error": fmt.Sprintf("Failed to get book info: %v", err),
		})
		return
	}

	if bookInfo.Mirror == "" {
		c.JSON(http.StatusNotFound, gin.H{
			"error": "No download mirror found",
		})
		return
	}
	if bookInfo.Mirror == "" {
		c.JSON(http.StatusNotFound, gin.H{
			"error": "No download mirror found",
		})
		return
	}

	s.logger.Info("Solving DDOS-Guard for Anna's Archive",
		zap.String("md5", md5),
		zap.String("mirror", bookInfo.Mirror))

	// Use the solver to bypass DDOS-Guard and get the real download URL
	finalDownloadURL, err := s.solveAnnasArchiveDownload(ctx, bookInfo.Mirror)
	if err != nil {
		s.logger.Error("Failed to solve DDOS-Guard",
			zap.String("md5", md5),
			zap.Error(err))
		c.JSON(http.StatusInternalServerError, gin.H{
			"error": fmt.Sprintf("Failed to bypass protection: %v", err),
		})
		return
	}

	s.downloadCache.Store(md5, &CachedDownload{
		URL:     finalDownloadURL,
		Created: time.Now(),
		MD5:     md5,
	})

	c.JSON(http.StatusOK, gin.H{
		"md5":          md5,
		"format":       bookInfo.Format,
		"mirror":       bookInfo.Mirror,
		"download_url": finalDownloadURL,
		"title":        bookInfo.Title,
		"cached":       false,
	})
}

func (s *Server) solveAnnasArchiveDownload(ctx context.Context, mirrorURL string) (string, error) {
	if s.solverClient == nil {
		return "", fmt.Errorf("solver not configured")
	}

	start := time.Now()
	s.logger.Info("Starting DDOS-Guard bypass", zap.String("url", mirrorURL))

	// Use FlareSolverr to solve the challenge
	doc, err := s.solverClient.GetDocument(ctx, mirrorURL)
	if err != nil {
		return "", fmt.Errorf("failed to solve challenge: %w", err)
	}

	downloadURL := ""

	// First try: emoji + "Download now" + exclude jdownloader
	doc.Find("a").EachWithBreak(func(i int, sel *goquery.Selection) bool {
		href, exists := sel.Attr("href")
		if !exists || !strings.HasPrefix(href, "https://") {
			return true
		}

		text := strings.TrimSpace(sel.Text())
		lowerText := strings.ToLower(text)
		lowerHref := strings.ToLower(href)

		if strings.Contains(lowerHref, "jdownloader") ||
			strings.Contains(lowerText, "jdownloader") {
			return true
		}

		// Prefer links with emoji or "Download now"
		if (strings.Contains(text, "📚") ||
			strings.Contains(lowerText, "download now")) &&
			(strings.Contains(lowerHref, ".epub") ||
				strings.Contains(lowerHref, ".pdf") ||
				strings.Contains(lowerHref, "/annas-arch-") ||
				strings.Contains(lowerHref, "/d3/") || strings.Contains(lowerHref, "/d4/")) {

			downloadURL = href
			return false // found → stop
		}
		return true
	})

	// Fallback: longest https link that looks file-ish and not jdownloader
	if downloadURL == "" {
		var best string
		maxLen := 0

		doc.Find("a[href^='https://']").Each(func(_ int, sel *goquery.Selection) {
			href, _ := sel.Attr("href")
			lower := strings.ToLower(href)

			if strings.Contains(lower, "jdownloader") {
				return
			}

			isLikelyFile := strings.Contains(lower, ".epub") ||
				strings.Contains(lower, ".pdf") ||
				strings.Contains(lower, ".azw3") ||
				strings.Contains(lower, "/annas-arch-") ||
				(strings.Contains(lower, "/d") && len(lower) > 120) // long CDN paths

			if isLikelyFile && len(href) > maxLen {
				best = href
				maxLen = len(href)
			}
		})
		if best != "" {
			downloadURL = best
		}
	}

	if downloadURL == "" {
		// Debug helper: log part of the page
		htmlStr, _ := doc.Html()
		preview := htmlStr
		if len(preview) > 1800 {
			preview = preview[:1800]
		}
		s.logger.Warn("No good download link extracted",
			zap.String("mirror", mirrorURL),
			zap.String("page_preview", preview))
		return "", fmt.Errorf("could not find real file link after bypass")
	}
	if downloadURL == "" {
		return "", fmt.Errorf("download link not found in solved page")
	}

	s.logger.Info("✅ DDOS-Guard bypassed",
		zap.String("download_url", downloadURL),
		zap.Duration("time", time.Since(start)))

	return downloadURL, nil
}

func (s *Server) handleAnnasArchiveProxyDownload(c *gin.Context) {
	md5 := c.Query("md5")
	if md5 == "" {
		c.JSON(http.StatusBadRequest, gin.H{
			"error": "md5 parameter required",
		})
		return
	}

	annasIndexer, ok := s.indexers["annas"]
	if !ok {
		c.JSON(http.StatusNotFound, gin.H{
			"error": "Anna's Archive indexer not available",
		})
		return
	}

	scraper := annasIndexer.(*annasarchive.AnnasArchiveIndexer)

	ctx, cancel := context.WithTimeout(c.Request.Context(), 120*time.Second)
	defer cancel()

	// Get book info
	bookURL := fmt.Sprintf("https://annas-archive.li/md5/%s", md5)
	bookInfo, err := scraper.GetBookInfo(ctx, bookURL)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{
			"error": fmt.Sprintf("Failed to get book info: %v", err),
		})
		return
	}

	// Solve DDOS-Guard
	finalDownloadURL, err := s.solveAnnasArchiveDownload(ctx, bookInfo.Mirror)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{
			"error": fmt.Sprintf("Failed to bypass protection: %v", err),
		})
		return
	}

	// Proxy the actual file download
	req, err := http.NewRequestWithContext(ctx, "GET", finalDownloadURL, nil)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{
			"error": "failed to create download request",
		})
		return
	}

	req.Header.Set("User-Agent", "Mozilla/5.0 (Windows NT 10.0; Win64; x64) AppleWebKit/537.36")

	client := &http.Client{Timeout: 120 * time.Second}
	resp, err := client.Do(req)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{
			"error": fmt.Sprintf("download failed: %v", err),
		})
		return
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		c.JSON(http.StatusBadGateway, gin.H{
			"error": fmt.Sprintf("upstream returned status %d", resp.StatusCode),
		})
		return
	}

	// Set headers for download
	if contentType := resp.Header.Get("Content-Type"); contentType != "" {
		c.Header("Content-Type", contentType)
	} else {
		// Default based on format
		switch strings.ToLower(bookInfo.Format) {
		case "epub":
			c.Header("Content-Type", "application/epub+zip")
		case "pdf":
			c.Header("Content-Type", "application/pdf")
		case "mobi":
			c.Header("Content-Type", "application/x-mobipocket-ebook")
		default:
			c.Header("Content-Type", "application/octet-stream")
		}
	}

	if contentLength := resp.Header.Get("Content-Length"); contentLength != "" {
		c.Header("Content-Length", contentLength)
	}

	// Generate filename
	filename := fmt.Sprintf("%s.%s", sanitizeFilename(bookInfo.Title), bookInfo.Format)
	c.Header("Content-Disposition", fmt.Sprintf("attachment; filename=%q", filename))

	// Stream the file
	written, err := io.Copy(c.Writer, resp.Body)
	if err != nil {
		s.logger.Error("Failed to stream download", zap.Error(err))
		return
	}

	s.logger.Info("Anna's Archive download completed",
		zap.String("md5", md5),
		zap.Int64("bytes", written))
}

// sanitizeFilename removes invalid characters from filenames
func sanitizeFilename(name string) string {
	// Remove invalid characters
	name = strings.Map(func(r rune) rune {
		switch r {
		case '/', '\\', ':', '*', '?', '"', '<', '>', '|':
			return '_'
		default:
			return r
		}
	}, name)

	// Limit length
	if len(name) > 200 {
		name = name[:200]
	}

	return name
}

// LibGen Mirrors Handler
func (s *Server) handleLibGenMirrors(c *gin.Context) {
	libgenIndexer, ok := s.indexers["libgen"]
	if !ok {
		c.JSON(http.StatusNotFound, gin.H{
			"error": "LibGen indexer not available",
		})
		return
	}

	// Type assertion to access mirrors
	if scraper, ok := libgenIndexer.(*libgen.LibGenScraperIndexer); ok {
		// Test each mirror
		ctx, cancel := context.WithTimeout(c.Request.Context(), 10*time.Second)
		defer cancel()

		type mirrorStatus struct {
			URL     string `json:"url"`
			Type    string `json:"type"`
			Healthy bool   `json:"healthy"`
		}

		mirrors := make([]mirrorStatus, 0)
		for _, mirror := range scraper.GetMirrors() {
			req, err := http.NewRequestWithContext(ctx, "HEAD", mirror.URL, nil)
			healthy := false
			if err == nil {
				client := &http.Client{Timeout: 5 * time.Second}
				resp, err := client.Do(req)
				if err == nil {
					resp.Body.Close()
					healthy = resp.StatusCode == http.StatusOK
				}
			}

			mirrors = append(mirrors, mirrorStatus{
				URL:     mirror.URL,
				Type:    mirror.Type,
				Healthy: healthy,
			})
		}

		c.JSON(http.StatusOK, gin.H{
			"mirrors": mirrors,
			"total":   len(mirrors),
		})
	} else {
		c.JSON(http.StatusInternalServerError, gin.H{
			"error": "Could not access LibGen mirrors",
		})
	}
}

// LibGen Health Check Handler
func (s *Server) handleLibGenHealth(c *gin.Context) {
	libgenIndexer, ok := s.indexers["libgen"]
	if !ok {
		c.JSON(http.StatusNotFound, gin.H{
			"error": "LibGen indexer not available",
		})
		return
	}

	ctx, cancel := context.WithTimeout(c.Request.Context(), 10*time.Second)
	defer cancel()

	start := time.Now()
	err := libgenIndexer.HealthCheck(ctx)
	checkTime := time.Since(start).Milliseconds()

	if err != nil {
		c.JSON(http.StatusServiceUnavailable, gin.H{
			"status":        "unhealthy",
			"error":         err.Error(),
			"check_time_ms": checkTime,
		})
		return
	}

	c.JSON(http.StatusOK, gin.H{
		"status":        "healthy",
		"check_time_ms": checkTime,
		"indexer":       "LibGen",
	})
}

// Fitgirl Handlers
func (s *Server) handleFitGirlLatest(c *gin.Context) {
	limit := 20
	if l := c.Query("limit"); l != "" {
		fmt.Sscanf(l, "%d", &limit)
	}

	if fitgirl, ok := s.indexers["Fitgirl"].(*fitgirl.FitgirlIndexer); ok {
		results, err := fitgirl.GetLatestReleases(c.Request.Context(), limit)
		if err != nil {
			c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
			return
		}
		c.JSON(http.StatusOK, gin.H{"results": results})
	} else {
		c.JSON(http.StatusNotFound, gin.H{"error": "FitGirl indexer not available"})
	}
}

func (s *Server) handleFitGirlPopular(c *gin.Context) {
	limit := 20
	if l := c.Query("limit"); l != "" {
		fmt.Sscanf(l, "%d", &limit)
	}

	if fitgirl, ok := s.indexers["Fitgirl"].(*fitgirl.FitgirlIndexer); ok {
		results, err := fitgirl.GetPopularReleases(c.Request.Context(), limit)
		if err != nil {
			c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
			return
		}
		c.JSON(http.StatusOK, gin.H{"results": results})
	} else {
		c.JSON(http.StatusNotFound, gin.H{"error": "FitGirl indexer not available"})
	}
}

// DODI handlers
func (s *Server) handleDODILatest(c *gin.Context) {
	limit := 20
	if l := c.Query("limit"); l != "" {
		fmt.Sscanf(l, "%d", &limit)
	}

	if dodi, ok := s.indexers["DODI"].(*dodi.DODIIndexer); ok {
		results, err := dodi.GetLatestReleases(c.Request.Context(), limit)
		if err != nil {
			c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
			return
		}
		c.JSON(http.StatusOK, gin.H{"results": results})
	} else {
		c.JSON(http.StatusNotFound, gin.H{"error": "FitGirl indexer not available"})
	}
}

func (s *Server) handleDODIPopular(c *gin.Context) {
	limit := 20
	if l := c.Query("limit"); l != "" {
		fmt.Sscanf(l, "%d", &limit)
	}

	if dodi, ok := s.indexers["DODI"].(*dodi.DODIIndexer); ok {
		results, err := dodi.GetPopularReleases(c.Request.Context(), limit)
		if err != nil {
			c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
			return
		}
		c.JSON(http.StatusOK, gin.H{"results": results})
	} else {
		c.JSON(http.StatusNotFound, gin.H{"error": "DODI indexer not available"})
	}
}

// handles for Books(all in One)
// handles searches for both annas-archive and libgen
func (s *Server) handleBooksSearch(c *gin.Context) {
	query := c.Query("query")
	if query == "" {
		c.JSON(http.StatusBadRequest, gin.H{"error": "query paramerter required"})
		return
	}
	limit := 20
	if l := c.Query("limit"); l != "" {
		fmt.Sscanf(l, "%d", &limit)
	}
	if aggregator, ok := s.indexers["Books"].(*books.BookAggregator); ok {
		results, err := aggregator.Search(c.Request.Context(), query, limit)
		if err != nil {
			c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
			return
		}
		c.JSON(http.StatusOK, gin.H{
			"query":   query,
			"results": results,
			"count":   len(results),
		})
	} else {
		c.JSON(http.StatusNotFound, gin.H{"error": "Repack aggregator not initialized"})
	}
}

// handles for Repacks
// handleRepacksSearch handles searching across all game repackers (FitGirl & DODI)
func (s *Server) handleRepacksSearch(c *gin.Context) {
	query := c.Query("query")
	if query == "" {
		c.JSON(http.StatusBadRequest, gin.H{"error": "query parameter required"})
		return
	}

	limit := 20
	if l := c.Query("limit"); l != "" {
		fmt.Sscanf(l, "%d", &limit)
	}

	// Retrieve the aggregator from the map
	if aggregator, ok := s.indexers["Repacks"].(*games.RepackAggregator); ok {
		results, err := aggregator.Search(c.Request.Context(), query, limit)
		if err != nil {
			c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
			return
		}
		c.JSON(http.StatusOK, gin.H{
			"query":   query,
			"results": results,
			"count":   len(results),
		})
	} else {
		c.JSON(http.StatusNotFound, gin.H{"error": "Repack aggregator not initialized"})
	}
}

// handleRepacksLatest handles fetching the combined latest releases
func (s *Server) handleRepacksLatest(c *gin.Context) {
	limit := 30
	if l := c.Query("limit"); l != "" {
		fmt.Sscanf(l, "%d", &limit)
	}

	if aggregator, ok := s.indexers["Repacks"].(*games.RepackAggregator); ok {
		results, err := aggregator.GetLatestReleases(c.Request.Context(), limit)
		if err != nil {
			c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
			return
		}
		c.JSON(http.StatusOK, gin.H{
			"results": results,
			"count":   len(results),
		})
	} else {
		c.JSON(http.StatusNotFound, gin.H{"error": "Repack aggregator not initialized"})
	}
}

// Optimized concurrent search with worker pool
func (s *Server) searchIndexers(ctx context.Context, query string, limit int, category string) []models.TorrentResult {
	type indexerResult struct {
		name    string
		results []models.TorrentResult
		err     error
	}

	resultsChan := make(chan indexerResult, len(s.indexers))
	var wg sync.WaitGroup

	// Launch searches concurrently
	for name, idx := range s.indexers {
		// Filter by category
		if category != "" && !idx.SupportsCategory(category) {
			continue
		}

		wg.Add(1)
		go func(indexerName string, indexer indexers.Indexer) {
			defer wg.Done()

			// Use worker pool to limit concurrency
			s.workerPool <- struct{}{}
			defer func() { <-s.workerPool }()

			// Timeout per indexer
			ctx, cancel := context.WithTimeout(ctx, 85*time.Second)
			defer cancel()

			start := time.Now()
			results, err := indexer.Search(ctx, query, limit)

			s.logger.Debug("Indexer search",
				zap.String("indexer", indexerName),
				zap.Duration("duration", time.Since(start)),
				zap.Int("results", len(results)),
				zap.Error(err))

			resultsChan <- indexerResult{
				name:    indexerName,
				results: results,
				err:     err,
			}
		}(name, idx)
	}

	// Close channel when all done
	go func() {
		wg.Wait()
		close(resultsChan)
	}()

	// Collect results
	allResults := make([]models.TorrentResult, 0, limit)
	for res := range resultsChan {
		if res.err != nil {
			s.logger.Warn("Indexer failed",
				zap.String("indexer", res.name),
				zap.Error(res.err))
			continue
		}
		allResults = append(allResults, res.results...)
	}

	// Sort by seeders
	sortByQuality(allResults)

	// Limit results
	if len(allResults) > limit {
		allResults = allResults[:limit]
	}

	return allResults
}

// Handlers for LibGen
func (s *Server) handleDirectDownload(c *gin.Context) {
	md5 := c.Query("md5")
	if md5 == "" {
		c.JSON(http.StatusBadRequest, gin.H{
			"error": "md5 parameter required",
		})
		return
	}

	// Get LibGen indexer
	libgenIndexer, ok := s.indexers["libgen"]
	if !ok {
		c.JSON(http.StatusNotFound, gin.H{
			"error": "LibGen indexer not available",
		})
		return
	}

	// Type assertion
	scraper, ok := libgenIndexer.(*libgen.LibGenScraperIndexer)
	if !ok {
		c.JSON(http.StatusInternalServerError, gin.H{
			"error": "LibGen indexer type mismatch",
		})
		return
	}

	ctx, cancel := context.WithTimeout(c.Request.Context(), 60*time.Second)
	defer cancel()

	// Find working mirror
	mirrors := scraper.GetMirrors()
	if len(mirrors) == 0 {
		c.JSON(http.StatusServiceUnavailable, gin.H{
			"error": "No LibGen mirrors available",
		})
		return
	}

	// Get cached or fetch download URL
	downloadURL, err := scraper.GetCachedDownloadURL(ctx, mirrors[0].URL, md5)
	if err != nil {
		s.logger.Error("Failed to get download URL",
			zap.String("md5", md5),
			zap.Error(err))
		c.JSON(http.StatusInternalServerError, gin.H{
			"error": fmt.Sprintf("Failed to get download URL: %v", err),
		})
		return
	}

	c.JSON(http.StatusOK, gin.H{
		"md5":          md5,
		"download_url": downloadURL,
		"cached":       true,
	})
}

// Proxy download handler (optional - for privacy/CORS)
func (s *Server) handleProxyDownload(c *gin.Context) {
	url := c.Query("url")
	if url == "" {
		c.JSON(http.StatusBadRequest, gin.H{
			"error": "url parameter required",
		})
		return
	}

	// Validate URL
	if !strings.HasPrefix(url, "http://") && !strings.HasPrefix(url, "https://") {
		c.JSON(http.StatusBadRequest, gin.H{
			"error": "invalid URL",
		})
		return
	}

	ctx, cancel := context.WithTimeout(c.Request.Context(), 120*time.Second)
	defer cancel()

	// Create request
	req, err := http.NewRequestWithContext(ctx, "GET", url, nil)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{
			"error": "failed to create request",
		})
		return
	}

	req.Header.Set("User-Agent", "Mozilla/5.0 (Windows NT 10.0; Win64; x64) AppleWebKit/537.36")

	// Execute request
	client := &http.Client{
		Timeout: 120 * time.Second,
	}
	resp, err := client.Do(req)
	if err != nil {
		s.logger.Error("Proxy download failed",
			zap.String("url", url),
			zap.Error(err))
		c.JSON(http.StatusInternalServerError, gin.H{
			"error": fmt.Sprintf("download failed: %v", err),
		})
		return
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		c.JSON(http.StatusBadGateway, gin.H{
			"error": fmt.Sprintf("upstream returned status %d", resp.StatusCode),
		})
		return
	}

	// Set headers
	c.Header("Content-Type", resp.Header.Get("Content-Type"))
	if contentLength := resp.Header.Get("Content-Length"); contentLength != "" {
		c.Header("Content-Length", contentLength)
	}
	if contentDisposition := resp.Header.Get("Content-Disposition"); contentDisposition != "" {
		c.Header("Content-Disposition", contentDisposition)
	} else {
		// Generate filename from URL
		filename := filepath.Base(url)
		c.Header("Content-Disposition", fmt.Sprintf("attachment; filename=%q", filename))
	}

	// Stream the response
	written, err := io.Copy(c.Writer, resp.Body)
	if err != nil {
		s.logger.Error("Failed to stream download",
			zap.String("url", url),
			zap.Int64("bytes_written", written),
			zap.Error(err))
		return
	}

	s.logger.Info("Proxy download completed",
		zap.String("url", url),
		zap.Int64("bytes", written))
}

func (s *Server) handleSearch(c *gin.Context) {
	query := c.Query("query")
	if query == "" {
		c.JSON(http.StatusBadRequest, gin.H{"error": "query parameter required"})
		return
	}

	limit := 50
	if l := c.Query("limit"); l != "" {
		fmt.Sscanf(l, "%d", &limit)
	}

	category := c.Query("category")

	// Check cache
	cacheKey := fmt.Sprintf("search:%s:%d:%s", query, limit, category)
	if s.config.CacheEnabled {
		if cached, found := s.cache.Get(cacheKey); found {
			response := cached.(*models.SearchResponse)
			response.CacheHit = true
			c.JSON(http.StatusOK, response)
			return
		}
	}

	// Search with generous timeout
	ctx, cancel := context.WithTimeout(c.Request.Context(), 90*time.Second)
	defer cancel()

	start := time.Now()
	results := s.searchIndexers(ctx, query, limit, category)
	searchTime := time.Since(start).Seconds() * 1000

	response := &models.SearchResponse{
		Query:      query,
		Results:    results,
		TotalFound: len(results),
		SearchTime: searchTime,
		Sources:    s.getEnabledSources(),
		CacheHit:   false,
		Category:   category,
	}

	// Cache results
	if s.config.CacheEnabled {
		s.cache.Set(cacheKey, response)
	}

	s.logger.Info("Search complete",
		zap.String("query", query),
		zap.Int("results", len(results)),
		zap.Float64("time_ms", searchTime))

	c.JSON(http.StatusOK, response)
}

func (s *Server) getEnabledSources() []string {
	sources := make([]string, 0, len(s.indexers))
	for name := range s.indexers {
		sources = append(sources, name)
	}
	return sources
}

func sortByQuality(results []models.TorrentResult) {
	// Quick sort would be better, but this is fine for now
	for i := 0; i < len(results)-1; i++ {
		for j := i + 1; j < len(results); j++ {
			if results[i].Seeders < results[j].Seeders {
				results[i], results[j] = results[j], results[i]
			}
		}
	}
}

func (s *Server) handleIndexers(c *gin.Context) {
	indexerList := make([]models.IndexerInfo, 0, len(s.indexers))

	for name, idx := range s.indexers {
		ctx, cancel := context.WithTimeout(c.Request.Context(), 3*time.Second)
		healthy := idx.HealthCheck(ctx) == nil
		cancel()

		indexerList = append(indexerList, models.IndexerInfo{
			Name:    name,
			Enabled: true,
			Healthy: healthy,
		})
	}

	c.JSON(http.StatusOK, gin.H{
		"indexers": indexerList,
		"total":    len(indexerList),
	})
}

func (s *Server) handleHealth(c *gin.Context) {
	healthyCount := 0
	indexerHealth := make(map[string]bool)

	var wg sync.WaitGroup
	var mu sync.Mutex

	for name, idx := range s.indexers {
		wg.Add(1)
		go func(n string, i indexers.Indexer) {
			defer wg.Done()
			ctx, cancel := context.WithTimeout(c.Request.Context(), 3*time.Second)
			defer cancel()

			healthy := i.HealthCheck(ctx) == nil

			mu.Lock()
			indexerHealth[n] = healthy
			if healthy {
				healthyCount++
			}
			mu.Unlock()
		}(name, idx)
	}

	wg.Wait()

	status := "healthy"
	if healthyCount < len(s.indexers)/2 {
		status = "degraded"
	}
	if healthyCount == 0 {
		status = "unhealthy"
	}

	c.JSON(http.StatusOK, models.HealthStatus{
		Status:        status,
		HealthyCount:  healthyCount,
		TotalIndexers: len(s.indexers),
		CacheEnabled:  s.config.CacheEnabled,
		SolverEnabled: s.config.SolverEnabled,
		Indexers:      indexerHealth,
		Uptime:        time.Since(startTime).String(),
	})
}

func (s *Server) handleStats(c *gin.Context) {
	cacheSize := 0
	if s.config.CacheEnabled {
		cacheSize = s.cache.Len()
	}

	var m runtime.MemStats
	runtime.ReadMemStats(&m)

	c.JSON(http.StatusOK, gin.H{
		"uptime":        time.Since(startTime).String(),
		"version":       "1.0.0",
		"cache_size":    cacheSize,
		"cache_enabled": s.config.CacheEnabled,
		"indexers":      len(s.indexers),
		"memory_mb":     m.Alloc / 1024 / 1024,
		"goroutines":    runtime.NumGoroutine(),
	})
}

func getEnv(key, defaultValue string) string {
	if value := os.Getenv(key); value != "" {
		return value
	}
	return defaultValue
}
