package db

import (
	"database/sql"
	"fmt"
	"strings"
	"unicode"

	"archive/zip"
	"io"
	"os"
	"path/filepath"

	_ "github.com/mattn/go-sqlite3"
	"golang.org/x/text/runes"
	"golang.org/x/text/transform"
	"golang.org/x/text/unicode/norm"
)

const (
	DbFileName = "michelin new.db"
)

// NoUpdateAvailableError indicates that no database update file was found
type NoUpdateAvailableError struct {
	path string
}

func (e *NoUpdateAvailableError) Error() string {
	return fmt.Sprintf("no database update file found at %s", e.path)
}

// IsNoUpdateAvailable checks if an error is a NoUpdateAvailableError
func IsNoUpdateAvailable(err error) bool {
	_, ok := err.(*NoUpdateAvailableError)
	return ok
}

// normalizeForSearch removes accents and converts to lowercase for search comparison
func normalizeForSearch(text string) string {
	// First normalize to NFD (decomposed form)
	t := transform.Chain(norm.NFD, runes.Remove(runes.In(unicode.Mn)), norm.NFC)
	normalized, _, _ := transform.String(t, text)
	return strings.ToLower(normalized)
}

// MigrateNormalizedColumns adds normalized columns for accent-insensitive search
func MigrateNormalizedColumns(db *sql.DB) error {
	// Check if normalized columns already exist
	var hasNormalized bool

	// Check if name_normalized column exists
	rows, err := db.Query("PRAGMA table_info(restaurants)")
	if err != nil {
		return fmt.Errorf("failed to check table info: %v", err)
	}
	defer rows.Close()

	for rows.Next() {
		var cid int
		var name, dataType string
		var notnull, pk int
		var defaultValue sql.NullString

		err := rows.Scan(&cid, &name, &dataType, &notnull, &defaultValue, &pk)
		if err != nil {
			return fmt.Errorf("failed to scan table info: %v", err)
		}

		if name == "name_normalized" {
			hasNormalized = true
			break
		}
	}

	if !hasNormalized {
		// Add normalized columns
		_, err := db.Exec(`
			ALTER TABLE restaurants ADD COLUMN name_normalized TEXT;
			ALTER TABLE restaurants ADD COLUMN location_normalized TEXT;
			ALTER TABLE restaurants ADD COLUMN cuisine_normalized TEXT;
		`)
		if err != nil {
			return fmt.Errorf("failed to add normalized columns: %v", err)
		}

		// Populate normalized columns
		rows, err := db.Query("SELECT id, name, location, cuisine FROM restaurants")
		if err != nil {
			return fmt.Errorf("failed to query restaurants: %v", err)
		}
		defer rows.Close()

		for rows.Next() {
			var id int64
			var name, location, cuisine sql.NullString

			err := rows.Scan(&id, &name, &location, &cuisine)
			if err != nil {
				return fmt.Errorf("failed to scan restaurant: %v", err)
			}

			// Normalize the text fields
			var nameNorm, locationNorm, cuisineNorm sql.NullString

			if name.Valid {
				nameNorm = sql.NullString{String: normalizeForSearch(name.String), Valid: true}
			}
			if location.Valid {
				locationNorm = sql.NullString{String: normalizeForSearch(location.String), Valid: true}
			}
			if cuisine.Valid {
				cuisineNorm = sql.NullString{String: normalizeForSearch(cuisine.String), Valid: true}
			}

			// Update the restaurant with normalized values
			_, err = db.Exec(`
				UPDATE restaurants 
				SET name_normalized = ?, location_normalized = ?, cuisine_normalized = ?
				WHERE id = ?
			`, nameNorm, locationNorm, cuisineNorm, id)
			if err != nil {
				return fmt.Errorf("failed to update normalized columns for restaurant %d: %v", id, err)
			}
		}

		// Add indexes on normalized columns
		_, err = db.Exec(`
			CREATE INDEX IF NOT EXISTS idx_name_normalized ON restaurants(name_normalized);
			CREATE INDEX IF NOT EXISTS idx_location_normalized ON restaurants(location_normalized);
			CREATE INDEX IF NOT EXISTS idx_cuisine_normalized ON restaurants(cuisine_normalized);
			
			-- Critical indexes for restaurant_awards table performance
			CREATE INDEX IF NOT EXISTS idx_restaurant_awards_restaurant_id ON restaurant_awards(restaurant_id);
			CREATE INDEX IF NOT EXISTS idx_restaurant_awards_year ON restaurant_awards(year);
			CREATE INDEX IF NOT EXISTS idx_restaurant_awards_distinction ON restaurant_awards(distinction);
			CREATE INDEX IF NOT EXISTS idx_restaurant_awards_composite ON restaurant_awards(restaurant_id, distinction, year);
		`)
		if err != nil {
			return fmt.Errorf("failed to create indexes on normalized columns: %v", err)
		}
	}

	return nil
}

// Restaurant represents a Michelin restaurant
type Restaurant struct {
	ID                    int64
	Name                  *string
	Address               *string
	Location              *string
	Cuisine               *string
	Longitude             *string
	Latitude              *string
	PhoneNumber           *string
	Url                   *string
	WebsiteUrl            *string
	FacilitiesAndServices *string
	Description           *string
	IsFavorite            bool
	IsVisited             bool
	VisitedDate           *string
	VisitedNotes          *string
	InGuide               int
	// Award info from latest award
	CurrentAward         *string
	CurrentPrice         *string
	CurrentGreenStar     *bool
	CurrentAwardYear     *int
	CurrentAwardLastYear *int
}

// RestaurantAward represents a restaurant's award history
type RestaurantAward struct {
	ID           int64
	RestaurantID int64
	Year         int
	Distinction  string
	Price        string
	GreenStar    *bool
}

