package main

import (
	"archive/zip"
	"database/sql"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/giovanni/alfred-michelin/db"
)

// Constants
const (
	// Alfred-related
	Title        = "Title"
	Subtitle     = "Subtitle"
	Arg          = "Arg"
	Icon         = "Icon"
	Valid        = "Valid"
	Autocomplete = "Autocomplete"
)

// Mod represents modifier-specific configuration
type Mod struct {
	Subtitle  string                 `json:"subtitle,omitempty"`
	Arg       string                 `json:"arg,omitempty"`
	Variables map[string]interface{} `json:"variables,omitempty"`
	Valid     *bool                  `json:"valid,omitempty"`
}

// Alfred item structure for results
type AlfredItem struct {
	Title        string                 `json:"title"`
	Subtitle     string                 `json:"subtitle,omitempty"`
	Arg          string                 `json:"arg,omitempty"`
	Icon         map[string]string      `json:"icon,omitempty"`
	Valid        bool                   `json:"valid"`
	Autocomplete string                 `json:"autocomplete,omitempty"`
	Variables    map[string]interface{} `json:"variables,omitempty"`
	Mods         map[string]Mod         `json:"mods,omitempty"`
}

// Alfred results structure
type AlfredResult struct {
	Items []AlfredItem `json:"items"`
}

// Timing utility function
func timeQuery(operation string, fn func() error) error {
	start := time.Now()
	err := fn()
	duration := time.Since(start)

	// Output timing to stderr for Alfred debugger
	fmt.Fprintf(os.Stderr, "[TIMING] %s took %d ms (%.2f μs)\n",
		operation,
		duration.Nanoseconds()/1000000,
		float64(duration.Nanoseconds())/1000.0)

	return err
}

// initializeDatabase handles database initialization and updates
func initializeDatabase() error {
	// 1. Check if the workflow data folder exists, create if not
	workflowDataDir := os.Getenv("alfred_workflow_data")
	if workflowDataDir == "" {
		return fmt.Errorf("alfred_workflow_data environment variable not set")
	}

	if err := os.MkdirAll(workflowDataDir, 0755); err != nil {
		return fmt.Errorf("failed to create workflow data directory: %v", err)
	}

	// 2. Check if michelin.db.zip is present in current directory
	currentDir, err := os.Getwd()
	if err != nil {
		return fmt.Errorf("failed to get current directory: %v", err)
	}

	zipPath := filepath.Join(currentDir, "michelin.db.zip")
	zipExists := false
	if _, err := os.Stat(zipPath); err == nil {
		zipExists = true
	}

	if !zipExists {
		// Check if michelin.db exists in workflow data folder
		dbPath := filepath.Join(workflowDataDir, "michelin.db")
		if _, err := os.Stat(dbPath); os.IsNotExist(err) {
			// Database doesn't exist, show error
			items := []AlfredItem{
				{
					Title:    "Error, missing database",
					Subtitle: "delete and re-install this workflow, or contact the developer",
					Valid:    false,
				},
			}
			result := AlfredResult{Items: items}
			printJSON(result)
			return fmt.Errorf("missing database file")
		}
		// Database exists, no action needed
		return nil
	}

	// 3. Zip file exists, extract and move database
	fmt.Fprintf(os.Stderr, "[DEBUG] Found michelin.db.zip, extracting...\n")

	// Create temporary directory for extraction
	tempDir, err := os.MkdirTemp("", "michelin_extract_")
	if err != nil {
		return fmt.Errorf("failed to create temp directory: %v", err)
	}
	defer os.RemoveAll(tempDir)

	// Extract the zip file
	extractedDbPath := filepath.Join(tempDir, "michelin.db")
	if err := extractZipFile(zipPath, extractedDbPath); err != nil {
		return fmt.Errorf("failed to extract zip file: %v", err)
	}

	// Check if extracted database exists
	if _, err := os.Stat(extractedDbPath); os.IsNotExist(err) {
		return fmt.Errorf("no michelin.db found in zip file")
	}

	// Move to workflow data directory
	targetDbPath := filepath.Join(workflowDataDir, "michelin.db")

	// Check if database already exists and preserve user data
	if _, err := os.Stat(targetDbPath); err == nil {
		backupPath := filepath.Join(workflowDataDir, "michelin_backup.db")
		if err := os.Rename(targetDbPath, backupPath); err != nil {
			return fmt.Errorf("failed to create backup of existing database: %v", err)
		}
		fmt.Fprintf(os.Stderr, "[DEBUG] Created backup of existing database\n")

		// Preserve user data from backup to new database
		if err := preserveUserData(backupPath, extractedDbPath); err != nil {
			fmt.Fprintf(os.Stderr, "[WARNING] Failed to preserve user data: %v\n", err)
			// Continue anyway, but log the warning
		} else {
			fmt.Fprintf(os.Stderr, "[DEBUG] User data preserved successfully\n")
		}
	}

	// Copy new database to target location
	if err := copyFile(extractedDbPath, targetDbPath); err != nil {
		return fmt.Errorf("failed to copy database to target location: %v", err)
	}

	fmt.Fprintf(os.Stderr, "[DEBUG] Database updated successfully\n")

	// 4. Delete the zip file
	if err := os.Remove(zipPath); err != nil {
		fmt.Fprintf(os.Stderr, "[WARNING] Failed to remove zip file: %v\n", err)
		// Don't return error here as the database update was successful
	} else {
		fmt.Fprintf(os.Stderr, "[DEBUG] Removed zip file\n")
	}

	return nil
}

