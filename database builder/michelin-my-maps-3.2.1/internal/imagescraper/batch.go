package imagescraper

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/ngshiheng/michelin-my-maps/v3/internal/models"
	"github.com/ngshiheng/michelin-my-maps/v3/internal/storage"
	log "github.com/sirupsen/logrus"
)

// BatchResult represents the result of processing a single restaurant
type BatchResult struct {
	RestaurantID   uint
	RestaurantName string
	RestaurantURL  string
	ImageURL       string
	Success        bool
	Error          string
}

// BatchReport represents the overall batch processing report
type BatchReport struct {
	TotalRestaurants int
	Successful       int
	Failed           int
	Skipped          int
	SuccessRate      float64
	StartTime        time.Time
	EndTime          time.Time
	Duration         time.Duration
	Results          []BatchResult
}

// BatchImageScraper handles batch processing of restaurant images
type BatchImageScraper struct {
	repo    storage.RestaurantRepository
	scraper *ImageScraper
}

// NewBatchImageScraper creates a new batch image scraper
func NewBatchImageScraper(repo storage.RestaurantRepository) *BatchImageScraper {
	return &BatchImageScraper{
		repo:    repo,
		scraper: New(repo),
	}
}

// ProcessAllRestaurants processes all restaurants in the database
func (bis *BatchImageScraper) ProcessAllRestaurants(ctx context.Context, limit int, retryFailed bool) (*BatchReport, error) {
	// Get all restaurants with URLs
	restaurants, err := bis.repo.ListAllRestaurantsWithURL()
	if err != nil {
		return nil, fmt.Errorf("failed to list restaurants: %w", err)
	}

	log.WithField("total_restaurants", len(restaurants)).Info("Starting batch image processing")

	report := &BatchReport{
		TotalRestaurants: len(restaurants),
		StartTime:        time.Now(),
		Results:          make([]BatchResult, 0, len(restaurants)),
	}

	processedCount := 0
	for i, restaurant := range restaurants {
		log.WithFields(log.Fields{
			"restaurant_id":   restaurant.ID,
			"restaurant_name": restaurant.Name,
			"restaurant_url":  restaurant.URL,
			"progress":        fmt.Sprintf("%d/%d", i+1, len(restaurants)),
		}).Info("Processing restaurant")

		result := bis.processRestaurant(ctx, restaurant, retryFailed)
		report.Results = append(report.Results, result)

		if result.Success {
			if strings.Contains(result.Error, "skipped") {
				report.Skipped++
			} else {
				report.Successful++
				processedCount++
			}
		} else {
			if strings.Contains(result.Error, "skipped") {
				report.Skipped++
			} else {
				report.Failed++
				processedCount++
			}
		}

		// Add a small delay to be respectful to the server (only for actual scraping)
		if !strings.Contains(result.Error, "skipped") {
			time.Sleep(2 * time.Second)
		}

		// Check if we've reached the limit for processed restaurants
		if limit > 0 && processedCount >= limit {
			log.WithFields(log.Fields{
				"processed_count": processedCount,
				"limit":           limit,
				"total_checked":   i + 1,
			}).Info("Reached processing limit, stopping")
			break
		}
	}

	report.EndTime = time.Now()
	report.Duration = report.EndTime.Sub(report.StartTime)
	report.SuccessRate = float64(report.Successful) / float64(report.TotalRestaurants) * 100

	log.WithFields(log.Fields{
		"total":        report.TotalRestaurants,
		"successful":   report.Successful,
		"failed":       report.Failed,
		"skipped":      report.Skipped,
		"success_rate": fmt.Sprintf("%.2f%%", report.SuccessRate),
		"duration":     report.Duration,
	}).Info("Batch processing completed")

	return report, nil
}