// Initialize opens the database connection and ensures favorite/visited tables exist
func Initialize(dbPath string) (*sql.DB, error) {
	db, err := sql.Open("sqlite3", dbPath)
	if err != nil {
		return nil, fmt.Errorf("failed to open database: %v", err)
	}

	// Create additional tables for our custom features if they don't exist
	_, err = db.Exec(`
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
		
		-- Ensure critical performance indexes exist on restaurant_awards
		CREATE INDEX IF NOT EXISTS idx_restaurant_awards_restaurant_id ON restaurant_awards(restaurant_id);
		CREATE INDEX IF NOT EXISTS idx_restaurant_awards_year ON restaurant_awards(year);
		CREATE INDEX IF NOT EXISTS idx_restaurant_awards_distinction ON restaurant_awards(distinction);
		CREATE INDEX IF NOT EXISTS idx_restaurant_awards_composite ON restaurant_awards(restaurant_id, distinction, year);
	`)
	if err != nil {
		db.Close()
		return nil, fmt.Errorf("failed to create user tables: %v", err)
	}

	// Run migration for normalized columns
	err = MigrateNormalizedColumns(db)
	if err != nil {
		db.Close()
		return nil, fmt.Errorf("failed to migrate normalized columns: %v", err)
	}

	return db, nil
}

// ImportCSV is deprecated - the new database comes pre-populated
// This function is kept for backward compatibility but does nothing
func ImportCSV(db *sql.DB, csvPath string) error {
	// The new database comes pre-populated with restaurant data
	// No need to import from CSV
	return nil
}

// SearchRestaurants searches for restaurants based on name, location, cuisine, and awards
func SearchRestaurants(db *sql.DB, query string) ([]Restaurant, error) {
	// Parse search terms
	terms := strings.Fields(strings.ToLower(query))
	awardFilters := []string{}
	searchTerms := []string{}
	greenStarFilter := false

	// Separate award filters from search terms
	for _, term := range terms {
		switch term {
		case "1s":
			awardFilters = append(awardFilters, "1 Star")
		case "2s":
			awardFilters = append(awardFilters, "2 Stars")
		case "3s":
			awardFilters = append(awardFilters, "3 Stars")
		case "bg":
			awardFilters = append(awardFilters, "Bib Gourmand")
		case "sr":
			awardFilters = append(awardFilters, "Selected Restaurants")
		case "gs":
			greenStarFilter = true
		default:
			searchTerms = append(searchTerms, term)
		}
	}

	// Build WHERE clause with both SQL filtering (for speed) and normalization (for accuracy)
	whereClause := "WHERE 1=1"
	args := []interface{}{}

	// Add text search using both original and normalized columns for accent-insensitive matching
	if len(searchTerms) > 0 {
		sqlConditions := []string{}
		for _, term := range searchTerms {
			normalizedTerm := normalizeForSearch(term)
			// Search both original columns (case-insensitive) and normalized columns
			sqlConditions = append(sqlConditions,
				`(LOWER(r.name) LIKE ? OR LOWER(r.location) LIKE ? OR LOWER(r.cuisine) LIKE ? OR 
				  r.name_normalized LIKE ? OR r.location_normalized LIKE ? OR r.cuisine_normalized LIKE ?)`)
			searchTerm := "%" + term + "%"
			normalizedSearchTerm := "%" + normalizedTerm + "%"
			args = append(args, searchTerm, searchTerm, searchTerm,
				normalizedSearchTerm, normalizedSearchTerm, normalizedSearchTerm)
		}
		// Use OR between different search terms to be more inclusive
		whereClause += " AND (" + strings.Join(sqlConditions, " OR ") + ")"
	}

	// Add award filters
	if len(awardFilters) > 0 {
		whereClause += " AND ra.distinction IN ("
		for i, filter := range awardFilters {
			if i > 0 {
				whereClause += ", "
			}
			whereClause += "?"
			args = append(args, filter)
		}
		whereClause += ")"
	}

	// Add green star filter
	if greenStarFilter {
		whereClause += " AND ra.green_star = 1"
	}

	// Query restaurants with enhanced search and sorting
	queryStr := `
		SELECT 
			r.id, r.name, r.address, r.location, r.cuisine, r.longitude, r.latitude,
			r.phone_number, r.url, r.website_url, r.facilities_and_services, 
			r.description, r.in_guide,
			CASE WHEN uf.restaurant_id IS NOT NULL THEN 1 ELSE 0 END as is_favorite,
			CASE WHEN uv.restaurant_id IS NOT NULL THEN 1 ELSE 0 END as is_visited,
			uv.visited_date, uv.notes,
			ra.distinction, ra.price, ra.green_star, ra.first_year, ra.last_year
		FROM restaurants r
		LEFT JOIN user_favorites uf ON r.id = uf.restaurant_id
		LEFT JOIN user_visits uv ON r.id = uv.restaurant_id
		LEFT JOIN (
			SELECT 
				ra1.restaurant_id,
				ra1.distinction,
				ra1.price,
				ra1.green_star,
				MIN(ra_range.year) as first_year,
				MAX(ra_range.year) as last_year
			FROM restaurant_awards ra1
			JOIN (
				SELECT restaurant_id, MAX(year) as max_year
				FROM restaurant_awards
				GROUP BY restaurant_id
			) latest ON ra1.restaurant_id = latest.restaurant_id AND ra1.year = latest.max_year
			JOIN restaurant_awards ra_range ON ra1.restaurant_id = ra_range.restaurant_id 
				AND ra1.distinction = ra_range.distinction
			GROUP BY ra1.restaurant_id, ra1.distinction, ra1.price, ra1.green_star
		) ra ON r.id = ra.restaurant_id
		` + whereClause + `
		ORDER BY 
			CASE WHEN uf.restaurant_id IS NOT NULL THEN 0 ELSE 1 END,
			CASE 
				WHEN ra.distinction = '3 Stars' THEN 1
				WHEN ra.distinction = '2 Stars' THEN 2
				WHEN ra.distinction = '1 Star' THEN 3
				WHEN ra.distinction = 'Bib Gourmand' THEN 4
				WHEN ra.distinction = 'Selected Restaurants' THEN 5
				WHEN ra.distinction = 'Green Star' THEN 6
				ELSE 7
			END,
			r.name
	`

	rows, err := db.Query(queryStr, args...)
	if err != nil {
		return nil, fmt.Errorf("search query failed: %v", err)
	}
	defer rows.Close()

	var restaurants []Restaurant
	for rows.Next() {
		var r Restaurant
		err := rows.Scan(
			&r.ID, &r.Name, &r.Address, &r.Location, &r.Cuisine,
			&r.Longitude, &r.Latitude, &r.PhoneNumber,
			&r.Url, &r.WebsiteUrl, &r.FacilitiesAndServices, &r.Description, &r.InGuide,
			&r.IsFavorite, &r.IsVisited, &r.VisitedDate, &r.VisitedNotes,
			&r.CurrentAward, &r.CurrentPrice, &r.CurrentGreenStar, &r.CurrentAwardYear, &r.CurrentAwardLastYear,
		)
		if err != nil {
			return nil, fmt.Errorf("failed to scan row: %v", err)
		}

		// No need for additional filtering - all accent-insensitive matching is now handled by the database query

		restaurants = append(restaurants, r)
	}

	return restaurants, nil
}

