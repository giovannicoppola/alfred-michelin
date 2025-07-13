package historical

import (
	"context"
	"encoding/csv"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/ngshiheng/michelin-my-maps/v3/internal/models"
	"github.com/ngshiheng/michelin-my-maps/v3/internal/parser"
	"github.com/ngshiheng/michelin-my-maps/v3/internal/storage"
	log "github.com/sirupsen/logrus"
	"gorm.io/gorm"
)

// Config holds configuration for the dataset processor
type Config struct {
	DatabasePath string
	DatasetDir   string // Directory containing CSV files (historical, current, or future datasets)
}

// DefaultConfig returns a default config for the dataset processor
func DefaultConfig() *Config {
	return &Config{
		DatabasePath: "data/michelin.db",
		DatasetDir:   "data/HistoricalData", // Default to historical data for backward compatibility
	}
}

// ProcessingStats tracks statistics during dataset processing
type ProcessingStats struct {
	StartTime             time.Time
	EndTime               time.Time
	FilesProcessed        int
	FilesSkipped          int
	TotalRestaurantsFound int
	NewRestaurantsAdded   int
	ExistingRestaurants   int
	AwardsAdded           int
	AwardsSkipped         int
	ProcessingErrors      int
	SkippedRecords        int

	// Breakdown by file
	FileStats map[string]*FileStats

	// Awards breakdown
	AwardStats map[string]int

	// Track unique restaurants to avoid double counting
	UniqueRestaurants map[string]bool
}

// FileStats tracks statistics for a single file
type FileStats struct {
	FileName                    string
	Year                        int
	Date                        time.Time // Add date for proper sorting in reports
	RestaurantsFound            int
	RestaurantsSkipped          int
	NewRestaurants              int
	ExistingRestaurants         int
	AwardsAdded                 int
	AwardsSkipped               int
	ProcessingErrors            int
	RestaurantsRemovedFromGuide int // For recent CSV files: restaurants marked as no longer in guide
}

// FileInfo holds CSV file information for sorting
type FileInfo struct {
	Path string
	Year int
	Date time.Time
}

// Processor handles importing CSV dataset files (historical, current, or future versions)
type Processor struct {
	config     *Config
	repository storage.RestaurantRepository
	stats      *ProcessingStats
}

// isRecentDataset determines if a CSV file represents recent/current data vs historical data
// Recent means: within last month or the file date is newer than what we expect for historical data
func (p *Processor) isRecentDataset(fileDate time.Time) bool {
	now := time.Now()
	oneMonthAgo := now.AddDate(0, -1, 0)

	// Consider it recent if it's within the last month
	return fileDate.After(oneMonthAgo)
}

// getCurrentGuideRestaurant checks if a restaurant is currently in the guide (InGuide=1) in the SQLite database
func (p *Processor) getCurrentGuideRestaurant(ctx context.Context, websiteURL, michelinURL string) (*models.Restaurant, bool, error) {
	var restaurant *models.Restaurant
	var err error

	// First try to find by website URL
	if websiteURL != "" {
		restaurant, err = p.repository.FindRestaurantByWebsiteURL(ctx, websiteURL)
		if err != nil && err != gorm.ErrRecordNotFound {
			return nil, false, fmt.Errorf("failed to find restaurant by website URL: %w", err)
		}
	}

	// If not found by website URL, try by Michelin Guide URL
	if restaurant == nil && michelinURL != "" {
		restaurant, err = p.repository.FindRestaurantByURL(ctx, michelinURL)
		if err != nil && err != gorm.ErrRecordNotFound {
			return nil, false, fmt.Errorf("failed to find restaurant by Michelin URL: %w", err)
		}
	}

	if restaurant == nil {
		return nil, false, nil // Restaurant not found in current database
	}

	// If restaurant exists in database, it's considered "in guide" unless explicitly marked as false
	// This handles the case where in_guide might be NULL or false but should be true
	isInGuide := restaurant.InGuide
	if !isInGuide {
		// Check if in_guide is actually NULL by querying the database directly
		var inGuideValue *bool
		err := p.repository.(*storage.SQLiteRepository).GetDB().WithContext(ctx).
			Model(&models.Restaurant{}).
			Where("id = ?", restaurant.ID).
			Select("in_guide").
			Scan(&inGuideValue).Error

		if err == nil && inGuideValue == nil {
			// in_guide is NULL, treat as "in guide" and fix it
			err = p.repository.(*storage.SQLiteRepository).GetDB().WithContext(ctx).
				Model(&models.Restaurant{}).
				Where("id = ?", restaurant.ID).
				Update("in_guide", true).Error

			if err == nil {
				isInGuide = true
				restaurant.InGuide = true // Update the local copy too
			}
		}
	}

	// Return the restaurant and whether it's currently in the guide
	return restaurant, isInGuide, nil
}