// Main function
func main() {
	// Check command-line arguments
	if len(os.Args) < 2 {
		fmt.Fprintf(os.Stderr, "[ERROR] Usage: alfred-michelin [command] [arguments]\n")
		os.Exit(1)
	}

	// Initialize database and check for updates
	if err := initializeDatabase(); err != nil {
		showError(fmt.Sprintf("Database initialization failed: %v", err))
		os.Exit(1)
	}

	// Get workflow directory (where the executable is)
	workDir, err := getWorkingDirectory()
	if err != nil {
		fmt.Fprintf(os.Stderr, "[ERROR] %v\n", err)
		os.Exit(1)
	}

	// Use the same database path that initializeDatabase() uses
	workflowDataDir := os.Getenv("alfred_workflow_data")
	if workflowDataDir == "" {
		workflowDataDir = workDir // fallback to workflow directory
	}
	dbPath := filepath.Join(workflowDataDir, db.DbFileName)
	database, err := db.Initialize(dbPath)
	if err != nil {
		fmt.Fprintf(os.Stderr, "[ERROR] Error initializing database: %v\n", err)
		os.Exit(1)
	}
	defer database.Close()

	// Skip CSV import for new database - data should already exist
	// The new database comes pre-populated with restaurant data

	// Automatically check for database updates before processing any commands
	fmt.Fprintf(os.Stderr, "[DEBUG] Checking for database updates...\n")
	err = db.UpdateDatabase(dbPath)
	if err != nil {
		if db.IsNoUpdateAvailable(err) {
			// No update file found - this is normal, just log at debug level
			fmt.Fprintf(os.Stderr, "[DEBUG] No database update file found\n")
		} else {
			// Real error occurred during update - log it but continue
			fmt.Fprintf(os.Stderr, "[ERROR] Database update failed: %v\n", err)
		}
	} else {
		fmt.Fprintf(os.Stderr, "[DEBUG] Database update completed successfully\n")
		// Reinitialize database connection after update
		database.Close()
		database, err = db.Initialize(dbPath)
		if err != nil {
			fmt.Fprintf(os.Stderr, "[ERROR] Error reinitializing database after update: %v\n", err)
			os.Exit(1)
		}
	}

	// Process commands
	command := os.Args[1]
	switch command {
	case "search":
		var query string

		// Debug output to stderr
		fmt.Fprintf(os.Stderr, "[DEBUG] Search command called\n")
		fmt.Fprintf(os.Stderr, "[DEBUG] Command args: %v\n", os.Args)
		fmt.Fprintf(os.Stderr, "[DEBUG] search_query env var: '%s'\n", os.Getenv("search_query"))
		fmt.Fprintf(os.Stderr, "[DEBUG] mode env var: '%s'\n", os.Getenv("mode"))

		// Check if there's a search query provided
		if len(os.Args) >= 3 {
			query = os.Args[2]
			fmt.Fprintf(os.Stderr, "[DEBUG] Using command line query: '%s'\n", query)
		} else {
			fmt.Fprintf(os.Stderr, "[DEBUG] No query available, showing no results\n")
			showNoResults("Enter a search term...")
			return
		}

		// Check if we're in a specific mode (favorites or visited)
		mode := os.Getenv("mode")
		switch mode {
		case "favorites":
			fmt.Fprintf(os.Stderr, "[DEBUG] Search in favorites mode\n")
			handleSearchFavorites(database, query)
		case "visited":
			fmt.Fprintf(os.Stderr, "[DEBUG] Search in visited mode\n")
			handleSearchVisited(database, query)
		default:
			fmt.Fprintf(os.Stderr, "[DEBUG] Search in normal mode\n")
			handleSearch(database, query)
		}

	case "toggle-favorite":
		if len(os.Args) < 3 {
			showError("Missing restaurant ID")
			return
		}
		id, err := strconv.ParseInt(os.Args[2], 10, 64)
		if err != nil {
			showError("Invalid restaurant ID")
			return
		}
		handleToggleFavorite(database, id)

	case "toggle-visited":
		if len(os.Args) < 3 {
			showError("Missing restaurant ID")
			return
		}
		id, err := strconv.ParseInt(os.Args[2], 10, 64)
		if err != nil {
			showError("Invalid restaurant ID")
			return
		}
		date := time.Now().Format("2006-01-02")
		notes := ""
		if len(os.Args) > 3 {
			notes = os.Args[3]
		}
		handleToggleVisited(database, id, date, notes)

	case "favorites":
		if len(os.Args) >= 3 {
			// Search within favorites
			query := os.Args[2]
			fmt.Fprintf(os.Stderr, "[DEBUG] Searching within favorites with query: '%s'\n", query)
			handleSearchFavorites(database, query)
		} else {
			// Show all favorites
			handleFavorites(database)
		}

	case "visited":
		if len(os.Args) >= 3 {
			// Search within visited
			query := os.Args[2]
			fmt.Fprintf(os.Stderr, "[DEBUG] Searching within visited with query: '%s'\n", query)
			handleSearchVisited(database, query)
		} else {
			// Show all visited
			handleVisited(database)
		}

	case "award-history":
		if len(os.Args) < 3 {
			showError("Missing restaurant ID")
			return
		}
		id, err := strconv.ParseInt(os.Args[2], 10, 64)
		if err != nil {
			showError("Invalid restaurant ID")
			return
		}

		// Get search query from environment variable or argument
		searchQuery := os.Getenv("search_query")
		if len(os.Args) >= 4 {
			searchQuery = os.Args[3]
		}

		// Get mode from environment variable
		mode := os.Getenv("mode")

		// Debug output to stderr
		fmt.Fprintf(os.Stderr, "[DEBUG] Award history command called\n")
		fmt.Fprintf(os.Stderr, "[DEBUG] Restaurant ID: %d\n", id)
		fmt.Fprintf(os.Stderr, "[DEBUG] search_query env var before: '%s'\n", os.Getenv("search_query"))
		fmt.Fprintf(os.Stderr, "[DEBUG] mode env var: '%s'\n", mode)
		fmt.Fprintf(os.Stderr, "[DEBUG] Final search query for award history: '%s'\n", searchQuery)

		// Set the search query environment variable if provided
		if searchQuery != "" {
			os.Setenv("search_query", searchQuery)
			fmt.Fprintf(os.Stderr, "[DEBUG] Set search_query env var to: '%s'\n", searchQuery)
		}

		// Set the mode environment variable if provided
		if mode != "" {
			os.Setenv("mode", mode)
			fmt.Fprintf(os.Stderr, "[DEBUG] Set mode env var to: '%s'\n", mode)
		}

		handleAwardHistory(database, id, searchQuery)

	case "back":
		// Go back to search results - the search command will pick up the file-stored query
		var query string

		// Debug output to stderr
		fmt.Fprintf(os.Stderr, "[DEBUG] Back command called\n")
		fmt.Fprintf(os.Stderr, "[DEBUG] Command args: %v\n", os.Args)
		fmt.Fprintf(os.Stderr, "[DEBUG] search_query env var: '%s'\n", os.Getenv("search_query"))
		fmt.Fprintf(os.Stderr, "[DEBUG] mode env var: '%s'\n", os.Getenv("mode"))

		// Get query from environment variable
		if envQuery := os.Getenv("search_query"); envQuery != "" {
			query = envQuery
			fmt.Fprintf(os.Stderr, "[DEBUG] Using environment query for back: '%s'\n", query)
		} else {
			fmt.Fprintf(os.Stderr, "[DEBUG] No search query available for back command\n")
			showError("No search query available. Please perform a new search.")
			return
		}

		// Check if we're in a specific mode and use the appropriate search function
		mode := os.Getenv("mode")
		switch mode {
		case "favorites":
			fmt.Fprintf(os.Stderr, "[DEBUG] Back to favorites mode\n")
			handleSearchFavorites(database, query)
		case "visited":
			fmt.Fprintf(os.Stderr, "[DEBUG] Back to visited mode\n")
			handleSearchVisited(database, query)
		default:
			fmt.Fprintf(os.Stderr, "[DEBUG] Back to normal search mode\n")
			handleSearch(database, query)
		}

	case "showDescription":
		fmt.Fprintf(os.Stderr, "[DEBUG] Show description command called\n")
		handleShowDescription(workDir)

	default:
		showError(fmt.Sprintf("Unknown command: %s", command))
	}
}

// getWorkingDirectory returns the directory where the executable is located
func getWorkingDirectory() (string, error) {
	// First try to get the Alfred workflow directory from environment
	alfredWorkflow := os.Getenv("alfred_workflow_data")
	if alfredWorkflow != "" {
		return alfredWorkflow, nil
	}

	// If not available, use the executable directory
	exePath, err := os.Executable()
	if err != nil {
		return "", fmt.Errorf("failed to get executable path: %v", err)
	}
	return filepath.Dir(exePath), nil
}

