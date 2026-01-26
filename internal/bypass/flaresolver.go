package bypass

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"time"

	"github.com/PuerkitoBio/goquery"
	"go.uber.org/zap"
)

// FlareSolverrClient communicates with FlareSolverr service
type FlareSolverrClient struct {
	endpoint   string
	httpClient *http.Client
	logger     *zap.Logger
}

type FlareSession struct {
	Cookies   []*Cookie
	UserAgent string
}

type FlareSolverrRequest struct {
	Cmd        string `json:"cmd"`
	URL        string `json:"url"`
	MaxTimeout int    `json:"maxTimeout"`
}

type FlareSolverrResponse struct {
	Status   string `json:"status"`
	Message  string `json:"message"`
	Solution struct {
		URL     string `json:"url"`
		Status  int    `json:"status"`
		Cookies []struct {
			Name     string  `json:"name"`
			Value    string  `json:"value"`
			Domain   string  `json:"domain"`
			Path     string  `json:"path"`
			Expires  float64 `json:"expires"`
			HTTPOnly bool    `json:"httpOnly"`
			Secure   bool    `json:"secure"`
		} `json:"cookies"`
		UserAgent string `json:"userAgent"`
		Response  string `json:"response"`
	} `json:"solution"`
}

func NewFlareSolverrClient(endpoint string, logger *zap.Logger) *FlareSolverrClient {
	return &FlareSolverrClient{
		endpoint: endpoint,
		httpClient: &http.Client{
			Timeout: 60 * time.Second,
			Transport: &http.Transport{
				MaxIdleConns:        100,
				MaxIdleConnsPerHost: 100,
				IdleConnTimeout:     90 * time.Second,
			},
		},
		logger: logger,
	}
}

func (f *FlareSolverrClient) Solve(ctx context.Context, url string) (*Response, error) {
	reqBody := FlareSolverrRequest{
		Cmd:        "request.get",
		URL:        url,
		MaxTimeout: 60000,
	}

	jsonData, _ := json.Marshal(reqBody)
	req, err := http.NewRequestWithContext(ctx, "POST", f.endpoint+"/v1", bytes.NewBuffer(jsonData))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", "application/json")

	resp, err := f.httpClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("flaresolverr request failed: %w", err)
	}
	defer resp.Body.Close()

	var fsResp FlareSolverrResponse
	if err := json.NewDecoder(resp.Body).Decode(&fsResp); err != nil {
		return nil, fmt.Errorf("decode failed: %w", err)
	}

	if fsResp.Status != "ok" {
		return nil, fmt.Errorf("flaresolverr error: %s", fsResp.Message)
	}

	cookies := make([]*Cookie, 0, len(fsResp.Solution.Cookies))
	for _, c := range fsResp.Solution.Cookies {
		cookies = append(cookies, &Cookie{
			Name:     c.Name,
			Value:    c.Value,
			Domain:   c.Domain,
			Path:     c.Path,
			Expires:  time.Unix(int64(c.Expires), 0),
			HttpOnly: c.HTTPOnly,
			Secure:   c.Secure,
		})
	}

	return &Response{
		HTML:       fsResp.Solution.Response,
		Cookies:    cookies,
		UserAgent:  fsResp.Solution.UserAgent,
		StatusCode: fsResp.Solution.Status,
	}, nil
}

// SessionCache with fast concurrent access
type SessionCache struct {
	sessions map[string]*CachedSession
	mu       sync.RWMutex
	ttl      time.Duration
}

type CachedSession struct {
	Cookies   []*Cookie
	UserAgent string
	Created   time.Time
}

func NewSessionCache(ttl time.Duration) *SessionCache {
	sc := &SessionCache{
		sessions: make(map[string]*CachedSession),
		ttl:      ttl,
	}

	// Cleanup every 10 minutes
	go func() {
		ticker := time.NewTicker(10 * time.Minute)
		defer ticker.Stop()
		for range ticker.C {
			sc.cleanup()
		}
	}()

	return sc
}