// New creates a new dataset processor
func New() (*Processor, error) {
	cfg := DefaultConfig()

	repo, err := storage.NewSQLiteRepository(cfg.DatabasePath)
	if err != nil {
		return nil, fmt.Errorf("failed to create repository: %w", err)
	}

	stats := &ProcessingStats{
		FileStats:         make(map[string]*FileStats),
		AwardStats:        make(map[string]int),
		UniqueRestaurants: make(map[string]bool),
	}

	return &Processor{
		config:     cfg,
		repository: repo,
		stats:      stats,
	}, nil
}

// HistoricalRestaurant represents a restaurant record from historical CSV file
type HistoricalRestaurant struct {
	Name           string
	Address        string
	Location       string
	Price          string
	Type           string // Cuisine type
	Longitude      string
	Latitude       string
	PhoneNumber    string
	URL            string // Michelin Guide URL
	WebsiteURL     string
	Classification string // Award classification
}

// ProcessHistoricalData processes all CSV dataset files in the configured directory
// This works for historical, current, or future dataset versions with intelligent InGuide logic:
// - SQLite database is the source of truth for current guide status
// - Recent CSV files (last month):
//   - New restaurants get InGuide=true + Michelin URL
//   - Existing restaurants missing from CSV get InGuide=false (no longer in guide)
//   - Existing restaurants in CSV keep InGuide=true and get awards added
//
// - Historical CSV files (older):
//   - New restaurants get InGuide=false, keep Michelin URL for reference
//   - Existing restaurants: InGuide status NEVER changed, only add historical awards
//
// limit: maximum number of restaurants to process per file (0 = no limit, for testing)
func (p *Processor) ProcessHistoricalData(ctx context.Context, limit int) error {
	// Initialize statistics with local time
	p.stats.StartTime = time.Now()

	csvFiles, err := p.findDatasetCSVFiles()
	if err != nil {
		return fmt.Errorf("failed to find dataset CSV files: %w", err)
	}

	// Sort files by date (oldest first) for chronological processing
	sortedFiles, err := p.sortFilesByDate(csvFiles)
	if err != nil {
		return fmt.Errorf("failed to sort files by date: %w", err)
	}

	log.WithFields(log.Fields{"count": len(sortedFiles)}).Info("found and sorted dataset CSV files chronologically")

	for _, fileInfo := range sortedFiles {
		log.WithFields(log.Fields{
			"file": fileInfo.Path,
			"date": fileInfo.Date.Format("2006-01-02"),
			"year": fileInfo.Year,
		}).Info("processing dataset CSV file")

		if err := p.processDatasetCSVFile(ctx, fileInfo.Path, limit); err != nil {
			log.WithFields(log.Fields{
				"file":  fileInfo.Path,
				"date":  fileInfo.Date.Format("2006-01-02"),
				"year":  fileInfo.Year,
				"error": err,
			}).Error("failed to process dataset CSV file")
			p.stats.FilesSkipped++
			continue
		}
		p.stats.FilesProcessed++
	}

	// Finalize statistics with local time
	p.stats.EndTime = time.Now()

	// Generate and save markdown report
	if err := p.generateMarkdownReport(); err != nil {
		log.WithFields(log.Fields{"error": err}).Error("failed to generate markdown report")
		// Don't return error here as the processing was successful
	}

	return nil
}

// findDatasetCSVFiles finds all CSV files in the dataset directory
func (p *Processor) findDatasetCSVFiles() ([]string, error) {
	var csvFiles []string

	err := filepath.WalkDir(p.config.DatasetDir, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}

		if !d.IsDir() && strings.HasSuffix(strings.ToLower(path), ".csv") {
			csvFiles = append(csvFiles, path)
		}

		return nil
	})

	return csvFiles, err
}

// sortFilesByDate sorts CSV files by their date (extracted from filename or file creation date) in ascending order.
func (p *Processor) sortFilesByDate(files []string) ([]FileInfo, error) {
	var fileInfos []FileInfo
	for _, file := range files {
		// Extract full date with fallback to file creation date
		date, err := parser.ParseDateFromFilenameWithFallback(file)
		if err != nil {
			return nil, fmt.Errorf("could not extract date from filename or file info %s: %w", file, err)
		}

		// Keep year for display and backward compatibility
		year := date.Year()

		fileInfos = append(fileInfos, FileInfo{
			Path: file,
			Year: year,
			Date: date,
		})
	}

	// Sort by date in ascending order (oldest first)
	sort.Slice(fileInfos, func(i, j int) bool {
		return fileInfos[i].Date.Before(fileInfos[j].Date)
	})

	return fileInfos, nil
}

