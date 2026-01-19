package bypass

import (
	"context"
	"fmt"
	"net/http"
	"strings"
	"time"

	"github.com/PuerkitoBio/goquery"
)

// Client wraps the solver with HTTP client interface
type Client struct {
	solver     *Solver
	httpClient *http.Client
	useSolver  bool
}

// NewClient creates a new solver client
func NewClient(solver *Solver) *Client {
	return &Client{
		solver: solver,
		httpClient: &http.Client{
			Timeout: 30 * time.Second,
		},
		useSolver: true,
	}
}

// Get performs a GET request with optional Cloudflare bypass
func (c *Client) Get(ctx context.Context, url string) (*http.Response, error) {
	// Try normal request first
	req, err := http.NewRequestWithContext(ctx, "GET", url, nil)
	if err != nil {
		return nil, err
	}

	req.Header.Set("User-Agent", "Mozilla/5.0 (Windows NT 10.0; Win64; x64) AppleWebKit/537.36")

	resp, err := c.httpClient.Do(req)
	if err != nil || (resp.StatusCode == 403 || resp.StatusCode == 503) {
		// Likely Cloudflare protection, use solver
		if c.useSolver && c.solver != nil {
			return c.solveAndGet(ctx, url)
		}
		return resp, err
	}

	return resp, nil
}

// solveAndGet uses the solver to bypass Cloudflare
func (c *Client) solveAndGet(ctx context.Context, url string) (*http.Response, error) {
	solverResp, err := c.solver.Solve(ctx, url)
	if err != nil {
		return nil, err
	}

	// Create a mock response with solved content
	// In production, you'd want to make actual HTTP request with cookies
	resp := &http.Response{
		StatusCode: solverResp.StatusCode,
		Body:       http.NoBody,
		Header:     make(http.Header),
	}

	// Add cookies to header
	for _, cookie := range solverResp.Cookies {
		resp.Header.Add("Set-Cookie", fmt.Sprintf("%s=%s", cookie.Name, cookie.Value))
	}

	return resp, nil
}

// GetDocument fetches and parses HTML document
func (c *Client) GetDocument(ctx context.Context, url string) (*goquery.Document, error) {
	if c.useSolver && c.solver != nil {
		// Use solver for Cloudflare-protected sites
		solverResp, err := c.solver.Solve(ctx, url)
		if err != nil {
			return nil, err
		}
		return goquery.NewDocumentFromReader(
			strings.NewReader(solverResp.HTML),
		)

	}

	// Normal request
	resp, err := c.Get(ctx, url)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	return goquery.NewDocumentFromReader(resp.Body)
}

// SetUseSolver enables or disables solver
func (c *Client) SetUseSolver(use bool) {
	c.useSolver = use
}