// SearchFavoriteRestaurants searches within favorite restaurants only
func SearchFavoriteRestaurants(db *sql.DB, query string) ([]Restaurant, error) {
	// Parse search terms
	terms := strings.Fields(strings.ToLower(query))
	awardFilters := []string{}
	searchTerms := []string{}
	greenStarFilter := false

	// Separate award filters from search terms
	for _, term := range terms {
		switch term {
		case "1s":
			awardFilters = append(awardFilters, "1 Star")
		case "2s":
			awardFilters = append(awardFilters, "2 Stars")
		case "3s":
			awardFilters = append(awardFilters, "3 Stars")
		case "bg":
			awardFilters = append(awardFilters, "Bib Gourmand")
		case "sr":
			awardFilters = append(awardFilters, "Selected Restaurants")
		case "gs":
			greenStarFilter = true
		default:
			searchTerms = append(searchTerms, term)
		}
	}

	// Build WHERE clause with both SQL filtering (for speed) and normalization (for accuracy)
	whereClause := "WHERE 1=1"
	args := []interface{}{}

	// Add text search using both original and normalized columns for accent-insensitive matching
	if len(searchTerms) > 0 {
		sqlConditions := []string{}
		for _, term := range searchTerms {
			normalizedTerm := normalizeForSearch(term)
			// Search both original columns (case-insensitive) and normalized columns
			sqlConditions = append(sqlConditions,
				`(LOWER(r.name) LIKE ? OR LOWER(r.location) LIKE ? OR LOWER(r.cuisine) LIKE ? OR 
				  r.name_normalized LIKE ? OR r.location_normalized LIKE ? OR r.cuisine_normalized LIKE ?)`)
			searchTerm := "%" + term + "%"
			normalizedSearchTerm := "%" + normalizedTerm + "%"
			args = append(args, searchTerm, searchTerm, searchTerm,
				normalizedSearchTerm, normalizedSearchTerm, normalizedSearchTerm)
		}
		// Use OR between different search terms to be more inclusive
		whereClause += " AND (" + strings.Join(sqlConditions, " OR ") + ")"
	}

	// Add award filters
	if len(awardFilters) > 0 {
		whereClause += " AND ra.distinction IN ("
		for i, filter := range awardFilters {
			if i > 0 {
				whereClause += ", "
			}
			whereClause += "?"
			args = append(args, filter)
		}
		whereClause += ")"
	}

	// Add green star filter
	if greenStarFilter {
		whereClause += " AND ra.green_star = 1"
	}

	// Query restaurants with enhanced search and sorting - INNER JOIN with user_favorites to only get favorites
	queryStr := `
		SELECT 
			r.id, r.name, r.address, r.location, r.cuisine, r.longitude, r.latitude,
			r.phone_number, r.url, r.website_url, r.facilities_and_services, 
			r.description, r.in_guide,
			1 as is_favorite,
			CASE WHEN uv.restaurant_id IS NOT NULL THEN 1 ELSE 0 END as is_visited,
			uv.visited_date, uv.notes,
			ra.distinction, ra.price, ra.green_star, ra.first_year, ra.last_year
		FROM restaurants r
		INNER JOIN user_favorites uf ON r.id = uf.restaurant_id
		LEFT JOIN user_visits uv ON r.id = uv.restaurant_id
		LEFT JOIN (
			SELECT 
				ra1.restaurant_id,
				ra1.distinction,
				ra1.price,
				ra1.green_star,
				MIN(ra_range.year) as first_year,
				MAX(ra_range.year) as last_year
			FROM restaurant_awards ra1
			JOIN (
				SELECT restaurant_id, MAX(year) as max_year
				FROM restaurant_awards
				GROUP BY restaurant_id
			) latest ON ra1.restaurant_id = latest.restaurant_id AND ra1.year = latest.max_year
			JOIN restaurant_awards ra_range ON ra1.restaurant_id = ra_range.restaurant_id 
				AND ra1.distinction = ra_range.distinction
			GROUP BY ra1.restaurant_id, ra1.distinction, ra1.price, ra1.green_star
		) ra ON r.id = ra.restaurant_id
		` + whereClause + `
		ORDER BY 
			CASE 
				WHEN ra.distinction = '3 Stars' THEN 1
				WHEN ra.distinction = '2 Stars' THEN 2
				WHEN ra.distinction = '1 Star' THEN 3
				WHEN ra.distinction = 'Bib Gourmand' THEN 4
				WHEN ra.distinction = 'Selected Restaurants' THEN 5
				WHEN ra.distinction = 'Green Star' THEN 6
				ELSE 7
			END,
			r.name
	`

	rows, err := db.Query(queryStr, args...)
	if err != nil {
		return nil, fmt.Errorf("search favorite restaurants query failed: %v", err)
	}
	defer rows.Close()

	var restaurants []Restaurant
	for rows.Next() {
		var r Restaurant
		err := rows.Scan(
			&r.ID, &r.Name, &r.Address, &r.Location, &r.Cuisine,
			&r.Longitude, &r.Latitude, &r.PhoneNumber,
			&r.Url, &r.WebsiteUrl, &r.FacilitiesAndServices, &r.Description, &r.InGuide,
			&r.IsFavorite, &r.IsVisited, &r.VisitedDate, &r.VisitedNotes,
			&r.CurrentAward, &r.CurrentPrice, &r.CurrentGreenStar, &r.CurrentAwardYear, &r.CurrentAwardLastYear,
		)
		if err != nil {
			return nil, fmt.Errorf("failed to scan row: %v", err)
		}

		restaurants = append(restaurants, r)
	}

	return restaurants, nil
}