// processDatasetCSVFile processes a single dataset CSV file
func (p *Processor) processDatasetCSVFile(ctx context.Context, csvFile string, limit int) error {
	// Extract date with fallback for any dataset version
	date, err := parser.ParseDateFromFilenameWithFallback(csvFile)
	if err != nil {
		return fmt.Errorf("could not extract date from file %s: %w", csvFile, err)
	}
	year := date.Year()
	isRecent := p.isRecentDataset(date)

	log.WithFields(log.Fields{
		"file":     csvFile,
		"date":     date.Format("2006-01-02"),
		"year":     year,
		"isRecent": isRecent,
	}).Info("extracted date from file")

	// Initialize file statistics
	fileStats := &FileStats{
		FileName: filepath.Base(csvFile),
		Year:     year,
		Date:     date,
	}

	p.stats.FileStats[csvFile] = fileStats

	restaurants, err := p.parseHistoricalCSV(csvFile)
	if err != nil {
		return fmt.Errorf("failed to parse dataset CSV file: %w", err)
	}

	fileStats.RestaurantsFound = len(restaurants)
	// Only count unique restaurants in total, not the same restaurant across multiple files
	for _, restaurant := range restaurants {
		key := restaurant.URL // Use URL as unique identifier
		if key == "" {
			key = restaurant.Name + "|" + restaurant.Address // Fallback for restaurants without URL
		}
		if !p.stats.UniqueRestaurants[key] {
			p.stats.UniqueRestaurants[key] = true
			p.stats.TotalRestaurantsFound++
		}
	}

	log.WithFields(log.Fields{
		"file":     csvFile,
		"count":    len(restaurants),
		"isRecent": isRecent,
	}).Info("parsed dataset restaurants")

	// Phase 1: Process restaurants in the CSV (apply limit if set)
	restaurantsInCSV := make(map[string]bool) // Track which restaurants are in this CSV
	restaurantsToProcess := restaurants
	if limit > 0 && len(restaurants) > limit {
		restaurantsToProcess = restaurants[:limit]
		log.WithFields(log.Fields{
			"limit":         limit,
			"total_in_file": len(restaurants),
			"processing":    len(restaurantsToProcess),
		}).Info("applying limit for testing - processing subset of restaurants")
	}

	for _, restaurant := range restaurantsToProcess {
		// Track restaurants in CSV for recent file processing
		if isRecent {
			if restaurant.WebsiteURL != "" {
				restaurantsInCSV[restaurant.WebsiteURL] = true
			}
			if restaurant.URL != "" {
				restaurantsInCSV[restaurant.URL] = true
			}
		}

		if err := p.processHistoricalRestaurant(ctx, restaurant, year, fileStats); err != nil {
			log.WithFields(log.Fields{
				"restaurant": restaurant.Name,
				"error":      err,
			}).Error("failed to process restaurant from dataset")
			fileStats.ProcessingErrors++
			p.stats.ProcessingErrors++
			continue
		}
	}

	// Phase 2: For recent CSV files, mark missing restaurants as no longer in guide
	if isRecent {
		if err := p.updateMissingRestaurants(ctx, restaurantsInCSV, fileStats); err != nil {
			log.WithFields(log.Fields{
				"error": err,
			}).Error("failed to update missing restaurants for recent CSV")
			// Don't return error, this is non-critical
		}
	}

	log.WithFields(log.Fields{
		"file":                 csvFile,
		"restaurants_found":    fileStats.RestaurantsFound,
		"new_restaurants":      fileStats.NewRestaurants,
		"existing_restaurants": fileStats.ExistingRestaurants,
		"awards_added":         fileStats.AwardsAdded,
		"awards_skipped":       fileStats.AwardsSkipped,
		"processing_errors":    fileStats.ProcessingErrors,
		"removed_from_guide":   fileStats.RestaurantsRemovedFromGuide,
		"isRecent":             isRecent,
	}).Info("completed processing dataset CSV file")

	return nil
}

