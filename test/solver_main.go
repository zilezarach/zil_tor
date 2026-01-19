package main

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/zilezarach/zil_tor-api/internal/bypass"
	"go.uber.org/zap"
)

func main() {
	logger, _ := zap.NewDevelopment()

	config := &bypass.Config{
		Timeout:      90 * time.Second, // Increased timeout
		MaxInstances: 1,
		Headless:     true, // Set to false to watch it work
		BrowserPath:  "",   // Auto-detect
	}

	solver := bypass.NewSolver(config, logger)
	defer solver.Close()

	ctx := context.Background()
	url := "https://1337x.to/search/ubuntu/1/"

	fmt.Println("Testing improved solver on:", url)
	fmt.Println("This may take 20-30 seconds...")

	// Use retry logic
	resp, err := solver.SolveWithRetry(ctx, url, 2)
	if err != nil {
		fmt.Println("❌ Solver failed:", err)
		return
	}

	fmt.Println("✅ Solver succeeded!")
	fmt.Println("HTML length:", len(resp.HTML))
	fmt.Println("Cookies:", len(resp.Cookies))
	fmt.Println("User-Agent:", resp.UserAgent)

	// Check for Cloudflare
	if strings.Contains(strings.ToLower(resp.HTML), "just a moment") {
		fmt.Println("❌ Still showing Cloudflare challenge")
		fmt.Println("HTML preview:", resp.HTML[:min(500, len(resp.HTML))])
		return
	}

	// Check if HTML contains expected content
	if strings.Contains(resp.HTML, "table-list") {
		fmt.Println("✅ Found table-list in HTML - Cloudflare bypassed!")

		// Count rows
		count := strings.Count(resp.HTML, "<tr>")
		fmt.Println("Found", count, "table rows")
	} else {
		fmt.Println("⚠️  No table-list found")
		fmt.Println("HTML preview:", resp.HTML[:min(1000, len(resp.HTML))])
	}
}

func min(a, b int) int {
	if a < b {
		return a
	}
	return b
}
