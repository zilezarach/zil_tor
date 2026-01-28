package Ext

import (
	"context"
	"fmt"
	"net/http"
	"regexp"
	"time"

	"github.com/chromedp/cdproto/network"
	"github.com/chromedp/chromedp"
)

func FetchTokens(ctx context.Context, url string) (*Tokens, error) {
	var pageToken, csrfToken string
	var pageHTML string

	// Add more detailed logging
	err := chromedp.Run(ctx,
		chromedp.ActionFunc(func(ctx context.Context) error {
			// Verify context is valid
			if ctx.Err() != nil {
				return fmt.Errorf("context already cancelled before navigation: %w", ctx.Err())
			}
			return nil
		}),
		network.Enable(),
		chromedp.Navigate(url),
		chromedp.Sleep(3*time.Second), // Increased wait time
		chromedp.WaitReady("body"),
		// Try to get tokens from window object first
		chromedp.Evaluate(`window.pageToken || ''`, &pageToken),
		chromedp.Evaluate(`window.csrfToken || ''`, &csrfToken),
		// Also get the HTML to parse as fallback
		chromedp.OuterHTML("html", &pageHTML),
	)
	if err != nil {
		return nil, fmt.Errorf("chromedp.Run failed: %w", err)
	}

	// If tokens not in window, try to extract from script tags
	if pageToken == "" || csrfToken == "" {
		extractedPageToken, extractedCsrfToken := extractTokensFromHTML(pageHTML)
		if pageToken == "" {
			pageToken = extractedPageToken
		}
		if csrfToken == "" {
			csrfToken = extractedCsrfToken
		}
	}

	if pageToken == "" || csrfToken == "" {
		return nil, fmt.Errorf("missing ext tokens: pageToken=%q, csrfToken=%q", pageToken, csrfToken)
	}

	cdpCookies, err := network.GetCookies().Do(ctx)
	if err != nil {
		return nil, fmt.Errorf("failed to get cookies: %w", err)
	}

	httpCookies := make([]*http.Cookie, 0, len(cdpCookies))
	for _, c := range cdpCookies {
		httpCookies = append(httpCookies, &http.Cookie{
			Name:     c.Name,
			Value:    c.Value,
			Domain:   c.Domain,
			Path:     c.Path,
			Secure:   c.Secure,
			HttpOnly: c.HTTPOnly,
		})
	}

	return &Tokens{
		PageToken: pageToken,
		CsrfToken: csrfToken,
		Cookies:   httpCookies,
	}, nil
}

// extractTokensFromHTML extracts pageToken and csrfToken from script tags
func extractTokensFromHTML(html string) (pageToken, csrfToken string) {
	// Look for: window.pageToken = "...";
	pageTokenRe := regexp.MustCompile(`window\.pageToken\s*=\s*["']([^"']+)["']`)
	if matches := pageTokenRe.FindStringSubmatch(html); len(matches) > 1 {
		pageToken = matches[1]
	}

	// Look for: window.csrfToken = "...";
	csrfTokenRe := regexp.MustCompile(`window\.csrfToken\s*=\s*["']([^"']+)["']`)
	if matches := csrfTokenRe.FindStringSubmatch(html); len(matches) > 1 {
		csrfToken = matches[1]
	}

	return pageToken, csrfToken
}