// parseHistoricalCSV parses a historical CSV file and returns restaurant records
func (p *Processor) parseHistoricalCSV(csvFile string) ([]HistoricalRestaurant, error) {
	file, err := os.Open(csvFile)
	if err != nil {
		return nil, fmt.Errorf("failed to open CSV file: %w", err)
	}
	defer file.Close()

	reader := csv.NewReader(file)
	records, err := reader.ReadAll()
	if err != nil {
		return nil, fmt.Errorf("failed to read CSV file: %w", err)
	}

	if len(records) == 0 {
		return nil, fmt.Errorf("CSV file is empty")
	}

	// Expected header: Name,Address,Location,Price,Type,Longitude,Latitude,PhoneNumber,Url,WebsiteUrl,Classification
	var restaurants []HistoricalRestaurant
	for i, record := range records[1:] { // Skip header
		if len(record) < 11 {
			log.WithFields(log.Fields{
				"line":   i + 2,
				"record": record,
			}).Warn("skipping CSV row with insufficient columns")
			p.stats.SkippedRecords++
			continue
		}

		restaurant := HistoricalRestaurant{
			Name:           strings.TrimSpace(record[0]),
			Address:        strings.TrimSpace(record[1]),
			Location:       strings.TrimSpace(record[2]),
			Price:          strings.TrimSpace(record[3]),
			Type:           strings.TrimSpace(record[4]),
			Longitude:      strings.TrimSpace(record[5]),
			Latitude:       strings.TrimSpace(record[6]),
			PhoneNumber:    strings.TrimSpace(record[7]),
			URL:            strings.TrimSpace(record[8]),
			WebsiteURL:     strings.TrimSpace(record[9]),
			Classification: strings.TrimSpace(record[10]),
		}

		// Skip restaurants with empty essential fields
		if restaurant.Name == "" || restaurant.URL == "" {
			log.WithFields(log.Fields{
				"line": i + 2,
				"name": restaurant.Name,
				"url":  restaurant.URL,
			}).Warn("skipping restaurant with empty name or URL")
			p.stats.SkippedRecords++
			continue
		}

		restaurants = append(restaurants, restaurant)
	}

	return restaurants, nil
}

// processHistoricalRestaurant processes a single historical restaurant record
func (p *Processor) processHistoricalRestaurant(ctx context.Context, restaurant HistoricalRestaurant, year int, fileStats *FileStats) error {
	// Determine if this CSV represents recent/current data
	fileDate := fileStats.Date
	isRecent := p.isRecentDataset(fileDate)

	// Check current status in SQLite database (source of truth)
	currentRestaurant, isCurrentlyInGuide, err := p.getCurrentGuideRestaurant(ctx, restaurant.WebsiteURL, restaurant.URL)
	if err != nil {
		return fmt.Errorf("failed to check current restaurant status: %w", err)
	}

	log.WithFields(log.Fields{
		"restaurant":       restaurant.Name,
		"isRecent":         isRecent,
		"foundInDB":        currentRestaurant != nil,
		"currentlyInGuide": isCurrentlyInGuide,
		"fileDate":         fileDate.Format("2006-01-02"),
	}).Debug("processing restaurant with new InGuide logic")

	if currentRestaurant != nil {
		// Restaurant exists in current database
		fileStats.ExistingRestaurants++
		// Only count unique existing restaurants in global stats
		key := restaurant.URL
		if key == "" {
			key = restaurant.Name + "|" + restaurant.Address
		}
		if !p.stats.UniqueRestaurants[key+"|existing"] {
			p.stats.UniqueRestaurants[key+"|existing"] = true
			p.stats.ExistingRestaurants++
		}
		return p.addHistoricalAward(ctx, currentRestaurant, restaurant, year, fileStats, isRecent, isCurrentlyInGuide)
	} else {
		// Restaurant doesn't exist in current database
		fileStats.NewRestaurants++
		p.stats.NewRestaurantsAdded++
		return p.createHistoricalRestaurant(ctx, restaurant, year, fileStats, isRecent)
	}
}