// processRestaurant processes a single restaurant
func (bis *BatchImageScraper) processRestaurant(ctx context.Context, restaurant models.Restaurant, retryFailed bool) BatchResult {
	result := BatchResult{
		RestaurantID:   restaurant.ID,
		RestaurantName: restaurant.Name,
		RestaurantURL:  restaurant.URL,
	}

	// Skip if already has a successful image URL
	if restaurant.ImageURL != "" && restaurant.ImageURL != "failed" {
		log.WithField("restaurant_id", restaurant.ID).Debug("Restaurant already has image URL, skipping")
		result.ImageURL = restaurant.ImageURL
		result.Success = true
		result.Error = "skipped - already processed"
		return result
	}

	// Skip if already marked as failed (unless we want to retry)
	if restaurant.ImageURL == "failed" && !retryFailed {
		log.WithField("restaurant_id", restaurant.ID).Debug("Restaurant already marked as failed, skipping")
		result.ImageURL = "failed"
		result.Success = false
		result.Error = "skipped - previously failed"
		return result
	}

	// Try to scrape the image URL
	imageURL, err := bis.scraper.ScrapeImageURL(ctx, restaurant.URL)
	if err != nil {
		log.WithError(err).WithField("restaurant_id", restaurant.ID).Error("Failed to scrape image URL")
		result.ImageURL = "failed"
		result.Success = false
		result.Error = err.Error()
	} else {
		result.ImageURL = imageURL
		result.Success = true
	}

	// Update the restaurant in the database
	restaurant.ImageURL = result.ImageURL
	if err := bis.repo.SaveRestaurant(ctx, &restaurant); err != nil {
		log.WithError(err).WithField("restaurant_id", restaurant.ID).Error("Failed to save restaurant")
		result.Error = fmt.Sprintf("scraping: %s, saving: %s", result.Error, err.Error())
	}

	return result
}

// GenerateMarkdownReport generates a markdown report from the batch results
func (br *BatchReport) GenerateMarkdownReport() string {
	// Format duration nicely
	duration := br.Duration
	minutes := int(duration.Minutes())
	seconds := int(duration.Seconds()) % 60
	durationStr := fmt.Sprintf("%dm %ds", minutes, seconds)

	// Calculate processed fraction
	processed := br.Successful + br.Failed + br.Skipped
	processedFraction := float64(processed) / float64(br.TotalRestaurants) * 100

	// Set timezone to EDT for report timestamps
	edt := time.FixedZone("EDT", -4*60*60) // EDT is UTC-4
	startTimeEDT := br.StartTime.In(edt)
	endTimeEDT := br.EndTime.In(edt)

	report := fmt.Sprintf(`# Restaurant Image Scraping Report

## Summary
- **Total Restaurants in Database**: %d
- **Processed (Downloaded + Skipped)**: %d (%.1f%% of total)
- **Successful**: %d
- **Failed**: %d
- **Skipped**: %d
- **Success Rate**: %.2f%%
- **Start Time (EDT)**: %s
- **End Time (EDT)**: %s
- **Duration**: %s

## Detailed Results

### Successful Scrapes (%d)
`, br.TotalRestaurants, processed, processedFraction, br.Successful, br.Failed, br.Skipped, br.SuccessRate,
		startTimeEDT.Format("2006-01-02 15:04:05"),
		endTimeEDT.Format("2006-01-02 15:04:05"),
		durationStr, br.Successful)

	// Add successful results (excluding skipped)
	for _, result := range br.Results {
		if result.Success && !strings.Contains(result.Error, "skipped") {
			report += fmt.Sprintf("- **%s** (ID: %d) - [%s](%s)\n",
				result.RestaurantName, result.RestaurantID, result.ImageURL, result.RestaurantURL)
		}
	}

	report += fmt.Sprintf("\n### Failed Scrapes (%d)\n", br.Failed)

	// Add failed results (excluding skipped)
	for _, result := range br.Results {
		if !result.Success && !strings.Contains(result.Error, "skipped") {
			report += fmt.Sprintf("- **%s** (ID: %d) - [%s](%s) - Error: %s\n",
				result.RestaurantName, result.RestaurantID, result.RestaurantURL, result.RestaurantURL, result.Error)
		}
	}

	return report
}