// SearchVisitedRestaurants searches within visited restaurants only
func SearchVisitedRestaurants(db *sql.DB, query string) ([]Restaurant, error) {
	// Parse search terms
	terms := strings.Fields(strings.ToLower(query))
	awardFilters := []string{}
	searchTerms := []string{}
	greenStarFilter := false

	// Separate award filters from search terms
	for _, term := range terms {
		switch term {
		case "1s":
			awardFilters = append(awardFilters, "1 Star")
		case "2s":
			awardFilters = append(awardFilters, "2 Stars")
		case "3s":
			awardFilters = append(awardFilters, "3 Stars")
		case "bg":
			awardFilters = append(awardFilters, "Bib Gourmand")
		case "sr":
			awardFilters = append(awardFilters, "Selected Restaurants")
		case "gs":
			greenStarFilter = true
		default:
			searchTerms = append(searchTerms, term)
		}
	}

	// Build WHERE clause with both SQL filtering (for speed) and normalization (for accuracy)
	whereClause := "WHERE 1=1"
	args := []interface{}{}

	// Add text search using both original and normalized columns for accent-insensitive matching
	if len(searchTerms) > 0 {
		sqlConditions := []string{}
		for _, term := range searchTerms {
			normalizedTerm := normalizeForSearch(term)
			// Search both original columns (case-insensitive) and normalized columns
			sqlConditions = append(sqlConditions,
				`(LOWER(r.name) LIKE ? OR LOWER(r.location) LIKE ? OR LOWER(r.cuisine) LIKE ? OR 
				  r.name_normalized LIKE ? OR r.location_normalized LIKE ? OR r.cuisine_normalized LIKE ?)`)
			searchTerm := "%" + term + "%"
			normalizedSearchTerm := "%" + normalizedTerm + "%"
			args = append(args, searchTerm, searchTerm, searchTerm,
				normalizedSearchTerm, normalizedSearchTerm, normalizedSearchTerm)
		}
		// Use OR between different search terms to be more inclusive
		whereClause += " AND (" + strings.Join(sqlConditions, " OR ") + ")"
	}

	// Add award filters
	if len(awardFilters) > 0 {
		whereClause += " AND ra.distinction IN ("
		for i, filter := range awardFilters {
			if i > 0 {
				whereClause += ", "
			}
			whereClause += "?"
			args = append(args, filter)
		}
		whereClause += ")"
	}

	// Add green star filter
	if greenStarFilter {
		whereClause += " AND ra.green_star = 1"
	}

	// Query restaurants with enhanced search and sorting - INNER JOIN with user_visits to only get visited
	queryStr := `
		SELECT 
			r.id, r.name, r.address, r.location, r.cuisine, r.longitude, r.latitude,
			r.phone_number, r.url, r.website_url, r.facilities_and_services, 
			r.description, r.in_guide,
			CASE WHEN uf.restaurant_id IS NOT NULL THEN 1 ELSE 0 END as is_favorite,
			1 as is_visited,
			uv.visited_date, uv.notes,
			ra.distinction, ra.price, ra.green_star, ra.first_year, ra.last_year
		FROM restaurants r
		INNER JOIN user_visits uv ON r.id = uv.restaurant_id
		LEFT JOIN user_favorites uf ON r.id = uf.restaurant_id
		LEFT JOIN (
			SELECT 
				ra1.restaurant_id,
				ra1.distinction,
				ra1.price,
				ra1.green_star,
				MIN(ra_range.year) as first_year,
				MAX(ra_range.year) as last_year
			FROM restaurant_awards ra1
			JOIN (
				SELECT restaurant_id, MAX(year) as max_year
				FROM restaurant_awards
				GROUP BY restaurant_id
			) latest ON ra1.restaurant_id = latest.restaurant_id AND ra1.year = latest.max_year
			JOIN restaurant_awards ra_range ON ra1.restaurant_id = ra_range.restaurant_id 
				AND ra1.distinction = ra_range.distinction
			GROUP BY ra1.restaurant_id, ra1.distinction, ra1.price, ra1.green_star
		) ra ON r.id = ra.restaurant_id
		` + whereClause + `
		ORDER BY 
			uv.visited_date DESC,
			CASE 
				WHEN ra.distinction = '3 Stars' THEN 1
				WHEN ra.distinction = '2 Stars' THEN 2
				WHEN ra.distinction = '1 Star' THEN 3
				WHEN ra.distinction = 'Bib Gourmand' THEN 4
				WHEN ra.distinction = 'Selected Restaurants' THEN 5
				WHEN ra.distinction = 'Green Star' THEN 6
				ELSE 7
			END,
			r.name
	`

	rows, err := db.Query(queryStr, args...)
	if err != nil {
		return nil, fmt.Errorf("search visited restaurants query failed: %v", err)
	}
	defer rows.Close()

	var restaurants []Restaurant
	for rows.Next() {
		var r Restaurant
		err := rows.Scan(
			&r.ID, &r.Name, &r.Address, &r.Location, &r.Cuisine,
			&r.Longitude, &r.Latitude, &r.PhoneNumber,
			&r.Url, &r.WebsiteUrl, &r.FacilitiesAndServices, &r.Description, &r.InGuide,
			&r.IsFavorite, &r.IsVisited, &r.VisitedDate, &r.VisitedNotes,
			&r.CurrentAward, &r.CurrentPrice, &r.CurrentGreenStar, &r.CurrentAwardYear, &r.CurrentAwardLastYear,
		)
		if err != nil {
			return nil, fmt.Errorf("failed to scan row: %v", err)
		}

		restaurants = append(restaurants, r)
	}

	return restaurants, nil
}

