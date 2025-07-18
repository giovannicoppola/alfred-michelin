package parser

import (
	"encoding/json"
	"fmt"
	"regexp"
	"strconv"
	"strings"
	"time"

	"os"
	"path/filepath"

	"github.com/ngshiheng/michelin-my-maps/v3/internal/models"
	"github.com/nyaruka/phonenumbers"
)

// SplitUnpack performs SplitN and unpacks a string.
func SplitUnpack(str string, separator string) (string, string) {
	if len(str) == 0 {
		return str, str
	}

	parsedStr := strings.SplitN(str, separator, 2)

	for i, s := range parsedStr {
		parsedStr[i] = strings.TrimSpace(s)
	}

	if len(parsedStr) == 1 {
		return "", parsedStr[0] // Always assume price is missing
	}

	return parsedStr[0], parsedStr[1]
}

// ParsePublishedYearFromJSONLD extracts the year from a Michelin Guide JSON-LD script.
// Returns 0 if not found or invalid.
func ParsePublishedYearFromJSONLD(jsonLD string) (int, error) {
	if jsonLD == "" {
		return 0, nil
	}
	var ld map[string]any
	if err := json.Unmarshal([]byte(jsonLD), &ld); err != nil {
		return 0, err
	}
	review, ok := ld["review"].(map[string]any)
	if !ok {
		return 0, nil
	}
	pd, ok := review["datePublished"].(string)
	if !ok || pd == "" {
		return 0, nil
	}
	layouts := []string{"2006-01-02", "2006-01-02T15:04", "2006-01-02T15:04:05"}
	for _, layout := range layouts {
		if t, err := time.Parse(layout, pd); err == nil {
			return t.Year(), nil
		}
	}
	if len(pd) == 4 {
		if y, err := strconv.Atoi(pd); err == nil {
			return y, nil
		}
	}
	return 0, nil
}

// TrimWhiteSpaces trims whitespace character such as line breaks or double spaces.
func TrimWhiteSpaces(str string) string {
	trimWhiteSpace := strings.NewReplacer("\n", "", "  ", "")
	return trimWhiteSpace.Replace(str)
}

// ParseDistinction parses the Michelin distinction based on the input string.
func ParseDistinction(distinction string) string {
	s := strings.ToLower(distinction)
	s = decodeHTMLEntities(s)
	s = strings.Trim(s, " .!?,;:-")
	s = strings.TrimSpace(s)

	switch {
	case re3Stars.MatchString(s):
		return models.ThreeStars
	case re2Stars.MatchString(s):
		return models.TwoStars
	case re1Star.MatchString(s):
		return models.OneStar
	case reBibGourmand.MatchString(s):
		return models.BibGourmand
	case reSelected.MatchString(s):
		return models.SelectedRestaurants
	default:
		return models.SelectedRestaurants
	}
}

var (
	re3Stars      = regexp.MustCompile(`(?i)\b(three|3)\b.*?\bstars?\b`)
	re2Stars      = regexp.MustCompile(`(?i)\b(two|2)\b.*?\bstars?\b`)
	re1Star       = regexp.MustCompile(`(?i)\b(one|1)\b.*?\bstar\b`)
	reBibGourmand = regexp.MustCompile(`(?i)\bbib\b`)
	reSelected    = regexp.MustCompile(`(?i)\bselected\s*restaurants?\b|\bplate\b`)
)

// decodeHTMLEntities decodes basic HTML entities (extend as needed)
func decodeHTMLEntities(s string) string {
	s = strings.ReplaceAll(s, "&bull;", "")
	s = strings.ReplaceAll(s, "•", "")
	return s
}

// ParseGreenStar parses the MICHELIN Green Star based on the input string.
func ParseGreenStar(distinction string) bool {
	return strings.ToLower(distinction) == "michelin green star"
}