// addHistoricalAward adds a historical award to an existing restaurant
func (p *Processor) addHistoricalAward(ctx context.Context, existingRestaurant *models.Restaurant, restaurant HistoricalRestaurant, year int, fileStats *FileStats, isRecent bool, isCurrentlyInGuide bool) error {
	// Check if award already exists for this year
	var existing models.RestaurantAward
	err := p.repository.(*storage.SQLiteRepository).GetDB().WithContext(ctx).Where("restaurant_id = ? AND year = ?", existingRestaurant.ID, year).First(&existing).Error

	if err == nil {
		log.WithFields(log.Fields{
			"restaurant_id": existingRestaurant.ID,
			"year":          year,
		}).Debug("award already exists for this year, skipping")
		fileStats.AwardsSkipped++
		p.stats.AwardsSkipped++
		return nil
	}

	if err != gorm.ErrRecordNotFound {
		return fmt.Errorf("failed to check existing award: %w", err)
	}

	// Fix NULL in_guide fields for existing restaurants
	// If in_guide is NULL and this is a restaurant that should be in guide, set it to true
	if existingRestaurant.InGuide == false && isCurrentlyInGuide {
		// Check if in_guide is actually NULL by querying the database directly
		var inGuideValue *bool
		err := p.repository.(*storage.SQLiteRepository).GetDB().WithContext(ctx).
			Model(&models.Restaurant{}).
			Where("id = ?", existingRestaurant.ID).
			Select("in_guide").
			Scan(&inGuideValue).Error

		if err == nil && inGuideValue == nil {
			// in_guide is NULL, set it to true for restaurants that should be in guide
			err = p.repository.(*storage.SQLiteRepository).GetDB().WithContext(ctx).
				Model(&models.Restaurant{}).
				Where("id = ?", existingRestaurant.ID).
				Update("in_guide", true).Error

			if err != nil {
				log.WithFields(log.Fields{
					"restaurant_id": existingRestaurant.ID,
					"error":         err,
				}).Warn("failed to update NULL in_guide to true")
			} else {
				log.WithFields(log.Fields{
					"restaurant_id": existingRestaurant.ID,
					"restaurant":    existingRestaurant.Name,
				}).Info("fixed NULL in_guide field - set to true")
			}
		}
	}

	// Create the award ONLY - do NOT touch the restaurant record at all
	distinction := p.parseClassification(restaurant.Classification)
	award := &models.RestaurantAward{
		RestaurantID: existingRestaurant.ID,
		Year:         year,
		Distinction:  distinction,
		Price:        p.parsePrice(restaurant.Price),
		GreenStar:    false, // Historical data doesn't have green star info
	}

	// Use SaveAward directly instead of UpsertRestaurantWithAward to avoid touching restaurant record
	if err := p.repository.SaveAward(ctx, award); err != nil {
		return fmt.Errorf("failed to save award: %w", err)
	}

	// Update statistics
	fileStats.AwardsAdded++
	p.stats.AwardsAdded++
	p.stats.AwardStats[distinction]++

	log.WithFields(log.Fields{
		"restaurant":       existingRestaurant.Name,
		"year":             year,
		"award":            award.Distinction,
		"currentlyInGuide": isCurrentlyInGuide,
		"csvIsRecent":      isRecent,
		"preservedInGuide": existingRestaurant.InGuide,
		"action":           "added award only - restaurant InGuide status preserved",
	}).Info("added award to existing restaurant without modifying InGuide status")

	return nil
}