// handleSearch searches restaurants and returns results in Alfred format
func handleSearch(database *sql.DB, query string) {
	// Search restaurants with timing
	var restaurants []db.Restaurant
	var err error

	var isEmptySearch bool
	timeQuery(fmt.Sprintf("search query: '%s'", query), func() error {
		restaurants, isEmptySearch, err = db.SearchRestaurants(database, query)
		return err
	})

	if err != nil {
		showError(fmt.Sprintf("Search error: %v", err))
		return
	}

	// Check if no results found
	if len(restaurants) == 0 {
		showNoResults("No restaurants found. Try a different search term.")
		return
	}

	// Format results for Alfred
	items := make([]AlfredItem, 0, len(restaurants))

	// Determine total count based on search type
	var totalCount int
	if isEmptySearch {
		// For empty search, get total database count
		totalCount, err = db.GetTotalRestaurantCount(database)
		if err != nil {
			fmt.Fprintf(os.Stderr, "[ERROR] Failed to get total count: %v\n", err)
			totalCount = len(restaurants) // Fallback to result count
		}
	} else {
		// For actual searches, use result count
		totalCount = len(restaurants)
	}

	for i, r := range restaurants {
		// Get cuisine or set default value
		cuisine := "Unknown cuisine"
		if r.Cuisine != nil && *r.Cuisine != "" {
			cuisine = *r.Cuisine
		}

		// Get location or set default value
		location := "Unknown location"
		if r.Location != nil && *r.Location != "" {
			location = *r.Location
		}

		// Create Alfred item
		restaurantName := "Unknown restaurant"
		if r.Name != nil && *r.Name != "" {
			restaurantName = *r.Name
		}

		// Add scroll emoji if restaurant is no longer in the guide
		if r.InGuide == 0 {
			restaurantName = restaurantName + " 📜"
		}

		// Add heart emoji to title for favorites
		favoriteEmoji := "❤️ add to favorites"
		if r.IsFavorite {
			restaurantName = restaurantName + " ❤️"
			favoriteEmoji = "💔 remove from favorites"
		}

		// Add visited emoji to title if needed
		visitedEmoji := "✅ add to visited"
		if r.IsVisited {
			restaurantName = restaurantName + " ✅"
			visitedEmoji = "❌ remove from visited"
		}

		// Format award display with stars and year
		award := formatAwardWithYearRange(r.CurrentAward, r.CurrentAwardYear, r.CurrentAwardLastYear, r.CurrentGreenStar, r.InGuide)

		// Create counter prefix
		counter := fmt.Sprintf("%s/%s", formatNumber(i+1), formatNumber(totalCount))

		// Determine the OPEN_IN_URL variable based on user preference
		openIn := os.Getenv("OPEN_IN")
		var openInURL string
		if openIn == "" {
			openIn = "restaurant" // default
		}
		switch openIn {
		case "restaurant":
			if r.WebsiteUrl != nil && *r.WebsiteUrl != "" {
				openInURL = *r.WebsiteUrl
			} else if r.Url != nil && *r.Url != "" {
				openInURL = *r.Url
			}
		case "michelin":
			if r.Url != nil && *r.Url != "" {
				openInURL = *r.Url
			}
		case "maps":
			if r.Latitude != nil && r.Longitude != nil && *r.Latitude != "" && *r.Longitude != "" {
				if r.Name != nil && *r.Name != "" {
					encodedName := url.QueryEscape(*r.Name)
					openInURL = fmt.Sprintf("https://www.google.com/maps?q=%s&ll=%s,%s&z=15", encodedName, *r.Latitude, *r.Longitude)
				} else {
					openInURL = fmt.Sprintf("https://www.google.com/maps?ll=%s,%s&z=15", *r.Latitude, *r.Longitude)
				}
			}
		case "apple_maps":
			if r.Latitude != nil && r.Longitude != nil && *r.Latitude != "" && *r.Longitude != "" {
				if r.Name != nil && *r.Name != "" {
					openInURL = fmt.Sprintf("https://maps.apple.com/?q=%s&ll=%s,%s", url.QueryEscape(*r.Name), *r.Latitude, *r.Longitude)
				} else {
					openInURL = fmt.Sprintf("https://maps.apple.com/?ll=%s,%s", *r.Latitude, *r.Longitude)
				}
			}
		}

		item := AlfredItem{
			Title: restaurantName,
			Subtitle: fmt.Sprintf("%s | %s | %s | %s",
				counter,
				location,
				award,
				cuisine),
			Arg: "",

			Valid: true,
			Variables: map[string]interface{}{
				"restaurant_id":      r.ID,
				"restaurant_name":    restaurantName,
				"is_favorite":        r.IsFavorite,
				"is_visited":         r.IsVisited,
				"restaurant_url":     r.Url,
				"website_url":        r.WebsiteUrl,
				"search_query":       query,
				"mode":               "",
				"favorite_emoji":     favoriteEmoji,
				"visited_emoji":      visitedEmoji,
				"myDescription":      r.Description,
				"imageURL":           r.ImageURL,
				"restaurant_address": r.Address,
				"restaurant_award":   award,
				"OPEN_IN_URL":        openInURL,
			},
			Mods: map[string]Mod{
				"ctrl": {
					Subtitle: favoriteEmoji,
				},
				"alt": {
					Subtitle: visitedEmoji,
				},
			},
		}

		// Add icon based on award type and guide status
		if r.InGuide == 0 {
			// Restaurant no longer in guide - show black star
			item.Icon = map[string]string{
				"path": "icons/blackStar.png",
			}
		} else if r.CurrentAward != nil {
			// Restaurant in guide - show award-based icon
			awardLower := strings.ToLower(*r.CurrentAward)
			if strings.Contains(awardLower, "bib gourmand") {
				item.Icon = map[string]string{
					"path": "icons/bibg.png",
				}
			} else if strings.Contains(awardLower, "selected restaurant") {
				item.Icon = map[string]string{
					"path": "icons/star.png",
				}
			}
		}

		items = append(items, item)
	}

	// Return results
	result := AlfredResult{Items: items}
	if err := printJSON(result); err != nil {
		showError(fmt.Sprintf("Error formatting results: %v", err))
	}
}

// handleSearchFavorites searches within favorite restaurants and returns results in Alfred format
func handleSearchFavorites(database *sql.DB, query string) {
	// Search favorite restaurants with timing
	var restaurants []db.Restaurant
	var err error

	timeQuery(fmt.Sprintf("search favorites query: '%s'", query), func() error {
		restaurants, err = db.SearchFavoriteRestaurants(database, query)
		return err
	})

	if err != nil {
		showError(fmt.Sprintf("Search favorites error: %v", err))
		return
	}

	// Check if no results found
	if len(restaurants) == 0 {
		showNoResults("No favorite restaurants found matching your search.")
		return
	}

	// Format results for Alfred
	items := make([]AlfredItem, 0, len(restaurants))
	totalCount := len(restaurants)

	for i, r := range restaurants {
		// Get cuisine or set default value
		cuisine := "Unknown cuisine"
		if r.Cuisine != nil && *r.Cuisine != "" {
			cuisine = *r.Cuisine
		}

		// Get location or set default value
		location := "Unknown location"
		if r.Location != nil && *r.Location != "" {
			location = *r.Location
		}

		// Create Alfred item
		restaurantName := "Unknown restaurant"
		if r.Name != nil && *r.Name != "" {
			restaurantName = *r.Name
		}

		// Add scroll emoji if restaurant is no longer in the guide
		if r.InGuide == 0 {
			restaurantName = restaurantName + " 📜"
		}

		// Add heart emoji to title for favorites (always for favorites search)
		restaurantName = restaurantName + " ❤️"

		// Add visited emoji to title if needed
		if r.IsVisited {
			restaurantName = restaurantName + " ✅"
		}

		// Format award display with stars and year
		award := formatAwardWithYearRange(r.CurrentAward, r.CurrentAwardYear, r.CurrentAwardLastYear, r.CurrentGreenStar, r.InGuide)

		// Create counter prefix
		counter := fmt.Sprintf("%s/%s", formatNumber(i+1), formatNumber(totalCount))

		// Determine the OPEN_IN_URL variable based on user preference
		openIn := os.Getenv("OPEN_IN")
		var openInURL string
		if openIn == "" {
			openIn = "restaurant" // default
		}
		switch openIn {
		case "restaurant":
			if r.WebsiteUrl != nil && *r.WebsiteUrl != "" {
				openInURL = *r.WebsiteUrl
			} else if r.Url != nil && *r.Url != "" {
				openInURL = *r.Url
			}
		case "michelin":
			if r.Url != nil && *r.Url != "" {
				openInURL = *r.Url
			}
		case "maps":
			if r.Longitude != nil && r.Latitude != nil && *r.Longitude != "" && *r.Latitude != "" {
				if r.Name != nil && *r.Name != "" {
					openInURL = fmt.Sprintf("https://www.google.com/maps/place/%s/@%s,%s,17z", url.QueryEscape(*r.Name), *r.Latitude, *r.Longitude)
				} else {
					openInURL = fmt.Sprintf("https://www.google.com/maps/@%s,%s,17z", *r.Latitude, *r.Longitude)
				}
			}
		case "apple_maps":
			if r.Latitude != nil && r.Longitude != nil && *r.Latitude != "" && *r.Longitude != "" {
				if r.Name != nil && *r.Name != "" {
					openInURL = fmt.Sprintf("https://maps.apple.com/?q=%s&ll=%s,%s", url.QueryEscape(*r.Name), *r.Latitude, *r.Longitude)
				} else {
					openInURL = fmt.Sprintf("https://maps.apple.com/?ll=%s,%s", *r.Latitude, *r.Longitude)
				}
			}
		}

		item := AlfredItem{
			Title: restaurantName,
			Subtitle: fmt.Sprintf("%s | %s | %s | %s",
				counter,
				location,
				award,
				cuisine),
			Arg:   "",
			Valid: true,
			Variables: map[string]interface{}{
				"restaurant_id":      r.ID,
				"restaurant_name":    restaurantName,
				"is_favorite":        r.IsFavorite,
				"is_visited":         r.IsVisited,
				"restaurant_url":     r.Url,
				"website_url":        r.WebsiteUrl,
				"search_query":       query,
				"mode":               "favorites",
				"imageURL":           r.ImageURL,
				"myDescription":      r.Description,
				"restaurant_address": r.Address,
				"restaurant_award":   award,
				"OPEN_IN_URL":        openInURL,
			},
		}

		// Add icon based on award type
		if r.CurrentAward != nil {
			awardLower := strings.ToLower(*r.CurrentAward)
			if strings.Contains(awardLower, "bib gourmand") {
				item.Icon = map[string]string{
					"path": "icons/bibg.png",
				}
			} else if strings.Contains(awardLower, "selected restaurant") {
				item.Icon = map[string]string{
					"path": "icons/star.png",
				}
			}
		}

		items = append(items, item)
	}

	// Return results
	result := AlfredResult{Items: items}
	if err := printJSON(result); err != nil {
		showError(fmt.Sprintf("Error formatting results: %v", err))
	}
}

