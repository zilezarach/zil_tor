package bypass

import (
	"context"
	"sync"

	"github.com/chromedp/chromedp"
	"go.uber.org/zap"
)

type Browser struct {
	ctx       context.Context
	cancel    context.CancelFunc
	temporary bool
}

type BrowserPool struct {
	pool        chan *Browser
	headless    bool
	logger      *zap.Logger
	mu          sync.Mutex
	closed      bool
	browserPath string
}

// NewBrowserPool initializes a fixed-size browser pool
func NewBrowserPool(size int, headless bool, logger *zap.Logger, browserPath string) *BrowserPool {
	p := &BrowserPool{
		pool:        make(chan *Browser, size),
		headless:    headless,
		logger:      logger,
		browserPath: browserPath,
	}

	for i := 0; i < size; i++ {
		p.pool <- p.newBrowser(false)
	}

	logger.Info("Browser pool initialized", zap.Int("size", size))
	return p
}

// newBrowser creates a single Chrome instance with anti-detection measures
func (p *BrowserPool) newBrowser(temp bool) *Browser {
	opts := append(chromedp.DefaultExecAllocatorOptions[:],
		// Basic flags
		chromedp.Flag("headless", p.headless),
		chromedp.Flag("disable-gpu", true),
		chromedp.Flag("no-sandbox", true),
		chromedp.Flag("disable-dev-shm-usage", true),

		// Anti-detection flags
		chromedp.Flag("disable-blink-features", "AutomationControlled"),
		chromedp.Flag("disable-infobars", true),
		chromedp.Flag("disable-automation", true),
		chromedp.Flag("disable-extensions", false),
		chromedp.Flag("disable-plugins-discovery", true),
		chromedp.Flag("disable-site-isolation-trials", true),
		chromedp.Flag("disable-setuid-sandbox", true),

		// Realistic browser settings
		chromedp.WindowSize(1920, 1080),
		chromedp.UserAgent(
			"Mozilla/5.0 (Windows NT 10.0; Win64; x64) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/122.0.0.0 Safari/537.36",
		),

		// Additional realism
		chromedp.Flag("disable-background-networking", false),
		chromedp.Flag("enable-features", "NetworkService,NetworkServiceInProcess"),
		chromedp.Flag("disable-background-timer-throttling", false),
		chromedp.Flag("disable-backgrounding-occluded-windows", false),
		chromedp.Flag("disable-breakpad", false),
		chromedp.Flag("disable-component-extensions-with-background-pages", false),
		chromedp.Flag("disable-features", "TranslateUI,BlinkGenPropertyTrees"),
		chromedp.Flag("disable-ipc-flooding-protection", false),
		chromedp.Flag("disable-renderer-backgrounding", false),
		chromedp.Flag("enable-automation", false),
		chromedp.Flag("password-store", "basic"),
		chromedp.Flag("use-mock-keychain", false),
	)

	// Use the explicit browser path if set
	if p.browserPath != "" {
		opts = append(opts, chromedp.ExecPath(p.browserPath))
	}

	allocCtx, allocCancel := chromedp.NewExecAllocator(context.Background(), opts...)
	ctx, ctxCancel := chromedp.NewContext(allocCtx,
		chromedp.WithLogf(func(format string, args ...interface{}) {
			// Suppress chromedp logs unless debug mode
		}),
	)

	cancel := func() {
		ctxCancel()
		allocCancel()
	}

	return &Browser{
		ctx:       ctx,
		cancel:    cancel,
		temporary: temp,
	}
}

// Get blocks until a browser is available or ctx is canceled
func (p *BrowserPool) Get(ctx context.Context) (*Browser, error) {
	select {
	case b := <-p.pool:
		return b, nil
	case <-ctx.Done():
		return nil, ctx.Err()
	}
}

// Put returns browser to pool or destroys temp ones
func (p *BrowserPool) Put(b *Browser) {
	if b == nil {
		return
	}
	if b.temporary {
		b.cancel()
		return
	}
	select {
	case p.pool <- b:
	default:
		p.logger.Warn("Browser pool overflow, destroying instance")
		b.cancel()
	}
}

// Close shuts down the entire pool
func (p *BrowserPool) Close() error {
	p.mu.Lock()
	defer p.mu.Unlock()

	if p.closed {
		return nil
	}

	p.closed = true
	close(p.pool)

	for b := range p.pool {
		b.cancel()
	}

	p.logger.Info("Browser pool closed")
	return nil
}
