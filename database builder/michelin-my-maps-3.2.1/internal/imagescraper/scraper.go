package imagescraper

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"

	"compress/gzip"

	"github.com/PuerkitoBio/goquery"
	"github.com/ngshiheng/michelin-my-maps/v3/internal/storage"
	log "github.com/sirupsen/logrus"
)

// ImageScraper handles scraping of restaurant images from Michelin Guide pages
type ImageScraper struct {
	repo   storage.RestaurantRepository
	client *http.Client
}

// New creates a new ImageScraper instance
func New(repo storage.RestaurantRepository) *ImageScraper {
	return &ImageScraper{
		repo: repo,
		client: &http.Client{
			Timeout: 30 * time.Second,
		},
	}
}

// ScrapeImageURL extracts the main image URL from a Michelin restaurant page
func (is *ImageScraper) ScrapeImageURL(ctx context.Context, restaurantURL string) (string, error) {
	log.WithField("url", restaurantURL).Debug("Scraping image URL from restaurant page")

	// Make HTTP request
	req, err := http.NewRequestWithContext(ctx, "GET", restaurantURL, nil)
	if err != nil {
		return "", fmt.Errorf("failed to create request: %w", err)
	}

	// Set headers to mimic a real browser (do NOT set Accept-Encoding)
	req.Header.Set("User-Agent", "Mozilla/5.0 (Macintosh; Intel Mac OS X 10_15_7) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/91.0.4472.124 Safari/537.36")
	req.Header.Set("Accept", "text/html,application/xhtml+xml,application/xml;q=0.9,image/webp,*/*;q=0.8")
	req.Header.Set("Accept-Language", "en-US,en;q=0.5")
	req.Header.Set("Connection", "keep-alive")
	req.Header.Set("Upgrade-Insecure-Requests", "1")

	resp, err := is.client.Do(req)
	if err != nil {
		return "", fmt.Errorf("failed to fetch webpage: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != 200 {
		return "", fmt.Errorf("failed to fetch webpage: %d %s", resp.StatusCode, resp.Status)
	}

	// Handle gzip encoding if present
	var reader io.ReadCloser
	switch resp.Header.Get("Content-Encoding") {
	case "gzip":
		reader, err = gzip.NewReader(resp.Body)
		if err != nil {
			return "", fmt.Errorf("failed to create gzip reader: %w", err)
		}
		defer reader.Close()
	default:
		reader = resp.Body
	}

	// Load the HTML document
	doc, err := goquery.NewDocumentFromReader(reader)
	if err != nil {
		return "", fmt.Errorf("failed to parse HTML: %w", err)
	}

	// Try multiple selectors to find the correct image URL
	var imageURL string

	// Check meta tags first (most reliable for this site)
	doc.Find("meta[property='og:image']").Each(func(i int, s *goquery.Selection) {
		content, exists := s.Attr("content")
		if exists && strings.Contains(content, "axwwgrkdco.cloudimg.io") {
			imageURL = content
			return
		}
	})

	// If not found in meta tags, try looking in other places
	if imageURL == "" {
		// Try data attributes that typically hold the main restaurant image
		doc.Find("[data-ci-bg-url]").Each(func(i int, s *goquery.Selection) {
			src, exists := s.Attr("data-ci-bg-url")
			if exists && strings.Contains(src, "axwwgrkdco.cloudimg.io") {
				imageURL = src
				return
			}
		})
	}

	// If still not found, check any img tags
	if imageURL == "" {
		doc.Find("img").Each(func(i int, s *goquery.Selection) {
			src, exists := s.Attr("src")
			if exists && strings.Contains(src, "axwwgrkdco.cloudimg.io") {
				imageURL = src
				return
			}
		})
	}

	if imageURL == "" {
		return "", fmt.Errorf("no image found with the specified URL prefix for %s", restaurantURL)
	}

	// Clean the URL (removing any query parameters if needed)
	if strings.Contains(imageURL, "?") {
		imageURL = strings.Split(imageURL, "?")[0]
	}

	log.WithFields(log.Fields{
		"restaurant_url": restaurantURL,
		"image_url":      imageURL,
	}).Debug("Successfully extracted image URL")

	return imageURL, nil
}

// ScrapeAndSaveImageURL scrapes the image URL and saves it to the restaurant record
func (is *ImageScraper) ScrapeAndSaveImageURL(ctx context.Context, restaurantURL string) error {
	// Scrape the image URL first
	imageURL, err := is.ScrapeImageURL(ctx, restaurantURL)
	if err != nil {
		return fmt.Errorf("failed to scrape image URL for %s: %w", restaurantURL, err)
	}

	// Try to find the restaurant in the database
	restaurant, err := is.repo.FindRestaurantByURL(ctx, restaurantURL)
	if err != nil {
		// Restaurant not found in database - just log the image URL
		log.WithFields(log.Fields{
			"restaurant_url": restaurantURL,
			"image_url":      imageURL,
		}).Warn("Restaurant not found in database, but image URL extracted successfully")

		// You could optionally create a minimal restaurant record here
		// For now, we'll just return success since we got the image URL
		return nil
	}

	// Update the restaurant with the image URL
	restaurant.ImageURL = imageURL
	if err := is.repo.SaveRestaurant(ctx, restaurant); err != nil {
		return fmt.Errorf("failed to save restaurant with image URL: %w", err)
	}

	log.WithFields(log.Fields{
		"restaurant_name": restaurant.Name,
		"restaurant_url":  restaurantURL,
		"image_url":       imageURL,
	}).Info("Successfully saved image URL for restaurant")

	return nil
}

// ScrapeImagesFromFile scrapes images for restaurants listed in a file
func (is *ImageScraper) ScrapeImagesFromFile(ctx context.Context, filePath string) error {
	// Read URLs from file
	urls, err := readURLsFromFile(filePath)
	if err != nil {
		return fmt.Errorf("failed to read URLs from file: %w", err)
	}

	log.WithField("count", len(urls)).Info("Starting to scrape images for restaurants from file")

	for i, url := range urls {
		log.WithFields(log.Fields{
			"url":   url,
			"index": i + 1,
			"total": len(urls),
		}).Info("Processing restaurant")

		if err := is.ScrapeAndSaveImageURL(ctx, url); err != nil {
			log.WithError(err).WithField("url", url).Error("Failed to scrape image for restaurant")
			continue
		}

		// Add a small delay to be respectful to the server
		time.Sleep(2 * time.Second)
	}

	return nil
}

// ScrapeImagesFromURLs scrapes images for a list of restaurant URLs
func (is *ImageScraper) ScrapeImagesFromURLs(ctx context.Context, urls []string) error {
	log.WithField("count", len(urls)).Info("Starting to scrape images for restaurants")

	for i, url := range urls {
		log.WithFields(log.Fields{
			"url":   url,
			"index": i + 1,
			"total": len(urls),
		}).Info("Processing restaurant")

		if err := is.ScrapeAndSaveImageURL(ctx, url); err != nil {
			log.WithError(err).WithField("url", url).Error("Failed to scrape image for restaurant")
			continue
		}

		// Add a small delay to be respectful to the server
		time.Sleep(2 * time.Second)
	}

	return nil
}

// ScrapeImageURLOnly scrapes just the image URL without requiring the restaurant to be in the database
func (is *ImageScraper) ScrapeImageURLOnly(ctx context.Context, restaurantURL string) (string, error) {
	imageURL, err := is.ScrapeImageURL(ctx, restaurantURL)
	if err != nil {
		return "", fmt.Errorf("failed to scrape image URL for %s: %w", restaurantURL, err)
	}

	log.WithFields(log.Fields{
		"restaurant_url": restaurantURL,
		"image_url":      imageURL,
	}).Info("Successfully extracted image URL")

	return imageURL, nil
}

// DownloadImage downloads an image from a URL and saves it to a local file
func (is *ImageScraper) DownloadImage(ctx context.Context, imageURL, filePath string) error {
	log.WithFields(log.Fields{
		"image_url": imageURL,
		"file_path": filePath,
	}).Debug("Downloading image")

	// Make HTTP request
	req, err := http.NewRequestWithContext(ctx, "GET", imageURL, nil)
	if err != nil {
		return fmt.Errorf("failed to create request: %w", err)
	}

	// Set headers
	req.Header.Set("User-Agent", "Mozilla/5.0 (Macintosh; Intel Mac OS X 10_15_7) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/91.0.4472.124 Safari/537.36")
	req.Header.Set("Accept", "image/webp,image/apng,image/*,*/*;q=0.8")
	req.Header.Set("Accept-Language", "en-US,en;q=0.5")
	req.Header.Set("Accept-Encoding", "gzip, deflate")
	req.Header.Set("Connection", "keep-alive")

	resp, err := is.client.Do(req)
	if err != nil {
		return fmt.Errorf("failed to download image: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != 200 {
		return fmt.Errorf("failed to download image: %d %s", resp.StatusCode, resp.Status)
	}

	// Create file
	file, err := createFile(filePath)
	if err != nil {
		return fmt.Errorf("failed to create file: %w", err)
	}
	defer file.Close()

	// Copy image data to file
	_, err = io.Copy(file, resp.Body)
	if err != nil {
		return fmt.Errorf("failed to write image to file: %w", err)
	}

	log.WithFields(log.Fields{
		"image_url": imageURL,
		"file_path": filePath,
	}).Info("Successfully downloaded image")

	return nil
}
