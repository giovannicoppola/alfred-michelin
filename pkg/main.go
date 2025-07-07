package main

import (
	"database/sql"
	"encoding/json"
	"fmt"
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

// Alfred item structure for results
type AlfredItem struct {
	UID          string                 `json:"uid,omitempty"`
	Title        string                 `json:"title"`
	Subtitle     string                 `json:"subtitle,omitempty"`
	Arg          string                 `json:"arg,omitempty"`
	Icon         map[string]string      `json:"icon,omitempty"`
	Valid        bool                   `json:"valid"`
	Autocomplete string                 `json:"autocomplete,omitempty"`
	Variables    map[string]interface{} `json:"variables,omitempty"`
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

// Main function
func main() {
	// Check command-line arguments
	if len(os.Args) < 2 {
		fmt.Println("Usage: alfred-michelin [command] [arguments]")
		os.Exit(1)
	}

	// Get workflow directory (where the executable is)
	workDir, err := getWorkingDirectory()
	if err != nil {
		fmt.Printf("Error: %v\n", err)
		os.Exit(1)
	}

	// Initialize database directly in the workflow directory
	dbPath := filepath.Join(workDir, db.DbFileName)
	database, err := db.Initialize(dbPath)
	if err != nil {
		fmt.Printf("Error initializing database: %v\n", err)
		os.Exit(1)
	}
	defer database.Close()

	// Skip CSV import for new database - data should already exist
	// The new database comes pre-populated with restaurant data

	// Process commands
	command := os.Args[1]
	switch command {
	case "search":
		var query string

		// Debug output to stderr
		fmt.Fprintf(os.Stderr, "[DEBUG] Search command called\n")
		fmt.Fprintf(os.Stderr, "[DEBUG] Command args: %v\n", os.Args)
		fmt.Fprintf(os.Stderr, "[DEBUG] search_query env var: '%s'\n", os.Getenv("search_query"))

		// Check if there's a saved search query in environment variable
		if envQuery := os.Getenv("search_query"); envQuery != "" {
			query = envQuery
			fmt.Fprintf(os.Stderr, "[DEBUG] Using environment query: '%s'\n", query)
		} else if len(os.Args) >= 3 {
			query = os.Args[2]
			fmt.Fprintf(os.Stderr, "[DEBUG] Using command line query: '%s'\n", query)
		} else {
			fmt.Fprintf(os.Stderr, "[DEBUG] No query available, showing no results\n")
			showNoResults("Enter a search term...")
			return
		}

		handleSearch(database, query)

	case "get":
		if len(os.Args) < 3 {
			showError("Missing restaurant ID")
			return
		}
		id, err := strconv.ParseInt(os.Args[2], 10, 64)
		if err != nil {
			showError("Invalid restaurant ID")
			return
		}
		handleGetRestaurant(database, id)

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
		handleFavorites(database)

	case "visited":
		handleVisited(database)

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

		// Debug output to stderr
		fmt.Fprintf(os.Stderr, "[DEBUG] Award history command called\n")
		fmt.Fprintf(os.Stderr, "[DEBUG] Restaurant ID: %d\n", id)
		fmt.Fprintf(os.Stderr, "[DEBUG] search_query env var before: '%s'\n", os.Getenv("search_query"))
		fmt.Fprintf(os.Stderr, "[DEBUG] Final search query for award history: '%s'\n", searchQuery)

		// Set the search query environment variable if provided
		if searchQuery != "" {
			os.Setenv("search_query", searchQuery)
			fmt.Fprintf(os.Stderr, "[DEBUG] Set search_query env var to: '%s'\n", searchQuery)
		}

		handleAwardHistory(database, id, searchQuery)

	case "back":
		// Go back to search results - the search command will pick up the environment variable
		var query string

		// Debug output to stderr
		fmt.Fprintf(os.Stderr, "[DEBUG] Back command called\n")
		fmt.Fprintf(os.Stderr, "[DEBUG] Command args: %v\n", os.Args)
		fmt.Fprintf(os.Stderr, "[DEBUG] search_query env var: '%s'\n", os.Getenv("search_query"))

		// Get query from environment variable
		if envQuery := os.Getenv("search_query"); envQuery != "" {
			query = envQuery
			fmt.Fprintf(os.Stderr, "[DEBUG] Using environment query for back: '%s'\n", query)
		} else {
			fmt.Fprintf(os.Stderr, "[DEBUG] No search query available for back command\n")
			showError("No search query available. Please perform a new search.")
			return
		}

		// Use the search query to recreate the original search
		handleSearch(database, query)

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

	timeQuery(fmt.Sprintf("search query: '%s'", query), func() error {
		restaurants, err = db.SearchRestaurants(database, query)
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

		// Add heart emoji to title for favorites
		if r.IsFavorite {
			restaurantName = restaurantName + " ❤️"
		}

		// Add visited emoji to title if needed
		if r.IsVisited {
			restaurantName = restaurantName + " ✅"
		}

		// Format award display with stars and year
		award := formatAwardWithStarsAndYear(r.CurrentAward, r.CurrentAwardYear)

		// Create counter prefix
		counter := fmt.Sprintf("%s/%s", formatNumber(i+1), formatNumber(totalCount))

		// Get the appropriate URL based on OPEN_IN preference
		openInURL := getOpenInURL(r)

		item := AlfredItem{
			UID:   fmt.Sprintf("restaurant-%d", r.ID),
			Title: restaurantName,
			Subtitle: fmt.Sprintf("%s | %s | %s | %s",
				counter,
				location,
				award,
				cuisine),
			Arg:   fmt.Sprintf("%d", r.ID),
			Valid: true,
			Variables: map[string]interface{}{
				"restaurant_id":   r.ID,
				"restaurant_name": restaurantName,
				"is_favorite":     r.IsFavorite,
				"is_visited":      r.IsVisited,
				"OPEN_IN":         openInURL,
			},
		}

		// Add icon for bib gourmand
		if r.CurrentAward != nil && strings.Contains(strings.ToLower(*r.CurrentAward), "bib gourmand") {
			item.Icon = map[string]string{
				"path": "../source/icons/bibg.png",
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

// handleGetRestaurant gets details for a specific restaurant
func handleGetRestaurant(database *sql.DB, id int64) {
	// Get restaurant by ID with timing
	var restaurant db.Restaurant
	var err error

	timeQuery(fmt.Sprintf("get restaurant id: %d", id), func() error {
		restaurant, err = db.GetRestaurantByID(database, id)
		return err
	})

	if err != nil {
		showError(fmt.Sprintf("Error getting restaurant: %v", err))
		return
	}

	// Get cuisine or set default value
	cuisine := "Unknown cuisine"
	if restaurant.Cuisine != nil && *restaurant.Cuisine != "" {
		cuisine = *restaurant.Cuisine
	}

	// Get location or set default value
	location := "Unknown location"
	if restaurant.Location != nil && *restaurant.Location != "" {
		location = *restaurant.Location
	}

	// Get restaurant name
	restaurantName := "Unknown restaurant"
	if restaurant.Name != nil && *restaurant.Name != "" {
		restaurantName = *restaurant.Name
	}

	// Format award display with stars and year
	award := formatAwardWithStarsAndYear(restaurant.CurrentAward, restaurant.CurrentAwardYear)

	// Create items for different actions
	items := []AlfredItem{
		{
			Title:    restaurantName,
			Subtitle: fmt.Sprintf("%s | %s | %s", location, award, cuisine),
			Arg:      fmt.Sprintf("%d", restaurant.ID),
			Valid:    false,
		},
	}

	// Add icon for bib gourmand
	if restaurant.CurrentAward != nil && strings.Contains(strings.ToLower(*restaurant.CurrentAward), "bib gourmand") {
		items[0].Icon = map[string]string{
			"path": "../source/icons/bibg.png",
		}
	}

	// Add website item if available
	if restaurant.WebsiteUrl != nil && *restaurant.WebsiteUrl != "" {
		items = append(items, AlfredItem{
			Title:    "🌐 Open Website",
			Subtitle: *restaurant.WebsiteUrl,
			Arg:      *restaurant.WebsiteUrl,
			Valid:    true,
		})
	}

	// Add Michelin Guide item if available
	if restaurant.Url != nil && *restaurant.Url != "" {
		items = append(items, AlfredItem{
			Title:    "🔍 View on Michelin Guide",
			Subtitle: *restaurant.Url,
			Arg:      *restaurant.Url,
			Valid:    true,
		})
	}

	// Add maps item if coordinates are available
	hasLatLng := restaurant.Latitude != nil && restaurant.Longitude != nil &&
		*restaurant.Latitude != "" && *restaurant.Longitude != ""

	address := "Unknown address"
	if restaurant.Address != nil && *restaurant.Address != "" {
		address = *restaurant.Address
	}

	if hasLatLng {
		// Use restaurant name with coordinates for better identification
		mapQuery := strings.ReplaceAll(restaurantName, " ", "+")
		items = append(items, AlfredItem{
			Title:    "📍 View on Google Maps",
			Subtitle: address,
			Arg:      fmt.Sprintf("https://www.google.com/maps?q=%s&ll=%s,%s", mapQuery, *restaurant.Latitude, *restaurant.Longitude),
			Valid:    true,
		})
	}

	// Add toggle favorite action
	favoriteTitle := "❤️ Add to Favorites"
	if restaurant.IsFavorite {
		favoriteTitle = "💔 Remove from Favorites"
	}
	items = append(items, AlfredItem{
		Title:    favoriteTitle,
		Subtitle: "Toggle favorite status",
		Arg:      fmt.Sprintf("%d", restaurant.ID),
		Valid:    true,
		Variables: map[string]interface{}{
			"action": "toggle-favorite",
		},
	})

	// Add toggle visited action
	visitedTitle := "✅ Mark as Visited"
	if restaurant.IsVisited {
		visitedTitle = "❌ Mark as Not Visited"
	}
	items = append(items, AlfredItem{
		Title:    visitedTitle,
		Subtitle: "Toggle visited status",
		Arg:      fmt.Sprintf("%d", restaurant.ID),
		Valid:    true,
		Variables: map[string]interface{}{
			"action": "toggle-visited",
		},
	})

	// Add award history action
	items = append(items, AlfredItem{
		Title:    "🏆 View Award History",
		Subtitle: "Show award history for this restaurant (SHIFT modifier)",
		Arg:      fmt.Sprintf("%d", restaurant.ID),
		Valid:    true,
		Variables: map[string]interface{}{
			"action": "award-history",
		},
	})

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

		// Add heart emoji to title (always for favorites list)
		restaurantName = restaurantName + " ❤️"

		// Add visited emoji to title if needed
		if r.IsVisited {
			restaurantName = restaurantName + " ✅"
		}

		// Format award display with stars and year
		award := formatAwardWithStarsAndYear(r.CurrentAward, r.CurrentAwardYear)

		// Create counter prefix
		counter := fmt.Sprintf("%s/%s", formatNumber(i+1), formatNumber(totalCount))

		// Get the appropriate URL based on OPEN_IN preference
		openInURL := getOpenInURL(r)

		// Create Alfred item
		item := AlfredItem{
			UID:   fmt.Sprintf("restaurant-%d", r.ID),
			Title: restaurantName,
			Subtitle: fmt.Sprintf("%s | %s | %s | %s",
				counter,
				location,
				award,
				cuisine),
			Arg:   fmt.Sprintf("%d", r.ID),
			Valid: true,
			Variables: map[string]interface{}{
				"restaurant_id":   r.ID,
				"restaurant_name": restaurantName,
				"OPEN_IN":         openInURL,
			},
		}

		// Add icon for bib gourmand
		if r.CurrentAward != nil && strings.Contains(strings.ToLower(*r.CurrentAward), "bib gourmand") {
			item.Icon = map[string]string{
				"path": "../source/icons/bibg.png",
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

		// Add heart emoji to title for favorites
		if r.IsFavorite {
			restaurantName = restaurantName + " ❤️"
		}

		// Add visited emoji to title (always for visited list)
		restaurantName = restaurantName + " ✅"

		// Format award display with stars and year
		award := formatAwardWithStarsAndYear(r.CurrentAward, r.CurrentAwardYear)

		// Create counter prefix
		counter := fmt.Sprintf("%s/%s", formatNumber(i+1), formatNumber(totalCount))

		// Get the appropriate URL based on OPEN_IN preference
		openInURL := getOpenInURL(r)

		// Create Alfred item
		subtitle := fmt.Sprintf("%s | %s | %s | %s",
			counter,
			location,
			award,
			cuisine)

		// Add visit date if available
		if r.VisitedDate != nil && *r.VisitedDate != "" {
			subtitle = fmt.Sprintf("Visited: %s | %s", *r.VisitedDate, subtitle)
		}

		item := AlfredItem{
			UID:      fmt.Sprintf("restaurant-%d", r.ID),
			Title:    restaurantName,
			Subtitle: subtitle,
			Arg:      fmt.Sprintf("%d", r.ID),
			Valid:    true,
			Variables: map[string]interface{}{
				"restaurant_id":   r.ID,
				"restaurant_name": restaurantName,
				"OPEN_IN":         openInURL,
			},
		}

		// Add icon for bib gourmand
		if r.CurrentAward != nil && strings.Contains(strings.ToLower(*r.CurrentAward), "bib gourmand") {
			item.Icon = map[string]string{
				"path": "../source/icons/bibg.png",
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

// getOpenInURL returns the appropriate URL based on OPEN_IN preference
func getOpenInURL(restaurant db.Restaurant) string {
	openIn := os.Getenv("OPEN_IN")
	if openIn == "" {
		openIn = "restaurant" // Default to restaurant website
	}

	switch openIn {
	case "restaurant":
		if restaurant.WebsiteUrl != nil && *restaurant.WebsiteUrl != "" {
			return *restaurant.WebsiteUrl
		}
	case "michelin":
		if restaurant.Url != nil && *restaurant.Url != "" {
			return *restaurant.Url
		}
	case "maps":
		if restaurant.Latitude != nil && restaurant.Longitude != nil &&
			*restaurant.Latitude != "" && *restaurant.Longitude != "" {
			// Use restaurant name with coordinates for better identification
			restaurantName := "Restaurant"
			if restaurant.Name != nil && *restaurant.Name != "" {
				restaurantName = *restaurant.Name
			}
			return fmt.Sprintf("https://www.google.com/maps?q=%s&ll=%s,%s",
				strings.ReplaceAll(restaurantName, " ", "+"),
				*restaurant.Latitude, *restaurant.Longitude)
		}
	case "apple_maps":
		if restaurant.Latitude != nil && restaurant.Longitude != nil &&
			*restaurant.Latitude != "" && *restaurant.Longitude != "" {
			// Use restaurant name as query with coordinates for better identification
			restaurantName := "Restaurant"
			if restaurant.Name != nil && *restaurant.Name != "" {
				restaurantName = *restaurant.Name
			}
			return fmt.Sprintf("maps://?q=%s&ll=%s,%s",
				strings.ReplaceAll(restaurantName, " ", "+"),
				*restaurant.Latitude, *restaurant.Longitude)
		}
	}

	// Fallback to restaurant website if preferred option not available
	if restaurant.WebsiteUrl != nil && *restaurant.WebsiteUrl != "" {
		return *restaurant.WebsiteUrl
	}

	// Fallback to Michelin Guide if no website
	if restaurant.Url != nil && *restaurant.Url != "" {
		return *restaurant.Url
	}

	return ""
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
		item := AlfredItem{
			Title:    fmt.Sprintf("%d: %s", award.Year, award.Distinction),
			Subtitle: fmt.Sprintf("%s | Year %d | CMD+ALT to go back", counter, award.Year),
			Arg:      fmt.Sprintf("%d", restaurant.ID),
			Valid:    true,
			Variables: map[string]interface{}{
				"restaurant_id": restaurant.ID,
				"search_query":  searchQuery,
			},
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

// formatAwardWithStarsAndYear formats award display with stars replacing text and year in parentheses
func formatAwardWithStarsAndYear(award *string, year *int) string {
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
	} else if strings.Contains(awardStr, "green star") {
		formattedAward = "🌿 Green Star"
	} else if strings.Contains(awardStr, "selected restaurant") {
		formattedAward = "Selected Restaurants"
	} else {
		formattedAward = *award
	}

	// Add year if available
	if year != nil && *year > 0 {
		formattedAward += fmt.Sprintf(" (%d)", *year)
	}

	return formattedAward
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