func (sc *SessionCache) cleanup() {
	sc.mu.Lock()
	defer sc.mu.Unlock()

	now := time.Now()
	for key, session := range sc.sessions {
		if now.Sub(session.Created) > sc.ttl {
			delete(sc.sessions, key)
		}
	}
}

func (sc *SessionCache) Get(urlStr string) (*CachedSession, bool) {
	sc.mu.RLock()
	defer sc.mu.RUnlock()

	u, _ := url.Parse(urlStr)
	key := u.Host

	session, exists := sc.sessions[key]
	if !exists {
		return nil, false
	}

	if time.Since(session.Created) > sc.ttl {
		return nil, false
	}

	return session, true
}

func (sc *SessionCache) Set(urlStr string, cookies []*Cookie, userAgent string) {
	sc.mu.Lock()
	defer sc.mu.Unlock()

	u, _ := url.Parse(urlStr)
	sc.sessions[u.Host] = &CachedSession{
		Cookies:   cookies,
		UserAgent: userAgent,
		Created:   time.Now(),
	}
}

// HybridClient with aggressive optimizations
type HybridClient struct {
	customClient   *Client
	flareSolverr   *FlareSolverrClient
	httpClient     *http.Client
	sessionCache   *SessionCache
	useCustomFirst bool
	logger         *zap.Logger

	// Request deduplication
	inFlight   map[string]*flightGroup
	flightLock sync.Mutex
}

type flightGroup struct {
	wg  sync.WaitGroup
	ch  chan result
	key string
}

type result struct {
	resp *http.Response
	err  error
}

func NewHybridClient(customSolver *Solver, flareEndpoint string, logger *zap.Logger) *HybridClient {
	var customClient *Client
	if customSolver != nil {
		customClient = NewClient(customSolver)
	}

	var flareSolverr *FlareSolverrClient
	if flareEndpoint != "" {
		flareSolverr = NewFlareSolverrClient(flareEndpoint, logger)
	}

	return &HybridClient{
		customClient: customClient,
		flareSolverr: flareSolverr,
		httpClient: &http.Client{
			Timeout: 15 * time.Second,
			Transport: &http.Transport{
				MaxIdleConns:        200,
				MaxIdleConnsPerHost: 100,
				MaxConnsPerHost:     100,
				IdleConnTimeout:     90 * time.Second,
				DisableKeepAlives:   false,
			},
		},
		sessionCache:   NewSessionCache(30 * time.Minute),
		useCustomFirst: customSolver != nil,
		logger:         logger,
		inFlight:       make(map[string]*flightGroup),
	}
}

// Get with request deduplication for concurrent requests to same URL
func (h *HybridClient) Get(ctx context.Context, urlStr string) (*http.Response, error) {
	// Check if this exact request is already in flight
	h.flightLock.Lock()
	if fg, exists := h.inFlight[urlStr]; exists {
		h.flightLock.Unlock()
		h.logger.Debug("Request already in flight, waiting...", zap.String("url", urlStr))
		res := <-fg.ch
		return res.resp, res.err
	}

	// Start new flight group
	fg := &flightGroup{
		ch:  make(chan result, 10),
		key: urlStr,
	}
	fg.wg.Add(1)
	h.inFlight[urlStr] = fg
	h.flightLock.Unlock()

	// Execute request
	go func() {
		defer fg.wg.Done()

		resp, err := h.doGet(ctx, urlStr)
		res := result{resp: resp, err: err}

		// Broadcast result to all waiters
		for i := 0; i < cap(fg.ch); i++ {
			select {
			case fg.ch <- res:
			default:
				break
			}
		}

		// Cleanup
		h.flightLock.Lock()
		delete(h.inFlight, urlStr)
		h.flightLock.Unlock()
		close(fg.ch)
	}()

	// Wait for result
	res := <-fg.ch
	return res.resp, res.err
}

