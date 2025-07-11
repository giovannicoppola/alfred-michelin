package storage

import (
	"context"
	"fmt"
	"time"

	"github.com/ngshiheng/michelin-my-maps/v3/internal/models"
	log "github.com/sirupsen/logrus"
	"gorm.io/driver/sqlite"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
	"gorm.io/gorm/logger"
)

// SQLiteRepository implements RestaurantRepository using SQLite database.
type SQLiteRepository struct {
	db *gorm.DB
}

// NewSQLiteRepository creates a new SQLite repository instance.
func NewSQLiteRepository(dbPath string) (*SQLiteRepository, error) {
	dsn := fmt.Sprintf("%s?_loc=UTC", dbPath)
	db, err := gorm.Open(sqlite.Open(dsn), &gorm.Config{
		PrepareStmt: true,
		Logger:      logger.Default.LogMode(logger.Silent), // Disable GORM logging
		NowFunc: func() time.Time {
			return time.Now().UTC() // Force UTC timestamps
		},
	})
	if err != nil {
		return nil, fmt.Errorf("failed to connect to database: %w", err)
	}

	// Get the generic database object sql.DB to use its functions
	sqlDB, err := db.DB()
	if err != nil {
		return nil, fmt.Errorf("failed to get database object: %w", err)
	}

	// Set PRAGMA statements for better performance
	pragmas := []string{
		"PRAGMA journal_mode = WAL;",
		"PRAGMA synchronous = NORMAL;",
		"PRAGMA cache_size = 10000;",
		"PRAGMA temp_store = MEMORY;",
	}

	for _, pragma := range pragmas {
		if _, err := sqlDB.Exec(pragma); err != nil {
			return nil, fmt.Errorf("failed to execute %s: %w", pragma, err)
		}
	}

	// Auto-migrate the Restaurant and RestaurantAward models
	if err := db.AutoMigrate(&models.Restaurant{}, &models.RestaurantAward{}); err != nil {
		return nil, fmt.Errorf("failed to auto-migrate models: %w", err)
	}

	return &SQLiteRepository{db: db}, nil
}

// SaveRestaurant saves a restaurant to the database.
func (r *SQLiteRepository) SaveRestaurant(ctx context.Context, restaurant *models.Restaurant) error {
	log.WithFields(log.Fields{
		"id":  restaurant.ID,
		"url": restaurant.URL,
	}).Debug("upserting restaurant")

	return r.db.WithContext(ctx).Clauses(clause.OnConflict{
		Columns: []clause.Column{{Name: "url"}},
		DoUpdates: clause.AssignmentColumns([]string{
			"name", "description", "address", "location",
			"latitude", "longitude", "cuisine",
			"facilities_and_services", "phone_number", "website_url",
			"in_guide", "updated_at",
		}),
	}).Create(restaurant).Error
}

/*
SaveAward upserts an award for (restaurant_id, year).
If a record exists, it updates distinction, price, greenStar, and updated_at.
*/
func (r *SQLiteRepository) SaveAward(ctx context.Context, award *models.RestaurantAward) error {
	var existing models.RestaurantAward
	err := r.db.WithContext(ctx).Where("restaurant_id = ? AND year = ?", award.RestaurantID, award.Year).First(&existing).Error
	if err == nil {
		// Existing row found
		if existing.WaybackURL == "" && award.WaybackURL != "" {
			// Existing is from scrape, incoming is from backfill: skip update
			log.WithFields(log.Fields{
				"restaurant_id": award.RestaurantID,
				"year":          award.Year,
			}).Debug("skipping backfill overwrite of fresher scrape data")
			return nil
		}
	}
	// If not found or not a scrape/backfill conflict, proceed with upsert
	return r.db.WithContext(ctx).Clauses(clause.OnConflict{
		Columns:   []clause.Column{{Name: "restaurant_id"}, {Name: "year"}},
		DoUpdates: clause.AssignmentColumns([]string{"distinction", "price", "green_star", "wayback_url", "updated_at"}),
	}).Create(award).Error
}

func (r *SQLiteRepository) FindRestaurantByURL(ctx context.Context, url string) (*models.Restaurant, error) {
	var restaurant models.Restaurant
	err := r.db.WithContext(ctx).Where("url = ?", url).First(&restaurant).Error
	if err != nil {
		return nil, err
	}
	return &restaurant, nil
}

func (r *SQLiteRepository) FindRestaurantByWebsiteURL(ctx context.Context, websiteURL string) (*models.Restaurant, error) {
	var restaurant models.Restaurant
	err := r.db.WithContext(ctx).Where("website_url = ? AND website_url != ''", websiteURL).First(&restaurant).Error
	if err != nil {
		return nil, err
	}
	return &restaurant, nil
}