// handleSearchVisited searches within visited restaurants and returns results in Alfred format
func handleSearchVisited(database *sql.DB, query string) {
	// Search visited restaurants with timing
	var restaurants []db.Restaurant
	var err error

	timeQuery(fmt.Sprintf("search visited query: '%s'", query), func() error {
		restaurants, err = db.SearchVisitedRestaurants(database, query)
		return err
	})

	if err != nil {
		showError(fmt.Sprintf("Search visited error: %v", err))
		return
	}

	// Check if no results found
	if len(restaurants) == 0 {
		showNoResults("No visited restaurants found matching your search.")
		return
	}

	// Format results for Alfred
	items := make([]AlfredItem, 0, len(restaurants))
	totalCount := len(restaurants)

	for i, r := range restaurants {
		// Get cuisine or set default value
		cuisine := "Unknown cuisine"
		if r.Cuisine != nil && *r.Cuisine != "" {
			cuisine = *r.Cuisine
		}

		// Get location or set default value
		location := "Unknown location"
		if r.Location != nil && *r.Location != "" {
			location = *r.Location
		}

		// Get restaurant name
		restaurantName := "Unknown restaurant"
		if r.Name != nil && *r.Name != "" {
			restaurantName = *r.Name
		}

		// Add scroll emoji if restaurant is no longer in the guide
		if r.InGuide == 0 {
			restaurantName = restaurantName + " 📜"
		}

		// Add heart emoji to title for favorites
		if r.IsFavorite {
			restaurantName = restaurantName + " ❤️"
		}

		// Add visited emoji to title (always for visited search)
		restaurantName = restaurantName + " ✅"

		// Format award display with stars and year
		award := formatAwardWithYearRange(r.CurrentAward, r.CurrentAwardYear, r.CurrentAwardLastYear, r.CurrentGreenStar, r.InGuide)

		// Create counter prefix
		counter := fmt.Sprintf("%s/%s", formatNumber(i+1), formatNumber(totalCount))

		// Create Alfred item
		subtitle := fmt.Sprintf("%s | %s | %s | %s",
			counter,
			location,
			award,
			cuisine)

		// Add visit date if available
		if r.VisitedDate != nil && *r.VisitedDate != "" {
			subtitle = fmt.Sprintf("%s | Visited: %s", subtitle, *r.VisitedDate)
		}

		// Determine the OPEN_IN_URL variable based on user preference
		openIn := os.Getenv("OPEN_IN")
		var openInURL string
		if openIn == "" {
			openIn = "restaurant" // default
		}
		switch openIn {
		case "restaurant":
			if r.WebsiteUrl != nil && *r.WebsiteUrl != "" {
				openInURL = *r.WebsiteUrl
			} else if r.Url != nil && *r.Url != "" {
				openInURL = *r.Url
			}
		case "michelin":
			if r.Url != nil && *r.Url != "" {
				openInURL = *r.Url
			}
		case "maps":
			if r.Longitude != nil && r.Latitude != nil && *r.Longitude != "" && *r.Latitude != "" {
				if r.Name != nil && *r.Name != "" {
					openInURL = fmt.Sprintf("https://www.google.com/maps/place/%s/@%s,%s,17z", url.QueryEscape(*r.Name), *r.Latitude, *r.Longitude)
				} else {
					openInURL = fmt.Sprintf("https://www.google.com/maps/@%s,%s,17z", *r.Latitude, *r.Longitude)
				}
			}
		case "apple_maps":
			if r.Latitude != nil && r.Longitude != nil && *r.Latitude != "" && *r.Longitude != "" {
				if r.Name != nil && *r.Name != "" {
					openInURL = fmt.Sprintf("https://maps.apple.com/?q=%s&ll=%s,%s", url.QueryEscape(*r.Name), *r.Latitude, *r.Longitude)
				} else {
					openInURL = fmt.Sprintf("https://maps.apple.com/?ll=%s,%s", *r.Latitude, *r.Longitude)
				}
			}
		}

		// Determine the argument (URL to open)

		item := AlfredItem{
			Title:    restaurantName,
			Subtitle: subtitle,
			Arg:      "",
			Valid:    true,
			Variables: map[string]interface{}{
				"restaurant_id":      r.ID,
				"restaurant_name":    restaurantName,
				"restaurant_url":     r.Url,
				"website_url":        r.WebsiteUrl,
				"search_query":       query,
				"mode":               "visited",
				"imageURL":           r.ImageURL,
				"myDescription":      r.Description,
				"restaurant_address": r.Address,
				"restaurant_award":   award,
				"OPEN_IN_URL":        openInURL,
			},
		}

		// Add icon based on award type
		if r.CurrentAward != nil {
			awardLower := strings.ToLower(*r.CurrentAward)
			if strings.Contains(awardLower, "bib gourmand") {
				item.Icon = map[string]string{
					"path": "icons/bibg.png",
				}
			} else if strings.Contains(awardLower, "selected restaurant") {
				item.Icon = map[string]string{
					"path": "icons/star.png",
				}
			}
		}

		items = append(items, item)
	}

	// Return results
	result := AlfredResult{Items: items}
	if err := printJSON(result); err != nil {
		showError(fmt.Sprintf("Error formatting results: %v", err))
	}
}

// handleToggleFavorite toggles a restaurant's favorite status
func handleToggleFavorite(database *sql.DB, id int64) {
	// Toggle favorite with timing
	timeQuery(fmt.Sprintf("toggle favorite id: %d", id), func() error {
		return db.ToggleFavorite(database, id)
	})

	// Get restaurant details with timing
	var restaurant db.Restaurant
	var err error

	timeQuery(fmt.Sprintf("get restaurant after toggle favorite id: %d", id), func() error {
		restaurant, err = db.GetRestaurantByID(database, id)
		return err
	})

	if err != nil {
		showError(fmt.Sprintf("Error getting restaurant: %v", err))
		return
	}

	if restaurant.IsFavorite {
		fmt.Println("Added to favorites ❤️")
	} else {
		fmt.Println("Removed from favorites 💔")
	}
}