// GetRestaurantByID retrieves a restaurant by its ID
func GetRestaurantByID(db *sql.DB, id int64) (Restaurant, error) {
	var r Restaurant
	err := db.QueryRow(`
		SELECT 
			r.id, r.name, r.address, r.location, r.cuisine, r.longitude, r.latitude,
			r.phone_number, r.url, r.website_url, r.facilities_and_services, 
			r.description, r.in_guide,
			CASE WHEN uf.restaurant_id IS NOT NULL THEN 1 ELSE 0 END as is_favorite,
			CASE WHEN uv.restaurant_id IS NOT NULL THEN 1 ELSE 0 END as is_visited,
			uv.visited_date, uv.notes,
			ra.distinction, ra.price, ra.green_star, ra.first_year, ra.last_year
		FROM restaurants r
		LEFT JOIN user_favorites uf ON r.id = uf.restaurant_id
		LEFT JOIN user_visits uv ON r.id = uv.restaurant_id
		LEFT JOIN (
			SELECT 
				ra1.restaurant_id,
				ra1.distinction,
				ra1.price,
				ra1.green_star,
				MIN(ra_range.year) as first_year,
				MAX(ra_range.year) as last_year
			FROM restaurant_awards ra1
			JOIN (
				SELECT restaurant_id, MAX(year) as max_year
				FROM restaurant_awards
				GROUP BY restaurant_id
			) latest ON ra1.restaurant_id = latest.restaurant_id AND ra1.year = latest.max_year
			JOIN restaurant_awards ra_range ON ra1.restaurant_id = ra_range.restaurant_id 
				AND ra1.distinction = ra_range.distinction
			GROUP BY ra1.restaurant_id, ra1.distinction, ra1.price, ra1.green_star
		) ra ON r.id = ra.restaurant_id
		WHERE r.id = ?
	`, id).Scan(
		&r.ID, &r.Name, &r.Address, &r.Location, &r.Cuisine,
		&r.Longitude, &r.Latitude, &r.PhoneNumber,
		&r.Url, &r.WebsiteUrl, &r.FacilitiesAndServices, &r.Description, &r.InGuide,
		&r.IsFavorite, &r.IsVisited, &r.VisitedDate, &r.VisitedNotes,
		&r.CurrentAward, &r.CurrentPrice, &r.CurrentGreenStar, &r.CurrentAwardYear, &r.CurrentAwardLastYear,
	)

	if err != nil {
		return Restaurant{}, fmt.Errorf("failed to get restaurant: %v", err)
	}

	return r, nil
}

// ToggleFavorite toggles a restaurant's favorite status
func ToggleFavorite(db *sql.DB, id int64) error {
	// Check if restaurant is already in favorites
	var exists bool
	err := db.QueryRow("SELECT EXISTS(SELECT 1 FROM user_favorites WHERE restaurant_id = ?)", id).Scan(&exists)
	if err != nil {
		return fmt.Errorf("failed to check favorite status: %v", err)
	}

	if exists {
		// Remove from favorites
		_, err = db.Exec("DELETE FROM user_favorites WHERE restaurant_id = ?", id)
	} else {
		// Add to favorites
		_, err = db.Exec("INSERT INTO user_favorites (restaurant_id) VALUES (?)", id)
	}

	if err != nil {
		return fmt.Errorf("failed to toggle favorite: %v", err)
	}

	return nil
}

// ToggleVisited toggles a restaurant's visited status
func ToggleVisited(db *sql.DB, id int64, date, notes string) error {
	// Check if restaurant is already in visited
	var exists bool
	err := db.QueryRow("SELECT EXISTS(SELECT 1 FROM user_visits WHERE restaurant_id = ?)", id).Scan(&exists)
	if err != nil {
		return fmt.Errorf("failed to check visited status: %v", err)
	}

	if exists {
		// Remove from visited
		_, err = db.Exec("DELETE FROM user_visits WHERE restaurant_id = ?", id)
	} else {
		// Add to visited
		// Handle empty string inputs
		var dateParam, notesParam interface{}

		if date == "" {
			dateParam = nil
		} else {
			dateParam = date
		}

		if notes == "" {
			notesParam = nil
		} else {
			notesParam = notes
		}

		_, err = db.Exec("INSERT INTO user_visits (restaurant_id, visited_date, notes) VALUES (?, ?, ?)", id, dateParam, notesParam)
	}

	if err != nil {
		return fmt.Errorf("failed to update visited status: %v", err)
	}

	return nil
}