// createHistoricalRestaurant creates a new restaurant from historical data
func (p *Processor) createHistoricalRestaurant(ctx context.Context, restaurant HistoricalRestaurant, year int, fileStats *FileStats, isRecent bool) error {
	// DOUBLE-CHECK: Make sure restaurant doesn't exist by URL to avoid overwriting
	var existingRestaurant models.Restaurant
	err := p.repository.(*storage.SQLiteRepository).GetDB().WithContext(ctx).Where("url = ?", restaurant.URL).First(&existingRestaurant).Error

	if err == nil {
		// Restaurant exists! This shouldn't happen, but if it does, treat it as existing
		log.WithFields(log.Fields{
			"restaurant": restaurant.Name,
			"url":        restaurant.URL,
			"existingID": existingRestaurant.ID,
			"warning":    "restaurant exists but was not found by getCurrentGuideRestaurant, adding award only",
		}).Warn("restaurant found by direct URL lookup, treating as existing")

		fileStats.ExistingRestaurants++
		p.stats.ExistingRestaurants++
		return p.addHistoricalAward(ctx, &existingRestaurant, restaurant, year, fileStats, isRecent, existingRestaurant.InGuide)
	}

	if err != gorm.ErrRecordNotFound {
		return fmt.Errorf("failed to double-check restaurant existence: %w", err)
	}

	// Confirmed new restaurant - create it
	distinction := p.parseClassification(restaurant.Classification)

	// Determine InGuide status based on CSV age
	var inGuide bool
	if isRecent {
		inGuide = true
		log.WithFields(log.Fields{
			"restaurant": restaurant.Name,
			"action":     "new restaurant from recent CSV",
			"inGuide":    true,
		}).Debug("setting InGuide=true for new restaurant from recent CSV")
	} else {
		inGuide = false
		log.WithFields(log.Fields{
			"restaurant": restaurant.Name,
			"action":     "new restaurant from historical CSV",
			"inGuide":    false,
		}).Debug("setting InGuide=false for new restaurant from historical CSV")
	}

	// Create restaurant record directly (not using UpsertRestaurantWithAward to avoid any conflicts)
	newRestaurant := models.Restaurant{
		URL:  restaurant.URL,
		Name: restaurant.Name,
		Description: fmt.Sprintf("Restaurant from %s dataset", func() string {
			if isRecent {
				return "current"
			} else {
				return "historical"
			}
		}()),
		Address:               restaurant.Address,
		Location:              restaurant.Location,
		Latitude:              restaurant.Latitude,
		Longitude:             restaurant.Longitude,
		Cuisine:               restaurant.Type,
		PhoneNumber:           restaurant.PhoneNumber,
		FacilitiesAndServices: "",
		WebsiteURL:            restaurant.WebsiteURL,
		InGuide:               inGuide,
	}

	// Handle missing required fields
	if newRestaurant.Latitude == "" {
		newRestaurant.Latitude = "0.0"
	}
	if newRestaurant.Longitude == "" {
		newRestaurant.Longitude = "0.0"
	}
	if newRestaurant.Cuisine == "" {
		newRestaurant.Cuisine = "Unknown"
	}

	// Create the restaurant directly
	if err := p.repository.(*storage.SQLiteRepository).GetDB().WithContext(ctx).Create(&newRestaurant).Error; err != nil {
		return fmt.Errorf("failed to create new restaurant: %w", err)
	}

	// Now create the award
	award := &models.RestaurantAward{
		RestaurantID: newRestaurant.ID,
		Year:         year,
		Distinction:  distinction,
		Price:        p.parsePrice(restaurant.Price),
		GreenStar:    false,
	}

	if err := p.repository.SaveAward(ctx, award); err != nil {
		return fmt.Errorf("failed to save award for new restaurant: %w", err)
	}

	// Update statistics
	fileStats.AwardsAdded++
	p.stats.AwardsAdded++
	p.stats.AwardStats[distinction]++

	log.WithFields(log.Fields{
		"restaurant": restaurant.Name,
		"year":       year,
		"award":      award.Distinction,
		"inGuide":    newRestaurant.InGuide,
		"id":         newRestaurant.ID,
		"source": func() string {
			if isRecent {
				return "recent"
			} else {
				return "historical"
			}
		}(),
	}).Info("created new restaurant from dataset with direct creation")

	return nil
}

// parseClassification converts historical classification to standard distinction
func (p *Processor) parseClassification(classification string) string {
	return parser.ParseDistinction(classification)
}

// parsePrice converts historical price format to standard price format
func (p *Processor) parsePrice(price string) string {
	// Historical prices are already in format like "225EUR", "65-130EUR", etc.
	// We can use this as-is or convert to standard format
	if price == "" {
		return "$" // Default price if empty
	}
	return price
}