func (h *HybridClient) doGet(ctx context.Context, urlStr string) (*http.Response, error) {
	// Fast path: Check session cache
	if session, exists := h.sessionCache.Get(urlStr); exists {
		resp, err := h.tryWithSession(ctx, urlStr, session)
		if err == nil {
			h.logger.Debug("✅ Session cache hit", zap.String("url", urlStr))
			return resp, nil
		}
	}

	// Try direct request
	req, _ := http.NewRequestWithContext(ctx, "GET", urlStr, nil)
	req.Header.Set("User-Agent", "Mozilla/5.0 (Windows NT 10.0; Win64; x64) AppleWebKit/537.36")
	req.Header.Set("Accept", "text/html,application/xhtml+xml,application/xml;q=0.9,*/*;q=0.8")

	resp, err := h.httpClient.Do(req)
	if err == nil && resp.StatusCode == 200 {
		bodyBytes, _ := io.ReadAll(resp.Body)
		resp.Body.Close()

		if !isCloudflareChallenge(string(bodyBytes)) {
			resp.Body = io.NopCloser(bytes.NewReader(bodyBytes))
			return resp, nil
		}
	}

	// Need to solve - prefer FlareSolverr for speed
	h.logger.Debug("Cloudflare detected, solving...", zap.String("url", urlStr))

	// Use FlareSolverr directly (it's faster and more reliable)
	if h.flareSolverr != nil {
		solverResp, err := h.flareSolverr.Solve(ctx, urlStr)
		if err == nil {
			h.sessionCache.Set(urlStr, solverResp.Cookies, solverResp.UserAgent)
			return createHTTPResponse(solverResp), nil
		}
		h.logger.Warn("FlareSolverr failed", zap.Error(err))
	}

	// Fallback to custom solver
	if h.customClient != nil {
		resp, err := h.customClient.Get(ctx, urlStr)
		if err == nil {
			return resp, nil
		}
	}

	return nil, fmt.Errorf("all solvers failed")
}

func (h *HybridClient) tryWithSession(ctx context.Context, urlStr string, session *CachedSession) (*http.Response, error) {
	req, _ := http.NewRequestWithContext(ctx, "GET", urlStr, nil)
	req.Header.Set("User-Agent", session.UserAgent)

	for _, cookie := range session.Cookies {
		req.AddCookie(&http.Cookie{
			Name:  cookie.Name,
			Value: cookie.Value,
		})
	}

	resp, err := h.httpClient.Do(req)
	if err != nil {
		return nil, err
	}

	if resp.StatusCode != 200 {
		resp.Body.Close()
		return nil, fmt.Errorf("status %d", resp.StatusCode)
	}

	bodyBytes, err := io.ReadAll(resp.Body)
	resp.Body.Close()
	if err != nil {
		return nil, err
	}

	if isCloudflareChallenge(string(bodyBytes)) {
		return nil, fmt.Errorf("session expired")
	}

	resp.Body = io.NopCloser(bytes.NewReader(bodyBytes))
	return resp, nil
}

func (h *HybridClient) GetDocument(ctx context.Context, url string) (*goquery.Document, error) {
	resp, err := h.Get(ctx, url)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	return goquery.NewDocumentFromReader(resp.Body)
}

func (h *HybridClient) SetUseSolver(use bool) {
	h.useCustomFirst = use
}

func (h *HybridClient) GetWithFlareSolverr(ctx context.Context, url string) (*http.Response, error) {
	if h.flareSolverr == nil {
		return nil, fmt.Errorf("flaresolverr not configured")
	}

	solverResp, err := h.flareSolverr.Solve(ctx, url)
	if err != nil {
		return nil, err
	}

	h.sessionCache.Set(url, solverResp.Cookies, solverResp.UserAgent)
	return createHTTPResponse(solverResp), nil
}

func createHTTPResponse(solverResp *Response) *http.Response {
	resp := &http.Response{
		StatusCode: 200,
		Status:     "200 OK",
		Proto:      "HTTP/1.1",
		ProtoMajor: 1,
		ProtoMinor: 1,
		Header:     make(http.Header),
		Body:       io.NopCloser(strings.NewReader(solverResp.HTML)),
	}

	resp.Header.Set("Content-Type", "text/html; charset=utf-8")
	return resp
}

func isCloudflareChallenge(html string) bool {
	lower := strings.ToLower(html)
	return strings.Contains(lower, "just a moment") ||
		strings.Contains(lower, "checking your browser") ||
		strings.Contains(lower, "verify you are human")
}
