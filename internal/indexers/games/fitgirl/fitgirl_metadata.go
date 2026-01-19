package fitgirl

import (
	"regexp"
	"strings"
)

// FitGirlMetadata represents metadata extracted from FitGirl release titles
type FitGirlMetadata struct {
	GameName          string
	Version           string
	IncludesDLCs      bool
	SelectiveDownload bool
	Languages         []string
	RepackSize        string
	OriginalSize      string
}

// isFitGirlRelease checks if the title is a FitGirl release
func isFitGirlRelease(title string) bool {
	titleLower := strings.ToLower(title)
	return strings.Contains(titleLower, "fitgirl repack") ||
		strings.Contains(titleLower, "fit girl") ||
		strings.Contains(titleLower, "fg-repack")
}

// extractFitGirlMetadata extracts metadata from FitGirl release title
func extractFitGirlMetadata(title string) FitGirlMetadata {
	metadata := FitGirlMetadata{
		Languages: make([]string, 0),
	}

	// Extract game name (everything before " – " or " - ")
	parts := regexp.MustCompile(`\s+[-–]\s+`).Split(title, -1)
	if len(parts) > 0 {
		metadata.GameName = strings.TrimSpace(parts[0])
		// Remove "FitGirl Repack" suffix if present
		metadata.GameName = regexp.MustCompile(`(?i)\s*\(?fitgirl\s*repack\)?`).ReplaceAllString(metadata.GameName, "")
		metadata.GameName = strings.TrimSpace(metadata.GameName)
	}

	// Extract version
	versionRe := regexp.MustCompile(`v?(\d+\.[\d.]+)`)
	if matches := versionRe.FindStringSubmatch(title); len(matches) > 1 {
		metadata.Version = matches[1]
	}

	// Check for DLCs
	metadata.IncludesDLCs = regexp.MustCompile(`(?i)(dlc|DLC|all.?dlc)`).MatchString(title)

	// Check for selective download
	metadata.SelectiveDownload = regexp.MustCompile(`(?i)selective`).MatchString(title)

	// Extract languages
	languagePatterns := []string{
		"English", "Russian", "Spanish", "French", "German",
		"Italian", "Japanese", "Chinese", "Korean", "Portuguese",
		"Polish", "Turkish", "Arabic", "Multi", "MULTi",
	}

	titleLower := strings.ToLower(title)
	for _, lang := range languagePatterns {
		if strings.Contains(titleLower, strings.ToLower(lang)) {
			metadata.Languages = append(metadata.Languages, lang)
		}
	}

	// If Multi found, it includes multiple languages
	if len(metadata.Languages) == 0 {
		metadata.Languages = append(metadata.Languages, "English")
	}

	return metadata
}

// extractRepackSize extracts repack size from title or description
func extractRepackSize(text string) string {
	// Pattern: "Repack Size: 10.5 GB" or "Size: 10.5 GB"
	re := regexp.MustCompile(`(?i)(?:repack\s+)?size[:\s]+([0-9.]+\s*[KMGT]B)`)
	if matches := re.FindStringSubmatch(text); len(matches) > 1 {
		return matches[1]
	}
	return ""
}