/*
UpsertRestaurantWithAward creates or updates a restaurant and its award for the explicit year provided in data.Year.
If data.Year is zero or invalid, the award upsert is skipped and a warning is logged.
*/
func (r *SQLiteRepository) UpsertRestaurantWithAward(ctx context.Context, data RestaurantData) error {
	log.WithFields(log.Fields{
		"url":         data.URL,
		"distinction": data.Distinction,
		"year":        data.Year,
		"inGuide": func() interface{} {
			if data.InGuide != nil {
				return *data.InGuide
			} else {
				return "nil"
			}
		}(),
	}).Debug("processing restaurant data")

	// Handle InGuide pointer - if nil, use database default, otherwise use explicit value
	var inGuide bool
	if data.InGuide != nil {
		inGuide = *data.InGuide
		log.WithFields(log.Fields{
			"explicit_inGuide": inGuide,
		}).Debug("using explicit InGuide value")
	} else {
		inGuide = true // Default if not specified
		log.Debug("using default InGuide=true (not specified)")
	}

	// Check if restaurant already exists
	var existingRestaurant models.Restaurant
	err := r.db.WithContext(ctx).Where("url = ?", data.URL).First(&existingRestaurant).Error

	var restaurant models.Restaurant

	if err == gorm.ErrRecordNotFound {
		// Restaurant doesn't exist - create new one
		log.WithFields(log.Fields{
			"url":     data.URL,
			"inGuide": inGuide,
		}).Debug("restaurant not found - creating new one with explicit InGuide")

		restaurant = models.Restaurant{
			URL:                   data.URL,
			Name:                  data.Name,
			Description:           data.Description,
			Address:               data.Address,
			Location:              data.Location,
			Latitude:              data.Latitude,
			Longitude:             data.Longitude,
			Cuisine:               data.Cuisine,
			FacilitiesAndServices: data.FacilitiesAndServices,
			PhoneNumber:           data.PhoneNumber,
			WebsiteURL:            data.WebsiteURL,
			InGuide:               inGuide,
		}

		// Direct create (not upsert) to avoid any default value conflicts
		if err := r.db.WithContext(ctx).Create(&restaurant).Error; err != nil {
			return fmt.Errorf("failed to create restaurant: %w", err)
		}

		log.WithFields(log.Fields{
			"url":     restaurant.URL,
			"id":      restaurant.ID,
			"inGuide": restaurant.InGuide,
		}).Debug("new restaurant created successfully")

	} else if err != nil {
		return fmt.Errorf("failed to check if restaurant exists: %w", err)
	} else {
		// Restaurant exists - update it
		log.WithFields(log.Fields{
			"url":              data.URL,
			"existing_inGuide": existingRestaurant.InGuide,
			"new_inGuide":      inGuide,
		}).Debug("restaurant exists - updating")

		restaurant = existingRestaurant
		restaurant.Name = data.Name
		restaurant.Description = data.Description
		restaurant.Address = data.Address
		restaurant.Location = data.Location
		restaurant.Latitude = data.Latitude
		restaurant.Longitude = data.Longitude
		restaurant.Cuisine = data.Cuisine
		restaurant.FacilitiesAndServices = data.FacilitiesAndServices
		restaurant.PhoneNumber = data.PhoneNumber
		restaurant.WebsiteURL = data.WebsiteURL
		restaurant.InGuide = inGuide

		if err := r.db.WithContext(ctx).Save(&restaurant).Error; err != nil {
			return fmt.Errorf("failed to update restaurant: %w", err)
		}

		log.WithFields(log.Fields{
			"url":     restaurant.URL,
			"id":      restaurant.ID,
			"inGuide": restaurant.InGuide,
		}).Debug("existing restaurant updated successfully")
	}

	if data.Year <= 0 {
		log.WithFields(log.Fields{
			"url":  data.URL,
			"note": "award year missing or invalid, skipping award upsert",
		}).Warn("Skipping award upsert due to invalid year")
		return nil
	}

	award := &models.RestaurantAward{
		RestaurantID: restaurant.ID,
		Year:         data.Year,
		Distinction:  data.Distinction,
		Price:        data.Price,
		GreenStar:    data.GreenStar,
		WaybackURL:   data.WaybackURL,
	}
	return r.SaveAward(ctx, award)
}

// Keep only one ListAllRestaurantsWithURL implementation
func (r *SQLiteRepository) ListAllRestaurantsWithURL() ([]models.Restaurant, error) {
	var restaurants []models.Restaurant
	err := r.db.Where("url != ''").Find(&restaurants).Error
	return restaurants, err
}

// GetDB returns the underlying database connection for advanced queries
func (r *SQLiteRepository) GetDB() *gorm.DB {
	return r.db
}