// handleToggleVisited toggles a restaurant's visited status
func handleToggleVisited(database *sql.DB, id int64, date, notes string) {
	// Toggle visited with timing
	timeQuery(fmt.Sprintf("toggle visited id: %d", id), func() error {
		return db.ToggleVisited(database, id, date, notes)
	})

	// Get restaurant details with timing
	var restaurant db.Restaurant
	var err error

	timeQuery(fmt.Sprintf("get restaurant after toggle visited id: %d", id), func() error {
		restaurant, err = db.GetRestaurantByID(database, id)
		return err
	})

	if err != nil {
		showError(fmt.Sprintf("Error getting restaurant: %v", err))
		return
	}

	if restaurant.IsVisited {
		fmt.Println("Added to visited ✅")
	} else {
		fmt.Println("Removed from visited ❌")
	}
}

// handleFavorites shows all favorite restaurants
func handleFavorites(database *sql.DB) {
	// Get all favorite restaurants with timing
	var restaurants []db.Restaurant
	var err error

	timeQuery("get favorites", func() error {
		restaurants, err = db.GetFavoriteRestaurants(database)
		return err
	})

	if err != nil {
		showError(fmt.Sprintf("Error getting favorites: %v", err))
		return
	}

	// Check if no favorites found
	if len(restaurants) == 0 {
		showNoResults("No favorite restaurants found.")
		return
	}

	// Format results for Alfred
	items := make([]AlfredItem, 0, len(restaurants))
	totalCount := len(restaurants)

	for i, r := range restaurants {
		// Get cuisine or set default value
		cuisine := "Unknown cuisine"
		if r.Cuisine != nil && *r.Cuisine != "" {
			cuisine = *r.Cuisine
		}

		// Get location or set default value
		location := "Unknown location"
		if r.Location != nil && *r.Location != "" {
			location = *r.Location
		}

		// Get restaurant name
		restaurantName := "Unknown restaurant"
		if r.Name != nil && *r.Name != "" {
			restaurantName = *r.Name
		}

		// Add scroll emoji if restaurant is no longer in the guide
		if r.InGuide == 0 {
			restaurantName = restaurantName + " 📜"
		}

		// Add heart emoji to title (always for favorites list)
		restaurantName = restaurantName + " ❤️"

		// Add visited emoji to title if needed
		if r.IsVisited {
			restaurantName = restaurantName + " ✅"
		}

		// Format award display with stars and year
		award := formatAwardWithYearRange(r.CurrentAward, r.CurrentAwardYear, r.CurrentAwardLastYear, r.CurrentGreenStar, r.InGuide)

		// Create counter prefix
		counter := fmt.Sprintf("%s/%s", formatNumber(i+1), formatNumber(totalCount))

		// Determine the OPEN_IN_URL variable based on user preference
		openIn := os.Getenv("OPEN_IN")
		var openInURL string
		if openIn == "" {
			openIn = "restaurant" // default
		}
		switch openIn {
		case "restaurant":
			if r.WebsiteUrl != nil && *r.WebsiteUrl != "" {
				openInURL = *r.WebsiteUrl
			} else if r.Url != nil && *r.Url != "" {
				openInURL = *r.Url
			}
		case "michelin":
			if r.Url != nil && *r.Url != "" {
				openInURL = *r.Url
			}
		case "maps":
			if r.Longitude != nil && r.Latitude != nil && *r.Longitude != "" && *r.Latitude != "" {
				openInURL = fmt.Sprintf("https://www.google.com/maps/@%s,%s,17z", *r.Latitude, *r.Longitude)
			}
		case "apple_maps":
			if r.Latitude != nil && r.Longitude != nil && *r.Latitude != "" && *r.Longitude != "" {
				if r.Name != nil && *r.Name != "" {
					openInURL = fmt.Sprintf("https://maps.apple.com/?q=%s&ll=%s,%s", url.QueryEscape(*r.Name), *r.Latitude, *r.Longitude)
				} else {
					openInURL = fmt.Sprintf("https://maps.apple.com/?ll=%s,%s", *r.Latitude, *r.Longitude)
				}
			}
		}

		item := AlfredItem{
			Title: restaurantName,
			Subtitle: fmt.Sprintf("%s | %s | %s | %s",
				counter,
				location,
				award,
				cuisine),
			Arg:   "",
			Valid: true,
			Variables: map[string]interface{}{
				"restaurant_id":      r.ID,
				"restaurant_name":    restaurantName,
				"restaurant_url":     r.Url,
				"website_url":        r.WebsiteUrl,
				"mode":               "favorites",
				"imageURL":           r.ImageURL,
				"myDescription":      r.Description,
				"restaurant_address": r.Address,
				"restaurant_award":   award,
				"OPEN_IN_URL":        openInURL,
			},
		}

		// Add icon based on award type
		if r.CurrentAward != nil {
			awardLower := strings.ToLower(*r.CurrentAward)
			if strings.Contains(awardLower, "bib gourmand") {
				item.Icon = map[string]string{
					"path": "icons/bibg.png",
				}
			} else if strings.Contains(awardLower, "selected restaurant") {
				item.Icon = map[string]string{
					"path": "icons/star.png",
				}
			}
		}

		items = append(items, item)
	}

	// Return results
	result := AlfredResult{Items: items}
	if err := printJSON(result); err != nil {
		showError(fmt.Sprintf("Error formatting results: %v", err))
	}
}

