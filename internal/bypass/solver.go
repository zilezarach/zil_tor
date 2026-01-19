package bypass

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/chromedp/cdproto/network"
	"github.com/chromedp/cdproto/page"
	"github.com/chromedp/chromedp"
	"go.uber.org/zap"
)

// Solver handles Cloudflare bypass using headless browsers
type Solver struct {
	logger       *zap.Logger
	pool         *BrowserPool
	timeout      time.Duration
	maxInstances int
	BrowserPath  string
}

// Config for the solver
type Config struct {
	Timeout      time.Duration
	MaxInstances int
	Headless     bool
	BrowserPath  string
}

// Response from the solver
type Response struct {
	HTML       string
	Cookies    []*Cookie
	UserAgent  string
	StatusCode int
	Error      error
}

// Cookie represents an HTTP cookie
type Cookie struct {
	Name     string
	Value    string
	Domain   string
	Path     string
	Expires  time.Time
	HttpOnly bool
	Secure   bool
}

// NewSolver creates a new solver instance
func NewSolver(cfg *Config, logger *zap.Logger) *Solver {
	return &Solver{
		logger:       logger,
		pool:         NewBrowserPool(cfg.MaxInstances, cfg.Headless, logger, cfg.BrowserPath),
		timeout:      cfg.Timeout,
		maxInstances: cfg.MaxInstances,
		BrowserPath:  cfg.BrowserPath,
	}
}

// Solve bypasses Cloudflare protection and returns page content
func (s *Solver) Solve(ctx context.Context, url string) (*Response, error) {
	browser, err := s.pool.Get(ctx)
	if err != nil {
		return nil, fmt.Errorf("failed to get browser: %w", err)
	}
	defer s.pool.Put(browser)

	// Aggressive timeout
	ctx, cancel := context.WithTimeout(browser.ctx, s.timeout)
	defer cancel()

	s.logger.Debug("Starting bypass", zap.String("url", url))

	var html string
	var cookies []*network.Cookie
	var ua string

	// Minimal stealth - just enough to pass
	stealthScript := `
		Object.defineProperty(navigator, 'webdriver', {get: () => undefined});
		Object.defineProperty(navigator, 'plugins', {get: () => [1, 2, 3, 4, 5]});
		Object.defineProperty(navigator, 'languages', {get: () => ['en-US', 'en']});
		window.chrome = {runtime: {}};
	`

	// Fast detection function
	detectSuccess := `
		(() => {
			const html = document.documentElement.innerHTML.toLowerCase();
			const inChallenge = html.includes('just a moment') || 
			                   html.includes('checking your browser') ||
			                   html.includes('verify you are human');
			const hasContent = html.length > 50000 || document.querySelector('.table-list');
			return !inChallenge && hasContent;
		})()
	`

	startTime := time.Now()
	maxWait := 25 * time.Second
	checkInterval := 1 * time.Second

	err = chromedp.Run(ctx,
		network.Enable(),
		page.Enable(),

		// Minimal headers
		network.SetExtraHTTPHeaders(map[string]interface{}{
			"Accept":          "text/html,application/xhtml+xml,application/xml;q=0.9,*/*;q=0.8",
			"Accept-Language": "en-US,en;q=0.9",
		}),

		chromedp.Navigate(url),
		chromedp.Sleep(500*time.Millisecond),
		chromedp.Evaluate(stealthScript, nil),
		chromedp.WaitReady("body", chromedp.ByQuery),

		// Fast polling loop
		chromedp.ActionFunc(func(ctx context.Context) error {
			for time.Since(startTime) < maxWait {
				var success bool
				if err := chromedp.Evaluate(detectSuccess, &success).Do(ctx); err == nil && success {
					s.logger.Debug("✅ Bypass successful", zap.Duration("time", time.Since(startTime)))
					return nil
				}

				if time.Since(startTime) < maxWait-checkInterval {
					time.Sleep(checkInterval)
				} else {
					break
				}
			}

			// Timeout - check if we have anything useful
			var htmlLen int
			chromedp.Evaluate(`document.documentElement.innerHTML.length`, &htmlLen).Do(ctx)
			if htmlLen < 10000 {
				return fmt.Errorf("bypass timeout - no content")
			}

			s.logger.Warn("Bypass timeout but got content", zap.Int("html_len", htmlLen))
			return nil
		}),

		chromedp.Sleep(1*time.Second), // Final stabilization
		chromedp.OuterHTML("html", &html),

		chromedp.ActionFunc(func(ctx context.Context) error {
			var err error
			cookies, err = network.GetCookies().Do(ctx)
			if err != nil {
				s.logger.Warn("Cookie fetch failed", zap.Error(err))
				return nil
			}
			return nil
		}),

		chromedp.Evaluate(`navigator.userAgent`, &ua),
	)
	if err != nil {
		s.logger.Error("Solver failed", zap.Error(err))
		return nil, fmt.Errorf("chromedp failed: %w", err)
	}

	// Quick validation
	lowerHTML := strings.ToLower(html)
	if strings.Contains(lowerHTML, "just a moment") || strings.Contains(lowerHTML, "checking your browser") {
		return nil, fmt.Errorf("still blocked by cloudflare")
	}

	respCookies := make([]*Cookie, 0, len(cookies))
	for _, c := range cookies {
		respCookies = append(respCookies, &Cookie{
			Name:     c.Name,
			Value:    c.Value,
			Domain:   c.Domain,
			Path:     c.Path,
			Expires:  time.Unix(int64(c.Expires), 0),
			HttpOnly: c.HTTPOnly,
			Secure:   c.Secure,
		})
	}

	s.logger.Info("✅ Solved",
		zap.Duration("time", time.Since(startTime)),
		zap.Int("html_len", len(html)),
		zap.Int("cookies", len(respCookies)))

	return &Response{
		HTML:       html,
		Cookies:    respCookies,
		UserAgent:  ua,
		StatusCode: 200,
	}, nil
}

// SolveWithRetry attempts to solve with retries
func (s *Solver) SolveWithRetry(ctx context.Context, url string, maxRetries int) (*Response, error) {
	var lastErr error

	for attempt := 1; attempt <= maxRetries; attempt++ {
		resp, err := s.Solve(ctx, url)
		if err != nil {
			lastErr = err
			if attempt < maxRetries {
				s.logger.Debug("Retry", zap.Int("attempt", attempt), zap.Error(err))
				time.Sleep(time.Duration(attempt) * time.Second)
			}
			continue
		}
		return resp, nil
	}

	return nil, fmt.Errorf("failed after %d attempts: %w", maxRetries, lastErr)
}

// Close cleans up resources
func (s *Solver) Close() error {
	return s.pool.Close()
}