// GetFavoriteRestaurants retrieves all favorite restaurants
func GetFavoriteRestaurants(db *sql.DB) ([]Restaurant, error) {
	rows, err := db.Query(`
		SELECT 
			r.id, r.name, r.address, r.location, r.cuisine, r.longitude, r.latitude,
			r.phone_number, r.url, r.website_url, r.facilities_and_services, 
			r.description, r.in_guide,
			1 as is_favorite,
			CASE WHEN uv.restaurant_id IS NOT NULL THEN 1 ELSE 0 END as is_visited,
			uv.visited_date, uv.notes,
			ra.distinction, ra.price, ra.green_star, ra.first_year, ra.last_year
		FROM restaurants r
		INNER JOIN user_favorites uf ON r.id = uf.restaurant_id
		LEFT JOIN user_visits uv ON r.id = uv.restaurant_id
		LEFT JOIN (
			SELECT 
				ra1.restaurant_id,
				ra1.distinction,
				ra1.price,
				ra1.green_star,
				MIN(ra_range.year) as first_year,
				MAX(ra_range.year) as last_year
			FROM restaurant_awards ra1
			JOIN (
				SELECT restaurant_id, MAX(year) as max_year
				FROM restaurant_awards
				GROUP BY restaurant_id
			) latest ON ra1.restaurant_id = latest.restaurant_id AND ra1.year = latest.max_year
			JOIN restaurant_awards ra_range ON ra1.restaurant_id = ra_range.restaurant_id 
				AND ra1.distinction = ra_range.distinction
			GROUP BY ra1.restaurant_id, ra1.distinction, ra1.price, ra1.green_star
		) ra ON r.id = ra.restaurant_id
		ORDER BY r.name
	`)

	if err != nil {
		return nil, fmt.Errorf("failed to get favorite restaurants: %v", err)
	}
	defer rows.Close()

	var restaurants []Restaurant
	for rows.Next() {
		var r Restaurant
		err := rows.Scan(
			&r.ID, &r.Name, &r.Address, &r.Location, &r.Cuisine,
			&r.Longitude, &r.Latitude, &r.PhoneNumber,
			&r.Url, &r.WebsiteUrl, &r.FacilitiesAndServices, &r.Description, &r.InGuide,
			&r.IsFavorite, &r.IsVisited, &r.VisitedDate, &r.VisitedNotes,
			&r.CurrentAward, &r.CurrentPrice, &r.CurrentGreenStar, &r.CurrentAwardYear, &r.CurrentAwardLastYear,
		)
		if err != nil {
			return nil, fmt.Errorf("failed to scan row: %v", err)
		}
		restaurants = append(restaurants, r)
	}

	return restaurants, nil
}

// GetVisitedRestaurants retrieves all visited restaurants
func GetVisitedRestaurants(db *sql.DB) ([]Restaurant, error) {
	rows, err := db.Query(`
		SELECT 
			r.id, r.name, r.address, r.location, r.cuisine, r.longitude, r.latitude,
			r.phone_number, r.url, r.website_url, r.facilities_and_services, 
			r.description, r.in_guide,
			CASE WHEN uf.restaurant_id IS NOT NULL THEN 1 ELSE 0 END as is_favorite,
			1 as is_visited,
			uv.visited_date, uv.notes,
			ra.distinction, ra.price, ra.green_star, ra.first_year, ra.last_year
		FROM restaurants r
		INNER JOIN user_visits uv ON r.id = uv.restaurant_id
		LEFT JOIN user_favorites uf ON r.id = uf.restaurant_id
		LEFT JOIN (
			SELECT 
				ra1.restaurant_id,
				ra1.distinction,
				ra1.price,
				ra1.green_star,
				MIN(ra_range.year) as first_year,
				MAX(ra_range.year) as last_year
			FROM restaurant_awards ra1
			JOIN (
				SELECT restaurant_id, MAX(year) as max_year
				FROM restaurant_awards
				GROUP BY restaurant_id
			) latest ON ra1.restaurant_id = latest.restaurant_id AND ra1.year = latest.max_year
			JOIN restaurant_awards ra_range ON ra1.restaurant_id = ra_range.restaurant_id 
				AND ra1.distinction = ra_range.distinction
			GROUP BY ra1.restaurant_id, ra1.distinction, ra1.price, ra1.green_star
		) ra ON r.id = ra.restaurant_id
		ORDER BY uv.visited_date DESC, r.name
	`)

	if err != nil {
		return nil, fmt.Errorf("failed to get visited restaurants: %v", err)
	}
	defer rows.Close()

	var restaurants []Restaurant
	for rows.Next() {
		var r Restaurant
		err := rows.Scan(
			&r.ID, &r.Name, &r.Address, &r.Location, &r.Cuisine,
			&r.Longitude, &r.Latitude, &r.PhoneNumber,
			&r.Url, &r.WebsiteUrl, &r.FacilitiesAndServices, &r.Description, &r.InGuide,
			&r.IsFavorite, &r.IsVisited, &r.VisitedDate, &r.VisitedNotes,
			&r.CurrentAward, &r.CurrentPrice, &r.CurrentGreenStar, &r.CurrentAwardYear, &r.CurrentAwardLastYear,
		)
		if err != nil {
			return nil, fmt.Errorf("failed to scan row: %v", err)
		}
		restaurants = append(restaurants, r)
	}

	return restaurants, nil
}

// GetRestaurantAwardHistory retrieves the award history for a restaurant
func GetRestaurantAwardHistory(db *sql.DB, restaurantID int64) ([]RestaurantAward, error) {
	rows, err := db.Query(`
		SELECT 
			id, restaurant_id, year, distinction, price, green_star
		FROM restaurant_awards
		WHERE restaurant_id = ?
		ORDER BY year DESC
	`, restaurantID)

	if err != nil {
		return nil, fmt.Errorf("failed to get restaurant award history: %v", err)
	}
	defer rows.Close()

	var awards []RestaurantAward
	for rows.Next() {
		var award RestaurantAward
		err := rows.Scan(
			&award.ID, &award.RestaurantID, &award.Year, &award.Distinction, &award.Price, &award.GreenStar,
		)
		if err != nil {
			return nil, fmt.Errorf("failed to scan award row: %v", err)
		}
		awards = append(awards, award)
	}

	return awards, nil
}