// handleVisited shows all visited restaurants
func handleVisited(database *sql.DB) {
	// Get all visited restaurants with timing
	var restaurants []db.Restaurant
	var err error

	timeQuery("get visited", func() error {
		restaurants, err = db.GetVisitedRestaurants(database)
		return err
	})

	if err != nil {
		showError(fmt.Sprintf("Error getting visited restaurants: %v", err))
		return
	}

	// Check if no visited restaurants found
	if len(restaurants) == 0 {
		showNoResults("No visited restaurants found.")
		return
	}

	// Format results for Alfred
	items := make([]AlfredItem, 0, len(restaurants))
	totalCount := len(restaurants)

	for i, r := range restaurants {
		// Get cuisine or set default value
		cuisine := "Unknown cuisine"
		if r.Cuisine != nil && *r.Cuisine != "" {
			cuisine = *r.Cuisine
		}

		// Get location or set default value
		location := "Unknown location"
		if r.Location != nil && *r.Location != "" {
			location = *r.Location
		}

		// Get restaurant name
		restaurantName := "Unknown restaurant"
		if r.Name != nil && *r.Name != "" {
			restaurantName = *r.Name
		}

		// Add scroll emoji if restaurant is no longer in the guide
		if r.InGuide == 0 {
			restaurantName = restaurantName + " 📜"
		}

		// Add heart emoji to title for favorites
		if r.IsFavorite {
			restaurantName = restaurantName + " ❤️"
		}

		// Add visited emoji to title (always for visited list)
		restaurantName = restaurantName + " ✅"

		// Format award display with stars and year
		award := formatAwardWithYearRange(r.CurrentAward, r.CurrentAwardYear, r.CurrentAwardLastYear, r.CurrentGreenStar, r.InGuide)

		// Create counter prefix
		counter := fmt.Sprintf("%s/%s", formatNumber(i+1), formatNumber(totalCount))

		// Create Alfred item
		subtitle := fmt.Sprintf("%s | %s | %s | %s",
			counter,
			location,
			award,
			cuisine)

		// Add visit date at the end if available
		if r.VisitedDate != nil && *r.VisitedDate != "" {
			subtitle = fmt.Sprintf("%s | Visited: %s", subtitle, *r.VisitedDate)
		}

		// Determine the OPEN_IN_URL variable based on user preference
		openIn := os.Getenv("OPEN_IN")
		var openInURL string
		if openIn == "" {
			openIn = "restaurant" // default
		}
		switch openIn {
		case "restaurant":
			if r.WebsiteUrl != nil && *r.WebsiteUrl != "" {
				openInURL = *r.WebsiteUrl
			} else if r.Url != nil && *r.Url != "" {
				openInURL = *r.Url
			}
		case "michelin":
			if r.Url != nil && *r.Url != "" {
				openInURL = *r.Url
			}
		case "maps":
			if r.Longitude != nil && r.Latitude != nil && *r.Longitude != "" && *r.Latitude != "" {
				openInURL = fmt.Sprintf("https://www.google.com/maps/@%s,%s,17z", *r.Latitude, *r.Longitude)
			}
		case "apple_maps":
			if r.Latitude != nil && r.Longitude != nil && *r.Latitude != "" && *r.Longitude != "" {
				if r.Name != nil && *r.Name != "" {
					openInURL = fmt.Sprintf("https://maps.apple.com/?q=%s&ll=%s,%s", url.QueryEscape(*r.Name), *r.Latitude, *r.Longitude)
				} else {
					openInURL = fmt.Sprintf("https://maps.apple.com/?ll=%s,%s", *r.Latitude, *r.Longitude)
				}
			}
		}

		item := AlfredItem{
			Title:    restaurantName,
			Subtitle: subtitle,
			Arg:      "",
			Valid:    true,
			Variables: map[string]interface{}{
				"restaurant_id":      r.ID,
				"restaurant_name":    restaurantName,
				"restaurant_url":     r.Url,
				"website_url":        r.WebsiteUrl,
				"mode":               "visited",
				"imageURL":           r.ImageURL,
				"myDescription":      r.Description,
				"restaurant_address": r.Address,
				"restaurant_award":   award,
				"OPEN_IN_URL":        openInURL,
			},
		}

		// Add icon based on award type
		if r.CurrentAward != nil {
			awardLower := strings.ToLower(*r.CurrentAward)
			if strings.Contains(awardLower, "bib gourmand") {
				item.Icon = map[string]string{
					"path": "icons/bibg.png",
				}
			} else if strings.Contains(awardLower, "selected restaurant") {
				item.Icon = map[string]string{
					"path": "icons/star.png",
				}
			}
		}

		items = append(items, item)
	}

	// Return results
	result := AlfredResult{Items: items}
	if err := printJSON(result); err != nil {
		showError(fmt.Sprintf("Error formatting results: %v", err))
	}
}

// showError displays an error message in Alfred format
func showError(message string) {
	// Log error to stderr for Alfred debugger
	fmt.Fprintf(os.Stderr, "[ERROR] %s\n", message)

	items := []AlfredItem{
		{
			Title:    fmt.Sprintf("Error: %s", message),
			Subtitle: "Press Enter to try again",
			Valid:    false,
		},
	}
	result := AlfredResult{Items: items}
	printJSON(result)
}

// showNoResults displays a message when no results are found
func showNoResults(message string) {
	items := []AlfredItem{
		{
			Title:    message,
			Subtitle: "",
			Valid:    false,
		},
	}
	result := AlfredResult{Items: items}
	printJSON(result)
}

// handleAwardHistory shows the award history for a restaurant
func handleAwardHistory(database *sql.DB, id int64, searchQuery string) {
	// Get restaurant details with timing
	var restaurant db.Restaurant
	var err error

	timeQuery(fmt.Sprintf("get restaurant for award history id: %d", id), func() error {
		restaurant, err = db.GetRestaurantByID(database, id)
		return err
	})

	if err != nil {
		showError(fmt.Sprintf("Error getting restaurant: %v", err))
		return
	}

	// Get award history with timing
	var awards []db.RestaurantAward

	timeQuery(fmt.Sprintf("get award history id: %d", id), func() error {
		awards, err = db.GetRestaurantAwardHistory(database, id)
		return err
	})

	if err != nil {
		showError(fmt.Sprintf("Error getting award history: %v", err))
		return
	}

	// Check if no award history found
	if len(awards) == 0 {
		showNoResults("No award history found for this restaurant.")
		return
	}

	// Format results for Alfred - only award history items
	items := make([]AlfredItem, 0, len(awards))

	// Add award history items
	for i, award := range awards {
		counter := fmt.Sprintf("%s/%s", formatNumber(i+1), formatNumber(len(awards)))

		// Get mode from environment variable to preserve it
		mode := os.Getenv("mode")

		// Format award with stars and green star emoji
		formattedAward := formatAwardWithStarsAndGreenStar(&award.Distinction, &award.Year, award.GreenStar)

		item := AlfredItem{
			Title:    fmt.Sprintf("%d: %s", award.Year, formattedAward),
			Subtitle: fmt.Sprintf("%s | Year %d ", counter, award.Year),
			Arg:      "",
			Valid:    true,
			Variables: map[string]interface{}{
				"restaurant_id": restaurant.ID,
				"search_query":  searchQuery,
				"mode":          mode,
			},
		}

		// Add icon based on award type
		awardLower := strings.ToLower(award.Distinction)
		if strings.Contains(awardLower, "bib gourmand") {
			item.Icon = map[string]string{
				"path": "icons/bibg.png",
			}
		} else if strings.Contains(awardLower, "selected restaurant") {
			item.Icon = map[string]string{
				"path": "icons/star.png",
			}
		}

		items = append(items, item)
	}

	// Return results
	result := AlfredResult{Items: items}
	if err := printJSON(result); err != nil {
		showError(fmt.Sprintf("Error formatting results: %v", err))
	}
}

// formatNumber formats a number with thousand separators (commas)
func formatNumber(n int) string {
	str := strconv.Itoa(n)
	if len(str) <= 3 {
		return str
	}

	// Add commas from right to left
	result := ""
	for i, digit := range str {
		if i > 0 && (len(str)-i)%3 == 0 {
			result += ","
		}
		result += string(digit)
	}
	return result
}

// formatAwardWithStarsAndGreenStar formats award display with stars replacing text, year in parentheses, and green star emoji
func formatAwardWithStarsAndGreenStar(award *string, year *int, greenStar *bool) string {
	if award == nil || *award == "" {
		return "No Michelin distinction"
	}

	awardStr := strings.ToLower(*award)
	formattedAward := ""

	if strings.Contains(awardStr, "3 star") {
		formattedAward = "⭐️⭐️⭐️"
	} else if strings.Contains(awardStr, "2 star") {
		formattedAward = "⭐️⭐️"
	} else if strings.Contains(awardStr, "1 star") {
		formattedAward = "⭐️"
	} else if strings.Contains(awardStr, "bib gourmand") {
		formattedAward = "Bib Gourmand"
	} else if strings.Contains(awardStr, "selected restaurant") {
		formattedAward = "Selected Restaurants"
	} else {
		formattedAward = *award
	}

	// Add green star emoji if present
	if greenStar != nil && *greenStar {
		formattedAward += " 🍀"
	}

	// Add year if available
	if year != nil && *year > 0 {
		formattedAward += fmt.Sprintf(" (%d)", *year)
	}

	return formattedAward
}