// generateMarkdownReport creates a detailed markdown report of the dataset processing
func (p *Processor) generateMarkdownReport() error {
	// Create report filename with local timestamp
	reportFileName := fmt.Sprintf("data/dataset_processing_report_%s.md",
		p.stats.StartTime.Format("2006-01-02_15-04-05"))

	// Calculate processing duration
	duration := p.stats.EndTime.Sub(p.stats.StartTime)

	// Format duration nicely
	var durationStr string
	if duration.Hours() >= 1 {
		hours := int(duration.Hours())
		minutes := int(duration.Minutes()) % 60
		seconds := int(duration.Seconds()) % 60
		durationStr = fmt.Sprintf("%dh %dm %ds", hours, minutes, seconds)
	} else if duration.Minutes() >= 1 {
		minutes := int(duration.Minutes())
		seconds := int(duration.Seconds()) % 60
		durationStr = fmt.Sprintf("%dm %ds", minutes, seconds)
	} else {
		seconds := int(duration.Seconds())
		milliseconds := int(duration.Milliseconds()) % 1000
		if milliseconds > 0 {
			durationStr = fmt.Sprintf("%d.%03ds", seconds, milliseconds)
		} else {
			durationStr = fmt.Sprintf("%ds", seconds)
		}
	}

	// Build markdown content
	var report strings.Builder

	// Header
	report.WriteString("# Dataset Processing Report\n\n")
	report.WriteString("*Processing CSV dataset files (historical, current, or future versions)*\n\n")

	// Get user's local timezone - use the actual local time
	report.WriteString(fmt.Sprintf("**Generated:** %s\n", p.stats.EndTime.Format("2006-01-02 15:04:05 MST -0700")))
	report.WriteString(fmt.Sprintf("**Processing Duration:** %s\n\n", durationStr))

	// Overview
	report.WriteString("## 📊 Processing Overview\n\n")
	report.WriteString("| Metric | Count |\n")
	report.WriteString("|--------|-------|\n")
	report.WriteString(fmt.Sprintf("| Files Processed | %d |\n", p.stats.FilesProcessed))
	report.WriteString(fmt.Sprintf("| Files Skipped | %d |\n", p.stats.FilesSkipped))
	report.WriteString(fmt.Sprintf("| Total Restaurants Found | %d |\n", p.stats.TotalRestaurantsFound))
	report.WriteString(fmt.Sprintf("| New Restaurants Added | %d |\n", p.stats.NewRestaurantsAdded))
	report.WriteString(fmt.Sprintf("| Existing Restaurants | %d |\n", p.stats.ExistingRestaurants))
	report.WriteString(fmt.Sprintf("| Awards Added | %d |\n", p.stats.AwardsAdded))
	report.WriteString(fmt.Sprintf("| Awards Skipped (duplicates) | %d |\n", p.stats.AwardsSkipped))
	report.WriteString(fmt.Sprintf("| Processing Errors | %d |\n", p.stats.ProcessingErrors))
	report.WriteString(fmt.Sprintf("| Records Skipped | %d |\n\n", p.stats.SkippedRecords))

	// File-by-file breakdown
	report.WriteString("## 📁 File Processing Details\n\n")
	report.WriteString("*Files processed in chronological order (oldest first)*\n\n")
	// Sort files chronologically for the report
	sortedFiles := make([]*FileStats, 0, len(p.stats.FileStats))
	for _, fileStats := range p.stats.FileStats {
		sortedFiles = append(sortedFiles, fileStats)
	}
	sort.Slice(sortedFiles, func(i, j int) bool {
		return sortedFiles[i].Date.Before(sortedFiles[j].Date)
	})

	for _, fileStats := range sortedFiles {
		report.WriteString(fmt.Sprintf("### %s (Year: %d, Date: %s)\n\n", fileStats.FileName, fileStats.Year, fileStats.Date.Format("2006-01-02")))
		report.WriteString("| Metric | Count |\n")
		report.WriteString("|--------|-------|\n")
		report.WriteString(fmt.Sprintf("| Restaurants Found | %d |\n", fileStats.RestaurantsFound))
		report.WriteString(fmt.Sprintf("| New Restaurants | %d |\n", fileStats.NewRestaurants))
		report.WriteString(fmt.Sprintf("| Existing Restaurants | %d |\n", fileStats.ExistingRestaurants))
		report.WriteString(fmt.Sprintf("| Awards Added | %d |\n", fileStats.AwardsAdded))
		report.WriteString(fmt.Sprintf("| Awards Skipped | %d |\n", fileStats.AwardsSkipped))
		report.WriteString(fmt.Sprintf("| Processing Errors | %d |\n", fileStats.ProcessingErrors))
		report.WriteString(fmt.Sprintf("| Records Skipped | %d |\n", fileStats.RestaurantsSkipped))
		if fileStats.RestaurantsRemovedFromGuide > 0 {
			report.WriteString(fmt.Sprintf("| Restaurants Removed from Guide | %d |\n", fileStats.RestaurantsRemovedFromGuide))
		}
		report.WriteString("\n")
	}

	// Awards breakdown
	if len(p.stats.AwardStats) > 0 {
		report.WriteString("## 🏆 Awards Distribution\n\n")
		report.WriteString("| Award Type | Count |\n")
		report.WriteString("|------------|-------|\n")
		for award, count := range p.stats.AwardStats {
			if award == "" {
				award = "No Award/Unknown"
			}
			report.WriteString(fmt.Sprintf("| %s | %d |\n", award, count))
		}
		report.WriteString("\n")
	}

	// Summary
	report.WriteString("## 📈 Summary\n\n")
	successRate := float64(p.stats.TotalRestaurantsFound-p.stats.ProcessingErrors-p.stats.SkippedRecords) / float64(p.stats.TotalRestaurantsFound) * 100
	report.WriteString(fmt.Sprintf("- **Success Rate:** %.2f%% of restaurants processed successfully\n", successRate))
	report.WriteString(fmt.Sprintf("- **New Restaurants:** %d restaurants added as dataset entries (InGuide=false for historical data)\n", p.stats.NewRestaurantsAdded))
	report.WriteString(fmt.Sprintf("- **Existing Restaurants:** %d restaurants found in current database\n", p.stats.ExistingRestaurants))
	report.WriteString(fmt.Sprintf("- **Awards Added:** %d new dataset awards added\n", p.stats.AwardsAdded))

	if p.stats.AwardsSkipped > 0 {
		report.WriteString(fmt.Sprintf("- **Duplicates Prevented:** %d awards skipped (same restaurant, same year)\n", p.stats.AwardsSkipped))
	}

	if p.stats.ProcessingErrors > 0 {
		report.WriteString(fmt.Sprintf("- **⚠️ Processing Errors:** %d restaurants failed to process\n", p.stats.ProcessingErrors))
	}

	report.WriteString("\n")
	report.WriteString("## 📝 Processing Notes\n\n")
	report.WriteString("- **Date Detection:** Dates extracted from filenames (YYYY-MM-DD pattern) or file creation date as fallback\n")
	report.WriteString("- **Chronological Order:** Files processed oldest first for proper historical progression\n")
	report.WriteString("- **Restaurant Matching:** Restaurants matched by website URL (primary) or Michelin Guide URL (fallback)\n")
	report.WriteString("- **Duplicate Prevention:** Awards for same restaurant and year are automatically skipped\n")
	report.WriteString("- **Future Support:** Can process historical, current, and future dataset versions\n")
	report.WriteString("- **InGuide Logic:** SQLite database is the source of truth for current guide status\n")
	report.WriteString("  - **Recent CSV files** (last month): New restaurants get InGuide=true + Michelin URL\n")
	report.WriteString("  - **Recent CSV files** (last month): Existing restaurants missing from CSV get InGuide=false\n")
	report.WriteString("  - **Recent CSV files** (last month): Existing restaurants in CSV keep InGuide=true + add awards\n")
	report.WriteString("  - **Historical CSV files** (older): New restaurants get InGuide=false, keep URL for reference\n")
	report.WriteString("  - **Historical CSV files** (older): Existing restaurants preserve InGuide status, only add awards\n\n")

	report.WriteString("---\n")
	report.WriteString("*Report generated by Michelin My Maps Dataset Processor*\n")

	// Write report to file
	if err := os.WriteFile(reportFileName, []byte(report.String()), 0644); err != nil {
		return fmt.Errorf("failed to write report file: %w", err)
	}

	log.WithFields(log.Fields{
		"report_file": reportFileName,
		"duration":    duration.String(),
	}).Info("markdown report generated successfully")

	return nil
}