/*
ParsePhoneNumber extracts and parses phone number from a raw string.

Example inputPhoneNumber: "+81 3-3874-1552"
*/
func ParsePhoneNumber(inputPhoneNumber string) string {
	parsedPhoneNumber, err := phonenumbers.Parse(inputPhoneNumber, "")
	if err != nil {
		return ""
	}

	return phonenumbers.Format(parsedPhoneNumber, phonenumbers.E164)
}

// MapPrice maps CAT_P01 ... CAT_P04 to $, $$, $$$, $$$$.
func MapPrice(price string) string {
	if price == "" {
		return ""
	}
	switch price {
	case "CAT_P01":
		return "$"
	case "CAT_P02":
		return "$$"
	case "CAT_P03":
		return "$$$"
	case "CAT_P04":
		return "$$$$"
	default:
		return price
	}
}

// ParseYear parses the year from a publishedDate string.
func ParseYear(publishedDate string) int {
	if publishedDate == "" {
		return 0
	}
	layouts := []string{"2006-01-02", "2006-01-02T15:04", "2006-01-02T15:04:05"}
	for _, layout := range layouts {
		if t, err := time.Parse(layout, publishedDate); err == nil {
			return t.Year()
		}
	}
	if len(publishedDate) == 4 {
		if y, err := strconv.Atoi(publishedDate); err == nil {
			return y
		}
	}
	return 0
}

// ParseYearFromFilename extracts the year from a filename like "2022-03-13_michelin_my_maps 2.csv"
func ParseYearFromFilename(filename string) int {
	// Extract date pattern from filename (YYYY-MM-DD)
	re := regexp.MustCompile(`(\d{4})-(\d{2})-(\d{2})`)
	matches := re.FindStringSubmatch(filename)
	if len(matches) > 1 {
		if year, err := strconv.Atoi(matches[1]); err == nil {
			return year
		}
	}
	return 0
}

// ParseDateFromFilename extracts the full date from a filename like "2022-03-13_michelin_my_maps 2.csv"
// Returns a time.Time for proper chronological sorting when multiple files exist per year
func ParseDateFromFilename(filename string) (time.Time, error) {
	// Extract date pattern from filename (YYYY-MM-DD)
	re := regexp.MustCompile(`(\d{4})-(\d{2})-(\d{2})`)
	matches := re.FindStringSubmatch(filename)
	if len(matches) >= 4 {
		dateStr := fmt.Sprintf("%s-%s-%s", matches[1], matches[2], matches[3])
		date, err := time.Parse("2006-01-02", dateStr)
		if err != nil {
			return time.Time{}, fmt.Errorf("failed to parse date from filename %s: %w", filename, err)
		}
		return date, nil
	}
	return time.Time{}, fmt.Errorf("no date pattern found in filename: %s", filename)
}

// ParseDateFromFilenameWithFallback extracts date from filename, falling back to file creation date if not found
// This is useful for processing any CSV dataset version (historical, current, or future)
func ParseDateFromFilenameWithFallback(filePath string) (time.Time, error) {
	filename := filepath.Base(filePath)

	// First try to extract date from filename
	if date, err := ParseDateFromFilename(filename); err == nil {
		return date, nil
	}

	// Fallback to file creation/modification date
	fileInfo, err := os.Stat(filePath)
	if err != nil {
		return time.Time{}, fmt.Errorf("failed to get file info for %s: %w", filePath, err)
	}

	// Use modification time as the fallback date
	return fileInfo.ModTime(), nil
}

/*
ParseDLayerValue parses a value from a dLayer script.

Supported: Only extracts from assignment syntax, not object literals.

Example (supported):

	script := "dLayer['distinction'] = '3 star';"
	value := ParseDLayerValue(script, "distinction")
	// value == "3 star"

Example (not supported):

	script := "dLayer = { 'distinction': '1 star' };"
	value := ParseDLayerValue(script, "distinction")
	// value == ""

To support object literal syntax, the parsing logic must be extended.
*/
func ParseDLayerValue(script, key string) string {
	re := regexp.MustCompile(key + `'\]\s*=\s*'([^']*)'`)
	m := re.FindStringSubmatch(script)
	if len(m) > 1 {
		return m[1]
	}
	return ""
}