// formatAwardWithYearRange formats award display with proper year information based on guide status
func formatAwardWithYearRange(award *string, firstYear *int, lastYear *int, greenStar *bool, inGuide int) string {
	if award == nil || *award == "" {
		return "No Michelin distinction"
	}

	awardStr := strings.ToLower(*award)
	formattedAward := ""

	if strings.Contains(awardStr, "3 star") {
		formattedAward = "⭐️⭐️⭐️"
	} else if strings.Contains(awardStr, "2 star") {
		formattedAward = "⭐️⭐️"
	} else if strings.Contains(awardStr, "1 star") {
		formattedAward = "⭐️"
	} else if strings.Contains(awardStr, "bib gourmand") {
		formattedAward = "Bib Gourmand"
	} else if strings.Contains(awardStr, "selected restaurant") {
		formattedAward = "Selected Restaurants"
	} else {
		formattedAward = *award
	}

	// Add green star emoji if present
	if greenStar != nil && *greenStar {
		formattedAward += " 🍀"
	}

	// Add year information based on guide status
	if firstYear != nil && *firstYear > 0 {
		if inGuide == 0 && lastYear != nil && *lastYear > 0 && *lastYear != *firstYear {
			// Restaurant no longer in guide and has different last year - show range
			formattedAward += fmt.Sprintf(" (%d-%d)", *firstYear, *lastYear)
		} else {
			// Restaurant still in guide or same first/last year - show single year
			formattedAward += fmt.Sprintf(" (%d)", *firstYear)
		}
	}

	return formattedAward
}

// handleShowDescription handles the showDescription command
func handleShowDescription(workDir string) {
	// Step 1: Retrieve environmental variables
	myDescription := os.Getenv("myDescription")
	imageURL := os.Getenv("imageURL")
	restaurantAddress := os.Getenv("restaurant_address")
	restaurantAward := os.Getenv("restaurant_award")

	// Step 2: Handle null/empty values
	if myDescription == "" {
		myDescription = "No description available"
	}
	if restaurantAddress == "" {
		restaurantAddress = "No address available"
	}
	if restaurantAward == "" {
		restaurantAward = "No award information available"
	}

	if imageURL == "" {
		// If no image URL, create JSON with "No image available" message
		restaurantName := os.Getenv("restaurant_name")
		restaurantID := os.Getenv("restaurant_id")

		// Get the restaurant URLs from environment variables
		restaurantURL := os.Getenv("restaurant_url")
		websiteURL := os.Getenv("website_url")

		// Create URL links if available
		urlLinks := ""
		if websiteURL != "" || restaurantURL != "" {
			urlLinks = "\n\n"
			if websiteURL != "" {
				urlLinks += fmt.Sprintf("🔗 %s", websiteURL)
			}
			if restaurantURL != "" {
				if websiteURL != "" {
					urlLinks += "\n\n"
				}
				urlLinks += fmt.Sprintf("🌟 %s", restaurantURL)
			}
		}

		// Build the response content
		responseContent := fmt.Sprintf("%s\n\nNo image available\n\n🏆 %s\n\n%s\n\n📍 %s",
			restaurantName, restaurantAward, myDescription, restaurantAddress)

		if urlLinks != "" {
			responseContent += urlLinks
		}

		// Create the response struct
		response := DescriptionResponse{
			Variables: make(map[string]interface{}),
			Response:  responseContent,
			Footer:    restaurantID,
		}
		response.Behaviour.Response = "append"
		response.Behaviour.Scroll = "end"
		response.Behaviour.Inputfield = "select"

		// Marshal to JSON
		jsonBytes, err := json.MarshalIndent(response, "", "  ")
		if err != nil {
			fmt.Fprintf(os.Stderr, "[ERROR] Failed to marshal JSON: %v\n", err)
			showError(fmt.Sprintf("Failed to marshal JSON: %v", err))
			return
		}

		// Output the JSON
		fmt.Println(string(jsonBytes))
		return
	}

	// Step 3: Extract filename from imageURL and check if it exists
	filename := extractFilenameFromURL(imageURL)
	if filename == "" {
		showError("Could not extract filename from image URL")
		return
	}

	// Create images folder if it doesn't exist
	imagesDir := filepath.Join(workDir, "images")
	if err := os.MkdirAll(imagesDir, 0755); err != nil {
		fmt.Fprintf(os.Stderr, "[ERROR] Failed to create images directory: %v\n", err)
		showError(fmt.Sprintf("Failed to create images directory: %v", err))
		return
	}

	// Step 4: Check if file exists
	imagePath := filepath.Join(imagesDir, filename)
	fileExists := false
	if _, err := os.Stat(imagePath); err == nil {
		fileExists = true
	}

	// Step 5: If file doesn't exist, fetch and save it
	if !fileExists {
		if err := downloadImage(imageURL, imagePath); err != nil {
			fmt.Fprintf(os.Stderr, "[ERROR] Failed to download image: %v\n", err)
			// If download fails, still show description with "No image available" message
			restaurantName := os.Getenv("restaurant_name")
			restaurantID := os.Getenv("restaurant_id")

			// Get the restaurant URLs from environment variables
			restaurantURL := os.Getenv("restaurant_url")
			websiteURL := os.Getenv("website_url")

			// Create URL links if available
			urlLinks := ""
			if websiteURL != "" || restaurantURL != "" {
				urlLinks = "\n\n"
				if websiteURL != "" {
					urlLinks += fmt.Sprintf("🔗 %s", websiteURL)
				}
				if restaurantURL != "" {
					if websiteURL != "" {
						urlLinks += "\n\n"
					}
					urlLinks += fmt.Sprintf("🌟 %s", restaurantURL)
				}
			}

			// Build the response content
			responseContent := fmt.Sprintf("%s\n\nNo image available\n\n🏆 %s\n\n%s\n\n📍 %s",
				restaurantName, restaurantAward, myDescription, restaurantAddress)

			if urlLinks != "" {
				responseContent += urlLinks
			}

			// Create the response struct
			response := DescriptionResponse{
				Variables: make(map[string]interface{}),
				Response:  responseContent,
				Footer:    restaurantID,
			}
			response.Behaviour.Response = "append"
			response.Behaviour.Scroll = "end"
			response.Behaviour.Inputfield = "select"

			// Marshal to JSON
			jsonBytes, err := json.MarshalIndent(response, "", "  ")
			if err != nil {
				fmt.Fprintf(os.Stderr, "[ERROR] Failed to marshal JSON: %v\n", err)
				showError(fmt.Sprintf("Failed to marshal JSON: %v", err))
				return
			}

			// Output the JSON
			fmt.Println(string(jsonBytes))
			return
		}
	}

	// Step 6: Create and output JSON using proper marshaling
	restaurantName := os.Getenv("restaurant_name")
	restaurantID := os.Getenv("restaurant_id")

	// Get the restaurant URLs from environment variables
	restaurantURL := os.Getenv("restaurant_url")
	websiteURL := os.Getenv("website_url")

	// Create URL links if available
	urlLinks := ""
	if websiteURL != "" || restaurantURL != "" {
		urlLinks = "\n\n"
		if websiteURL != "" {
			urlLinks += fmt.Sprintf("🔗 %s", websiteURL)
		}
		if restaurantURL != "" {
			if websiteURL != "" {
				urlLinks += "\n\n"
			}
			urlLinks += fmt.Sprintf("🌟 %s", restaurantURL)
		}
	}

	// Build the response content
	responseContent := fmt.Sprintf("%s\n\n![](%s)\n\n🏆 %s\n\n%s\n\n📍 %s",
		restaurantName, imagePath, restaurantAward, myDescription, restaurantAddress)

	if urlLinks != "" {
		responseContent += urlLinks
	}

	// Create the response struct
	response := DescriptionResponse{
		Variables: make(map[string]interface{}),
		Response:  responseContent,
		Footer:    restaurantID,
	}
	response.Behaviour.Response = "append"
	response.Behaviour.Scroll = "end"
	response.Behaviour.Inputfield = "select"

	// Marshal to JSON
	jsonBytes, err := json.MarshalIndent(response, "", "  ")
	if err != nil {
		fmt.Fprintf(os.Stderr, "[ERROR] Failed to marshal JSON: %v\n", err)
		showError(fmt.Sprintf("Failed to marshal JSON: %v", err))
		return
	}

	// Output the JSON
	fmt.Println(string(jsonBytes))
}