// updateMissingRestaurants finds restaurants that are currently in guide but missing from recent CSV
// and marks them as no longer in guide (InGuide=0). Only applies to recent CSV files.
func (p *Processor) updateMissingRestaurants(ctx context.Context, restaurantsInCSV map[string]bool, fileStats *FileStats) error {
	// Get all restaurants currently marked as InGuide=1
	var currentGuideRestaurants []models.Restaurant
	err := p.repository.(*storage.SQLiteRepository).GetDB().WithContext(ctx).Where("in_guide = ?", true).Find(&currentGuideRestaurants).Error
	if err != nil {
		return fmt.Errorf("failed to fetch current guide restaurants: %w", err)
	}

	var restaurantsToUpdate []uint
	for _, restaurant := range currentGuideRestaurants {
		// Check if this restaurant is present in the recent CSV
		inCSV := false

		// Check by website URL first (most reliable)
		if restaurant.WebsiteURL != "" && restaurantsInCSV[restaurant.WebsiteURL] {
			inCSV = true
		}

		// Check by Michelin URL as fallback
		if !inCSV && restaurant.URL != "" && restaurantsInCSV[restaurant.URL] {
			inCSV = true
		}

		// If restaurant is not in the recent CSV, it's no longer in guide
		if !inCSV {
			restaurantsToUpdate = append(restaurantsToUpdate, restaurant.ID)
			log.WithFields(log.Fields{
				"restaurant":  restaurant.Name,
				"id":          restaurant.ID,
				"websiteURL":  restaurant.WebsiteURL,
				"michelinURL": restaurant.URL,
			}).Debug("restaurant no longer in recent CSV, will mark as InGuide=false")
		}
	}

	// Batch update restaurants to InGuide=false
	if len(restaurantsToUpdate) > 0 {
		err = p.repository.(*storage.SQLiteRepository).GetDB().WithContext(ctx).
			Model(&models.Restaurant{}).
			Where("id IN ?", restaurantsToUpdate).
			Update("in_guide", false).Error

		if err != nil {
			return fmt.Errorf("failed to update restaurants as no longer in guide: %w", err)
		}

		log.WithFields(log.Fields{
			"count": len(restaurantsToUpdate),
		}).Info("marked restaurants as no longer in guide (missing from recent CSV)")

		// Update statistics (these restaurants are now out of guide)
		fileStats.RestaurantsRemovedFromGuide += len(restaurantsToUpdate)
	} else {
		log.Debug("no restaurants need to be marked as no longer in guide")
	}

	return nil
}
