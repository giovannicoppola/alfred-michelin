package db

import (
	"database/sql"
	"fmt"
	"strings"

	_ "github.com/mattn/go-sqlite3"
)

const (
	DbFileName = "michelin new.db"
)

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
	// Award info from latest award
	CurrentAward     *string
	CurrentPrice     *string
	CurrentGreenStar *bool
	CurrentAwardYear *int
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
	`)
	if err != nil {
		db.Close()
		return nil, fmt.Errorf("failed to create user tables: %v", err)
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

	// Build WHERE clause
	whereClause := "WHERE 1=1"
	args := []interface{}{}

	// Add search terms (partial match on name, location, cuisine)
	if len(searchTerms) > 0 {
		for _, term := range searchTerms {
			whereClause += " AND (LOWER(r.name) LIKE ? OR LOWER(r.location) LIKE ? OR LOWER(r.cuisine) LIKE ?)"
			searchTerm := "%" + term + "%"
			args = append(args, searchTerm, searchTerm, searchTerm)
		}
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
			r.description,
			CASE WHEN uf.restaurant_id IS NOT NULL THEN 1 ELSE 0 END as is_favorite,
			CASE WHEN uv.restaurant_id IS NOT NULL THEN 1 ELSE 0 END as is_visited,
			uv.visited_date, uv.notes,
			ra.distinction, ra.price, ra.green_star, ra.first_year
		FROM restaurants r
		LEFT JOIN user_favorites uf ON r.id = uf.restaurant_id
		LEFT JOIN user_visits uv ON r.id = uv.restaurant_id
		LEFT JOIN (
			SELECT 
				ra1.restaurant_id, 
				ra1.distinction, 
				ra1.price, 
				ra1.green_star, 
				ra1.year as first_year
			FROM restaurant_awards ra1
			WHERE ra1.distinction = (
				SELECT ra2.distinction 
				FROM restaurant_awards ra2 
				WHERE ra2.restaurant_id = ra1.restaurant_id 
				ORDER BY ra2.year DESC 
				LIMIT 1
			)
			AND ra1.year = (
				SELECT MIN(ra3.year)
				FROM restaurant_awards ra3
				WHERE ra3.restaurant_id = ra1.restaurant_id
				AND ra3.distinction = ra1.distinction
			)
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
			&r.Url, &r.WebsiteUrl, &r.FacilitiesAndServices, &r.Description,
			&r.IsFavorite, &r.IsVisited, &r.VisitedDate, &r.VisitedNotes,
			&r.CurrentAward, &r.CurrentPrice, &r.CurrentGreenStar, &r.CurrentAwardYear,
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
			r.description,
			CASE WHEN uf.restaurant_id IS NOT NULL THEN 1 ELSE 0 END as is_favorite,
			CASE WHEN uv.restaurant_id IS NOT NULL THEN 1 ELSE 0 END as is_visited,
			uv.visited_date, uv.notes,
			ra.distinction, ra.price, ra.green_star, ra.first_year
		FROM restaurants r
		LEFT JOIN user_favorites uf ON r.id = uf.restaurant_id
		LEFT JOIN user_visits uv ON r.id = uv.restaurant_id
		LEFT JOIN (
			SELECT 
				ra1.restaurant_id, 
				ra1.distinction, 
				ra1.price, 
				ra1.green_star, 
				ra1.year as first_year
			FROM restaurant_awards ra1
			WHERE ra1.distinction = (
				SELECT ra2.distinction 
				FROM restaurant_awards ra2 
				WHERE ra2.restaurant_id = ra1.restaurant_id 
				ORDER BY ra2.year DESC 
				LIMIT 1
			)
			AND ra1.year = (
				SELECT MIN(ra3.year)
				FROM restaurant_awards ra3
				WHERE ra3.restaurant_id = ra1.restaurant_id
				AND ra3.distinction = ra1.distinction
			)
		) ra ON r.id = ra.restaurant_id
		WHERE r.id = ?
	`, id).Scan(
		&r.ID, &r.Name, &r.Address, &r.Location, &r.Cuisine,
		&r.Longitude, &r.Latitude, &r.PhoneNumber,
		&r.Url, &r.WebsiteUrl, &r.FacilitiesAndServices, &r.Description,
		&r.IsFavorite, &r.IsVisited, &r.VisitedDate, &r.VisitedNotes,
		&r.CurrentAward, &r.CurrentPrice, &r.CurrentGreenStar, &r.CurrentAwardYear,
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
			r.description,
			1 as is_favorite,
			CASE WHEN uv.restaurant_id IS NOT NULL THEN 1 ELSE 0 END as is_visited,
			uv.visited_date, uv.notes,
			ra.distinction, ra.price, ra.green_star, ra.first_year
		FROM restaurants r
		INNER JOIN user_favorites uf ON r.id = uf.restaurant_id
		LEFT JOIN user_visits uv ON r.id = uv.restaurant_id
		LEFT JOIN (
			SELECT 
				ra1.restaurant_id, 
				ra1.distinction, 
				ra1.price, 
				ra1.green_star, 
				ra1.year as first_year
			FROM restaurant_awards ra1
			WHERE ra1.distinction = (
				SELECT ra2.distinction 
				FROM restaurant_awards ra2 
				WHERE ra2.restaurant_id = ra1.restaurant_id 
				ORDER BY ra2.year DESC 
				LIMIT 1
			)
			AND ra1.year = (
				SELECT MIN(ra3.year)
				FROM restaurant_awards ra3
				WHERE ra3.restaurant_id = ra1.restaurant_id
				AND ra3.distinction = ra1.distinction
			)
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
			&r.Url, &r.WebsiteUrl, &r.FacilitiesAndServices, &r.Description,
			&r.IsFavorite, &r.IsVisited, &r.VisitedDate, &r.VisitedNotes,
			&r.CurrentAward, &r.CurrentPrice, &r.CurrentGreenStar, &r.CurrentAwardYear,
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
			r.description,
			CASE WHEN uf.restaurant_id IS NOT NULL THEN 1 ELSE 0 END as is_favorite,
			1 as is_visited,
			uv.visited_date, uv.notes,
			ra.distinction, ra.price, ra.green_star, ra.first_year
		FROM restaurants r
		INNER JOIN user_visits uv ON r.id = uv.restaurant_id
		LEFT JOIN user_favorites uf ON r.id = uf.restaurant_id
		LEFT JOIN (
			SELECT 
				ra1.restaurant_id, 
				ra1.distinction, 
				ra1.price, 
				ra1.green_star, 
				ra1.year as first_year
			FROM restaurant_awards ra1
			WHERE ra1.distinction = (
				SELECT ra2.distinction 
				FROM restaurant_awards ra2 
				WHERE ra2.restaurant_id = ra1.restaurant_id 
				ORDER BY ra2.year DESC 
				LIMIT 1
			)
			AND ra1.year = (
				SELECT MIN(ra3.year)
				FROM restaurant_awards ra3
				WHERE ra3.restaurant_id = ra1.restaurant_id
				AND ra3.distinction = ra1.distinction
			)
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
			&r.Url, &r.WebsiteUrl, &r.FacilitiesAndServices, &r.Description,
			&r.IsFavorite, &r.IsVisited, &r.VisitedDate, &r.VisitedNotes,
			&r.CurrentAward, &r.CurrentPrice, &r.CurrentGreenStar, &r.CurrentAwardYear,
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