// extractFilenameFromURL extracts the filename from a URL
func extractFilenameFromURL(url string) string {
	// Split by '/' and get the last part
	parts := strings.Split(url, "/")
	if len(parts) == 0 {
		return ""
	}

	filename := parts[len(parts)-1]

	// Remove query parameters if present
	if idx := strings.Index(filename, "?"); idx != -1 {
		filename = filename[:idx]
	}

	return filename
}

// downloadImage downloads an image from a URL and saves it to the specified path
func downloadImage(url, filepath string) error {
	// Create HTTP client with timeout
	client := &http.Client{
		Timeout: 30 * time.Second,
	}

	// Make HTTP request
	resp, err := client.Get(url)
	if err != nil {
		return fmt.Errorf("failed to make HTTP request: %v", err)
	}
	defer resp.Body.Close()

	// Check if response is successful
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("HTTP request failed with status: %d", resp.StatusCode)
	}

	// Create the file
	file, err := os.Create(filepath)
	if err != nil {
		return fmt.Errorf("failed to create file: %v", err)
	}
	defer file.Close()

	// Copy the response body to the file
	_, err = io.Copy(file, resp.Body)
	if err != nil {
		return fmt.Errorf("failed to copy response body to file: %v", err)
	}

	return nil
}

// printJSON marshals and prints JSON to stdout
func printJSON(result AlfredResult) error {
	jsonBytes, err := json.MarshalIndent(result, "", "  ")
	if err != nil {
		return err
	}
	fmt.Println(string(jsonBytes))
	return nil
}

// extractZipFile extracts a zip file containing a database to the specified path
func extractZipFile(zipPath, extractPath string) error {
	// Open zip file
	r, err := zip.OpenReader(zipPath)
	if err != nil {
		return err
	}
	defer r.Close()

	// Find the database file in the zip
	var dbFile *zip.File
	for _, f := range r.File {
		if strings.HasSuffix(f.Name, ".db") {
			dbFile = f
			break
		}
	}

	if dbFile == nil {
		return fmt.Errorf("no .db file found in zip archive")
	}

	// Extract the database file
	rc, err := dbFile.Open()
	if err != nil {
		return err
	}
	defer rc.Close()

	outFile, err := os.OpenFile(extractPath, os.O_WRONLY|os.O_CREATE|os.O_TRUNC, dbFile.Mode())
	if err != nil {
		return err
	}
	defer outFile.Close()

	_, err = io.Copy(outFile, rc)
	return err
}

// copyFile copies a file from src to dst
func copyFile(src, dst string) error {
	sourceFile, err := os.Open(src)
	if err != nil {
		return err
	}
	defer sourceFile.Close()

	destFile, err := os.Create(dst)
	if err != nil {
		return err
	}
	defer destFile.Close()

	_, err = io.Copy(destFile, sourceFile)
	return err
}

// preserveUserData copies user-generated tables from old database to new database
func preserveUserData(oldDbPath, newDbPath string) error {
	// Open old database
	oldDb, err := sql.Open("sqlite3", oldDbPath)
	if err != nil {
		return fmt.Errorf("failed to open old database: %v", err)
	}
	defer oldDb.Close()

	// Open new database
	newDb, err := sql.Open("sqlite3", newDbPath)
	if err != nil {
		return fmt.Errorf("failed to open new database: %v", err)
	}
	defer newDb.Close()

	// Check if user tables exist in old database
	var userFavoritesExist, userVisitsExist bool

	// Check for user_favorites table
	rows, err := oldDb.Query("SELECT name FROM sqlite_master WHERE type='table' AND name='user_favorites'")
	if err != nil {
		return fmt.Errorf("failed to check for user_favorites table: %v", err)
	}
	userFavoritesExist = rows.Next()
	rows.Close()

	// Check for user_visits table
	rows, err = oldDb.Query("SELECT name FROM sqlite_master WHERE type='table' AND name='user_visits'")
	if err != nil {
		return fmt.Errorf("failed to check for user_visits table: %v", err)
	}
	userVisitsExist = rows.Next()
	rows.Close()

	if !userFavoritesExist && !userVisitsExist {
		// No user data to preserve
		return nil
	}

	// Create user tables in new database if they don't exist
	_, err = newDb.Exec(`
		CREATE TABLE IF NOT EXISTS user_favorites (
			id INTEGER PRIMARY KEY AUTOINCREMENT,
			restaurant_id INTEGER NOT NULL,
			created_at DATETIME DEFAULT CURRENT_TIMESTAMP,
			FOREIGN KEY (restaurant_id) REFERENCES restaurants(id),
			UNIQUE (restaurant_id)
		);
		
		CREATE TABLE IF NOT EXISTS user_visits (
			id INTEGER PRIMARY KEY AUTOINCREMENT,
			restaurant_id INTEGER NOT NULL,
			visited_date TEXT,
			notes TEXT,
			created_at DATETIME DEFAULT CURRENT_TIMESTAMP,
			FOREIGN KEY (restaurant_id) REFERENCES restaurants(id),
			UNIQUE (restaurant_id)
		);
		
		CREATE INDEX IF NOT EXISTS idx_user_favorites_restaurant ON user_favorites(restaurant_id);
		CREATE INDEX IF NOT EXISTS idx_user_visits_restaurant ON user_visits(restaurant_id);
	`)
	if err != nil {
		return fmt.Errorf("failed to create user tables in new database: %v", err)
	}

	// Copy user_favorites if it exists
	if userFavoritesExist {
		rows, err := oldDb.Query("SELECT restaurant_id, created_at FROM user_favorites")
		if err != nil {
			return fmt.Errorf("failed to query user_favorites from old database: %v", err)
		}
		defer rows.Close()

		for rows.Next() {
			var restaurantID int64
			var createdAt string
			if err := rows.Scan(&restaurantID, &createdAt); err != nil {
				return fmt.Errorf("failed to scan user_favorites row: %v", err)
			}

			// Insert into new database
			_, err := newDb.Exec("INSERT OR IGNORE INTO user_favorites (restaurant_id, created_at) VALUES (?, ?)", restaurantID, createdAt)
			if err != nil {
				return fmt.Errorf("failed to insert user_favorites into new database: %v", err)
			}
		}
		fmt.Fprintf(os.Stderr, "[DEBUG] Copied user_favorites table\n")
	}

	// Copy user_visits if it exists
	if userVisitsExist {
		rows, err := oldDb.Query("SELECT restaurant_id, visited_date, notes, created_at FROM user_visits")
		if err != nil {
			return fmt.Errorf("failed to query user_visits from old database: %v", err)
		}
		defer rows.Close()

		for rows.Next() {
			var restaurantID int64
			var visitedDate, notes, createdAt sql.NullString
			if err := rows.Scan(&restaurantID, &visitedDate, &notes, &createdAt); err != nil {
				return fmt.Errorf("failed to scan user_visits row: %v", err)
			}

			// Insert into new database
			_, err := newDb.Exec("INSERT OR IGNORE INTO user_visits (restaurant_id, visited_date, notes, created_at) VALUES (?, ?, ?, ?)",
				restaurantID, visitedDate, notes, createdAt)
			if err != nil {
				return fmt.Errorf("failed to insert user_visits into new database: %v", err)
			}
		}
		fmt.Fprintf(os.Stderr, "[DEBUG] Copied user_visits table\n")
	}

	return nil
}

// DescriptionResponse represents the JSON response for showDescription
type DescriptionResponse struct {
	Variables map[string]interface{} `json:"variables"`
	Response  string                 `json:"response"`
	Footer    string                 `json:"footer"`
	Behaviour struct {
		Response   string `json:"response"`
		Scroll     string `json:"scroll"`
		Inputfield string `json:"inputfield"`
	} `json:"behaviour"`
}