// UpdateDatabase checks for a zipped database in the workflow directory and updates the main database
// Returns nil if update was successful, an error if update failed, or a special "no update available" error
func UpdateDatabase(currentDbPath string) error {
	workflowDir := "/Users/giovanni/gDrive/GitHub repos/alfred-michelin/source"
	zipPath := filepath.Join(workflowDir, "michelin.db.zip")

	// Check if the zip file exists
	if _, err := os.Stat(zipPath); os.IsNotExist(err) {
		// Return a special error type that indicates no update is available (not a real error)
		return &NoUpdateAvailableError{path: zipPath}
	}

	fmt.Printf("[UPDATE INFO] Found database update file: %s\n", zipPath)

	// Extract the zip file to a temporary location
	tempDir, err := os.MkdirTemp("", "michelin_update_")
	if err != nil {
		return fmt.Errorf("failed to create temp directory: %v", err)
	}
	defer os.RemoveAll(tempDir)

	newDbPath := filepath.Join(tempDir, "michelin.db")
	err = extractZipFile(zipPath, newDbPath)
	if err != nil {
		return fmt.Errorf("failed to extract zip file: %v", err)
	}

	// Backup the current database
	backupPath := strings.Replace(currentDbPath, ".db", "_backup.db", 1)
	err = copyFile(currentDbPath, backupPath)
	if err != nil {
		return fmt.Errorf("failed to backup current database: %v", err)
	}

	fmt.Printf("Backed up current database to: %s\n", backupPath)

	// Open both databases
	currentDb, err := sql.Open("sqlite3", currentDbPath)
	if err != nil {
		return fmt.Errorf("failed to open current database: %v", err)
	}
	defer currentDb.Close()

	newDb, err := sql.Open("sqlite3", newDbPath)
	if err != nil {
		return fmt.Errorf("failed to open new database: %v", err)
	}
	defer newDb.Close()

	// Preserve user data during update
	err = preserveUserDataDuringUpdate(currentDb, newDb)
	if err != nil {
		return fmt.Errorf("failed to preserve user data during update: %v", err)
	}

	// Replace the current database with the updated one
	currentDb.Close()
	newDb.Close()

	err = os.Remove(currentDbPath)
	if err != nil {
		return fmt.Errorf("failed to remove old database: %v", err)
	}

	err = copyFile(newDbPath, currentDbPath)
	if err != nil {
		return fmt.Errorf("failed to copy updated database: %v", err)
	}

	// Delete the zip file to prevent re-updating
	err = os.Remove(zipPath)
	if err != nil {
		return fmt.Errorf("failed to remove zip file: %v", err)
	}

	fmt.Printf("[UPDATE SUCCESS] Database update completed successfully\n")
	fmt.Printf("[UPDATE SUCCESS] Backup created at: %s\n", backupPath)
	fmt.Printf("[UPDATE SUCCESS] Update zip file removed: %s\n", zipPath)
	fmt.Printf("[UPDATE SUCCESS] ===== DATABASE UPDATE SUMMARY =====\n")
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

// preserveUserDataDuringUpdate handles the complex logic of updating the database while preserving user data
func preserveUserDataDuringUpdate(currentDb, newDb *sql.DB) error {
	// Get user favorites and visits from current database
	favorites, err := getUserFavorites(currentDb)
	if err != nil {
		return fmt.Errorf("failed to get user favorites: %v", err)
	}

	visits, err := getUserVisits(currentDb)
	if err != nil {
		return fmt.Errorf("failed to get user visits: %v", err)
	}

	// Get restaurant counts for statistics
	oldRestaurantCount, err := getRestaurantCount(currentDb)
	if err != nil {
		return fmt.Errorf("failed to get old restaurant count: %v", err)
	}

	newRestaurantCount, err := getRestaurantCount(newDb)
	if err != nil {
		return fmt.Errorf("failed to get new restaurant count: %v", err)
	}

	// Get restaurant mappings between old and new databases
	oldToNewRestaurantMap, err := createRestaurantMapping(currentDb, newDb)
	if err != nil {
		return fmt.Errorf("failed to create restaurant mapping: %v", err)
	}

	// Print statistics to STDERR
	fmt.Printf("[UPDATE STATS] Old database: %d restaurants\n", oldRestaurantCount)
	fmt.Printf("[UPDATE STATS] New database: %d restaurants\n", newRestaurantCount)
	fmt.Printf("[UPDATE STATS] Restaurants added: %d\n", newRestaurantCount-oldRestaurantCount)
	fmt.Printf("[UPDATE STATS] Restaurant ID mappings found: %d\n", len(oldToNewRestaurantMap))
	fmt.Printf("[UPDATE STATS] User favorites to migrate: %d\n", len(favorites))
	fmt.Printf("[UPDATE STATS] User visits to migrate: %d\n", len(visits))

	// Create user tables in the new database
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

	// Migrate user favorites
	migratedFavorites := 0
	orphanedFavorites := 0
	for _, favorite := range favorites {
		if newRestaurantID, exists := oldToNewRestaurantMap[favorite.RestaurantID]; exists {
			_, err = newDb.Exec("INSERT OR IGNORE INTO user_favorites (restaurant_id, created_at) VALUES (?, ?)",
				newRestaurantID, favorite.CreatedAt)
			if err != nil {
				return fmt.Errorf("failed to migrate favorite restaurant %d: %v", favorite.RestaurantID, err)
			}
			migratedFavorites++
		} else {
			orphanedFavorites++
			fmt.Printf("[UPDATE WARN] Orphaned favorite: restaurant ID %d not found in new database\n", favorite.RestaurantID)
		}
	}

	// Migrate user visits
	migratedVisits := 0
	orphanedVisits := 0
	for _, visit := range visits {
		if newRestaurantID, exists := oldToNewRestaurantMap[visit.RestaurantID]; exists {
			_, err = newDb.Exec("INSERT OR IGNORE INTO user_visits (restaurant_id, visited_date, notes, created_at) VALUES (?, ?, ?, ?)",
				newRestaurantID, visit.VisitedDate, visit.Notes, visit.CreatedAt)
			if err != nil {
				return fmt.Errorf("failed to migrate visit for restaurant %d: %v", visit.RestaurantID, err)
			}
			migratedVisits++
		} else {
			orphanedVisits++
			fmt.Printf("[UPDATE WARN] Orphaned visit: restaurant ID %d not found in new database\n", visit.RestaurantID)
		}
	}

	// Print migration results
	fmt.Printf("[UPDATE STATS] Favorites successfully migrated: %d/%d\n", migratedFavorites, len(favorites))
	fmt.Printf("[UPDATE STATS] Visits successfully migrated: %d/%d\n", migratedVisits, len(visits))
	if orphanedFavorites > 0 {
		fmt.Printf("[UPDATE WARN] Orphaned favorites (restaurant no longer exists): %d\n", orphanedFavorites)
	}
	if orphanedVisits > 0 {
		fmt.Printf("[UPDATE WARN] Orphaned visits (restaurant no longer exists): %d\n", orphanedVisits)
	}

	// Run migration for normalized columns on the new database
	err = MigrateNormalizedColumns(newDb)
	if err != nil {
		return fmt.Errorf("failed to migrate normalized columns in new database: %v", err)
	}

	return nil
}

// UserFavorite represents a user's favorite restaurant record
type UserFavorite struct {
	ID           int64
	RestaurantID int64
	CreatedAt    string
}

// UserVisit represents a user's restaurant visit record
type UserVisit struct {
	ID           int64
	RestaurantID int64
	VisitedDate  *string
	Notes        *string
	CreatedAt    string
}

// getRestaurantCount returns the total number of restaurants in the database
func getRestaurantCount(db *sql.DB) (int, error) {
	var count int
	err := db.QueryRow("SELECT COUNT(*) FROM restaurants").Scan(&count)
	return count, err
}

// getUserFavorites retrieves all user favorites from the database
func getUserFavorites(db *sql.DB) ([]UserFavorite, error) {
	rows, err := db.Query("SELECT id, restaurant_id, created_at FROM user_favorites")
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var favorites []UserFavorite
	for rows.Next() {
		var fav UserFavorite
		err := rows.Scan(&fav.ID, &fav.RestaurantID, &fav.CreatedAt)
		if err != nil {
			return nil, err
		}
		favorites = append(favorites, fav)
	}

	return favorites, nil
}

// getUserVisits retrieves all user visits from the database
func getUserVisits(db *sql.DB) ([]UserVisit, error) {
	rows, err := db.Query("SELECT id, restaurant_id, visited_date, notes, created_at FROM user_visits")
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var visits []UserVisit
	for rows.Next() {
		var visit UserVisit
		err := rows.Scan(&visit.ID, &visit.RestaurantID, &visit.VisitedDate, &visit.Notes, &visit.CreatedAt)
		if err != nil {
			return nil, err
		}
		visits = append(visits, visit)
	}

	return visits, nil
}

// createRestaurantMapping creates a mapping between restaurant IDs in old and new databases
func createRestaurantMapping(oldDb, newDb *sql.DB) (map[int64]int64, error) {
	// Get restaurants from old database
	oldRestaurants := make(map[string]int64) // URL -> ID
	rows, err := oldDb.Query("SELECT id, url FROM restaurants WHERE url IS NOT NULL AND url != ''")
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	oldRestaurantsWithURL := 0
	for rows.Next() {
		var id int64
		var url string
		err := rows.Scan(&id, &url)
		if err != nil {
			return nil, err
		}
		oldRestaurants[url] = id
		oldRestaurantsWithURL++
	}

	// Get restaurants from new database and create mapping
	mapping := make(map[int64]int64) // old ID -> new ID
	rows, err = newDb.Query("SELECT id, url FROM restaurants WHERE url IS NOT NULL AND url != ''")
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	newRestaurantsWithURL := 0
	unchangedIDs := 0
	changedIDs := 0

	for rows.Next() {
		var newID int64
		var url string
		err := rows.Scan(&newID, &url)
		if err != nil {
			return nil, err
		}

		newRestaurantsWithURL++
		if oldID, exists := oldRestaurants[url]; exists {
			mapping[oldID] = newID
			if oldID == newID {
				unchangedIDs++
			} else {
				changedIDs++
				fmt.Printf("[UPDATE DEBUG] Restaurant ID changed: %d -> %d (URL: %s)\n", oldID, newID, url)
			}
		}
	}

	// Print mapping statistics
	fmt.Printf("[UPDATE STATS] Old restaurants with URLs: %d\n", oldRestaurantsWithURL)
	fmt.Printf("[UPDATE STATS] New restaurants with URLs: %d\n", newRestaurantsWithURL)
	fmt.Printf("[UPDATE STATS] Restaurant IDs unchanged: %d\n", unchangedIDs)
	fmt.Printf("[UPDATE STATS] Restaurant IDs changed: %d\n", changedIDs)
	fmt.Printf("[UPDATE STATS] Total mappings created: %d\n", len(mapping))

	return mapping, nil
}
