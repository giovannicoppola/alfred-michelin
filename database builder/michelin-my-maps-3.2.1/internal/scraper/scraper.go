package scraper

import (
	"context"
	"encoding/csv"
	"fmt"
	"net/http"
	"os"
	"strings"
	"sync"
	"time"

	"github.com/gocolly/colly/v2"
	"github.com/gocolly/colly/v2/queue"
	"github.com/ngshiheng/michelin-my-maps/v3/internal/models"
	"github.com/ngshiheng/michelin-my-maps/v3/internal/parser"
	"github.com/ngshiheng/michelin-my-maps/v3/internal/storage"
	"github.com/ngshiheng/michelin-my-maps/v3/internal/webclient"
	log "github.com/sirupsen/logrus"
)

// Config holds configuration for the scraper process.
type Config struct {
	AllowedDomains []string
	CachePath      string
	DatabasePath   string
	Delay          time.Duration
	MaxRetry       int
	MaxURLs        int
	MaxRestaurants int // Maximum number of restaurants to extract (0 = no limit)
	RandomDelay    time.Duration
	ThreadCount    int
}

// DefaultConfig returns a default config for the scraper.
func DefaultConfig() *Config {
	return &Config{
		AllowedDomains: []string{"guide.michelin.com"},
		CachePath:      "cache/scrape",
		DatabasePath:   "data/michelin.db",
		Delay:          4 * time.Second, // Increased from 2s to 4s
		MaxRetry:       3,
		MaxURLs:        30_000,
		MaxRestaurants: 0,               // No limit by default
		RandomDelay:    4 * time.Second, // Increased from 2s to 4s (total 4-8s delay)
		ThreadCount:    1,
	}
}

// ConservativeConfig returns a very conservative config for heavily protected sites
func ConservativeConfig() *Config {
	return &Config{
		AllowedDomains: []string{"guide.michelin.com"},
		CachePath:      "cache/scrape",
		DatabasePath:   "data/michelin.db",
		Delay:          8 * time.Second, // Very conservative 8s base delay
		MaxRetry:       3,
		MaxURLs:        30_000,
		MaxRestaurants: 0,
		RandomDelay:    8 * time.Second, // 8-16s total delay
		ThreadCount:    1,
	}
}

// Scraper orchestrates the web scraping process.
type Scraper struct {
	config         *Config
	client         *webclient.WebClient
	repository     storage.RestaurantRepository
	michelinURLs   []models.GuideURL
	processedCount int
	queuedCount    int // Track queued restaurant detail pages
	mu             sync.Mutex
}

// New returns a new Scraper with default settings.
func New() (*Scraper, error) {
	return NewWithLimit(0)
}

// NewConservative returns a new Scraper with very conservative settings (8-16s delays)
func NewConservative() (*Scraper, error) {
	return NewConservativeWithLimit(0)
}

// NewConservativeWithLimit returns a new Scraper with conservative settings and a restaurant limit
func NewConservativeWithLimit(maxRestaurants int) (*Scraper, error) {
	cfg := ConservativeConfig()
	cfg.MaxRestaurants = maxRestaurants

	repo, err := storage.NewSQLiteRepository(cfg.DatabasePath)
	if err != nil {
		return nil, fmt.Errorf("failed to create repository: %w", err)
	}

	wc, err := webclient.New(&webclient.Config{
		CachePath:      cfg.CachePath,
		AllowedDomains: cfg.AllowedDomains,
		Delay:          cfg.Delay,
		RandomDelay:    cfg.RandomDelay,
		ThreadCount:    cfg.ThreadCount,
		MaxURLs:        cfg.MaxURLs,
	})
	if err != nil {
		return nil, fmt.Errorf("failed to create web client: %w", err)
	}

	s := &Scraper{
		client:     wc,
		config:     cfg,
		repository: repo,
	}
	s.initURLs()
	return s, nil
}

// NewWithLimit returns a new Scraper with a specified restaurant limit.
func NewWithLimit(maxRestaurants int) (*Scraper, error) {
	cfg := DefaultConfig()
	cfg.MaxRestaurants = maxRestaurants

	repo, err := storage.NewSQLiteRepository(cfg.DatabasePath)
	if err != nil {
		return nil, fmt.Errorf("failed to create repository: %w", err)
	}

	wc, err := webclient.New(&webclient.Config{
		CachePath:      cfg.CachePath,
		AllowedDomains: cfg.AllowedDomains,
		Delay:          cfg.Delay,
		RandomDelay:    cfg.RandomDelay,
		ThreadCount:    cfg.ThreadCount,
		MaxURLs:        cfg.MaxURLs,
	})
	if err != nil {
		return nil, fmt.Errorf("failed to create web client: %w", err)
	}

	s := &Scraper{
		client:     wc,
		config:     cfg,
		repository: repo,
	}
	s.initURLs()
	return s, nil
}

// initURLs initializes the default start URLs for all award distinctions.
func (s *Scraper) initURLs() {
	allAwards := []string{
		models.ThreeStars,
		models.TwoStars,
		models.OneStar,
		models.BibGourmand,
		models.SelectedRestaurants,
	}

	for _, distinction := range allAwards {
		url, ok := models.DistinctionURL[distinction]
		if !ok {
			continue
		}

		michelinURL := models.GuideURL{
			Distinction: distinction,
			URL:         url,
		}
		s.michelinURLs = append(s.michelinURLs, michelinURL)
	}
}

// Run crawls Michelin Guide restaurant information from the configured URLs.
func (s *Scraper) Run(ctx context.Context) error {
	queue := s.client.GetQueue()
	collector := s.client.GetCollector()
	detailCollector := s.client.CreateDetailCollector()

	s.setupMainHandlers(ctx, collector, queue, detailCollector)
	s.setupDetailHandlers(ctx, detailCollector, queue)

	for _, url := range s.michelinURLs {
		s.client.AddURL(url.URL)
	}

	s.client.Run()
	log.Info("scraping completed")
	return nil
}

// shouldProcessRestaurant determines if a restaurant should be processed for randomization
// This is only used when no hard limit is set (for future use or sampling without limits)
func (s *Scraper) shouldProcessRestaurant() bool {
	// For now, always return true since we're using hard limits
	// This function can be extended later for sampling strategies
	return true
}

// incrementProcessedCount increments the processed restaurant counter
func (s *Scraper) incrementProcessedCount() {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.processedCount++
}

// getProcessedCount returns the current processed count
func (s *Scraper) getProcessedCount() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.processedCount
}

// incrementQueuedCount increments the queued restaurant counter
func (s *Scraper) incrementQueuedCount() {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.queuedCount++
}

// getQueuedCount returns the current queued count
func (s *Scraper) getQueuedCount() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.queuedCount
}

// getTotalCount returns processed + queued count
func (s *Scraper) getTotalCount() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.processedCount + s.queuedCount
}

// CSVRestaurant represents a restaurant record from CSV file
type CSVRestaurant struct {
	Name     string
	Location string
	URL      string
	Cuisine  string
	Award    string
	Price    string
	Address  string
}

// parseCSV reads and parses the CSV file containing restaurant information
func (s *Scraper) parseCSV(csvFile string) ([]CSVRestaurant, error) {
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

	// Skip header row and parse data
	var restaurants []CSVRestaurant
	for i, record := range records[1:] { // Skip header
		if len(record) < 7 {
			log.WithFields(log.Fields{
				"line":   i + 2, // +2 because we skip header and i starts at 0
				"record": record,
			}).Warn("skipping CSV row with insufficient columns")
			continue
		}

		restaurant := CSVRestaurant{
			Name:     strings.TrimSpace(record[0]),
			Location: strings.TrimSpace(record[1]),
			URL:      strings.TrimSpace(record[2]),
			Cuisine:  strings.TrimSpace(record[3]),
			Award:    strings.TrimSpace(record[4]),
			Price:    strings.TrimSpace(record[5]),
			Address:  strings.TrimSpace(record[6]),
		}

		// Validate URL
		if restaurant.URL == "" {
			log.WithFields(log.Fields{
				"line": i + 2,
				"name": restaurant.Name,
			}).Warn("skipping restaurant with empty URL")
			continue
		}

		// Ensure URL contains Michelin guide domain
		if !strings.Contains(restaurant.URL, "guide.michelin.com") {
			log.WithFields(log.Fields{
				"line": i + 2,
				"name": restaurant.Name,
				"url":  restaurant.URL,
			}).Warn("skipping restaurant with non-Michelin URL")
			continue
		}

		restaurants = append(restaurants, restaurant)
	}

	return restaurants, nil
}

// RunFromCSV crawls specific restaurant information from CSV file URLs
func (s *Scraper) RunFromCSV(ctx context.Context, csvFile string) error {
	restaurants, err := s.parseCSV(csvFile)
	if err != nil {
		return fmt.Errorf("failed to parse CSV file: %w", err)
	}

	log.WithFields(log.Fields{"count": len(restaurants)}).Info("restaurants loaded from CSV")

	if len(restaurants) == 0 {
		return fmt.Errorf("no valid restaurants found in CSV file")
	}

	// Set up collectors
	detailCollector := s.client.CreateDetailCollector()
	s.setupCSVDetailHandlers(ctx, detailCollector, restaurants)

	// Add all restaurant URLs to be scraped
	for i, restaurant := range restaurants {
		// Respect MaxRestaurants limit if set
		if s.config.MaxRestaurants > 0 && i >= s.config.MaxRestaurants {
			log.WithFields(log.Fields{
				"processed": i,
				"limit":     s.config.MaxRestaurants,
				"total":     len(restaurants),
			}).Info("reached restaurant limit for CSV processing")
			break
		}

		log.WithFields(log.Fields{
			"index": i + 1,
			"total": len(restaurants),
			"name":  restaurant.Name,
			"url":   restaurant.URL,
		}).Info("queueing restaurant from CSV")

		// Create a new context for each restaurant with CSV data
		requestCtx := colly.NewContext()
		requestCtx.Put("csv_name", restaurant.Name)
		requestCtx.Put("csv_location", restaurant.Location)
		requestCtx.Put("csv_cuisine", restaurant.Cuisine)
		requestCtx.Put("csv_award", restaurant.Award)
		requestCtx.Put("csv_price", restaurant.Price)
		requestCtx.Put("csv_address", restaurant.Address)

		// Parse location for lat/lng if available
		if restaurant.Location != "" {
			requestCtx.Put("location", restaurant.Location)
		}

		err := detailCollector.Request("GET", restaurant.URL, nil, requestCtx, nil)
		if err != nil {
			log.WithFields(log.Fields{
				"error": err,
				"url":   restaurant.URL,
				"name":  restaurant.Name,
			}).Error("failed to queue restaurant URL")
		}
	}

	// Start the scraping process
	s.client.GetQueue().Run(detailCollector)
	log.Info("CSV scraping completed")
	return nil
}

// setupCSVDetailHandlers sets up handlers specifically for CSV-based scraping
func (s *Scraper) setupCSVDetailHandlers(ctx context.Context, detailCollector *colly.Collector, restaurants []CSVRestaurant) {
	detailCollector.OnRequest(func(r *colly.Request) {
		attempt := r.Ctx.GetAny("attempt")
		if attempt == nil {
			r.Ctx.Put("attempt", 1)
			attempt = 1
		}
		log.WithFields(log.Fields{
			"attempt": attempt,
			"url":     r.URL.String(),
			"name":    r.Ctx.Get("csv_name"),
		}).Debug("fetching restaurant detail from CSV")
	})

	detailCollector.OnXML(restaurantAwardPublishedYearXPath, func(e *colly.XMLElement) {
		jsonLD := e.Text
		year, err := parser.ParsePublishedYearFromJSONLD(jsonLD)
		if err == nil && year > 0 {
			e.Request.Ctx.Put("jsonLD", jsonLD)
			e.Request.Ctx.Put("publishedYear", year)
		}
	})

	// Extract details of each restaurant and save to database
	detailCollector.OnXML(restaurantDetailXPath, func(e *colly.XMLElement) {
		data := s.extractRestaurantData(e)

		// Use CSV data as fallback for missing information
		if csvName := e.Request.Ctx.Get("csv_name"); csvName != "" && data.Name == "" {
			data.Name = csvName
		}
		if csvLocation := e.Request.Ctx.Get("csv_location"); csvLocation != "" && data.Location == "" {
			data.Location = csvLocation
		}

		// Handle missing coordinates by providing defaults for CSV mode
		if data.Latitude == "" {
			data.Latitude = "0.0"
			log.WithFields(log.Fields{
				"url":      data.URL,
				"csv_name": e.Request.Ctx.Get("csv_name"),
			}).Debug("no latitude found, using default 0.0")
		}
		if data.Longitude == "" {
			data.Longitude = "0.0"
			log.WithFields(log.Fields{
				"url":      data.URL,
				"csv_name": e.Request.Ctx.Get("csv_name"),
			}).Debug("no longitude found, using default 0.0")
		}

		// Handle missing description which is also required
		if data.Description == "" {
			data.Description = "Restaurant information from Michelin Guide"
			log.WithFields(log.Fields{
				"url":      data.URL,
				"csv_name": e.Request.Ctx.Get("csv_name"),
			}).Debug("no description found, using default")
		}

		log.WithFields(log.Fields{
			"distinction": data.Distinction,
			"name":        data.Name,
			"url":         data.URL,
			"csv_name":    e.Request.Ctx.Get("csv_name"),
		}).Debug("restaurant detail extracted from CSV")

		if err := s.repository.UpsertRestaurantWithAward(ctx, data); err != nil {
			log.WithFields(log.Fields{
				"error":    err,
				"url":      data.URL,
				"csv_name": e.Request.Ctx.Get("csv_name"),
			}).Error("failed to upsert restaurant award from CSV")
		} else {
			// Update processed count
			s.mu.Lock()
			s.processedCount++
			currentProcessed := s.processedCount
			s.mu.Unlock()

			log.WithFields(log.Fields{
				"distinction": data.Distinction,
				"name":        data.Name,
				"url":         data.URL,
				"year":        data.Year,
				"processed":   currentProcessed,
				"csv_name":    e.Request.Ctx.Get("csv_name"),
			}).Info("upserted restaurant award from CSV")
		}
	})

	detailCollector.OnError(s.createErrorHandler())
}

func (s *Scraper) setupMainHandlers(ctx context.Context, collector *colly.Collector, q *queue.Queue, detailCollector *colly.Collector) {
	collector.OnRequest(func(r *colly.Request) {
		attempt := r.Ctx.GetAny("attempt")
		if attempt == nil {
			r.Ctx.Put("attempt", 1)
			attempt = 1
		}
		log.WithFields(log.Fields{
			"url":     r.URL.String(),
			"attempt": attempt,
		}).Debug("fetching listing page")
	})

	collector.OnResponse(func(r *colly.Response) {
		log.WithFields(log.Fields{
			"url":         r.Request.URL.String(),
			"status_code": r.StatusCode,
		}).Debug("processing listing page")
	})

	collector.OnScraped(func(r *colly.Response) {
		log.WithFields(log.Fields{"url": r.Request.URL.String()}).Debug("listing page parsed")
	})

	// Extract restaurant URLs from the main page and visit them
	collector.OnXML(restaurantXPath, func(e *colly.XMLElement) {
		// If we have a limit and have reached it (including queued), stop processing more restaurants
		if s.config.MaxRestaurants > 0 && s.getTotalCount() >= s.config.MaxRestaurants {
			return
		}

		// Check if we should process this restaurant (for randomization when no hard limit)
		if s.config.MaxRestaurants == 0 && !s.shouldProcessRestaurant() {
			return
		}

		url := e.Request.AbsoluteURL(e.ChildAttr(restaurantDetailUrlXPath, "href"))

		location := e.ChildText(restaurantLocationXPath)
		longitude := e.Attr("data-lng")
		latitude := e.Attr("data-lat")

		e.Request.Ctx.Put("location", location)
		e.Request.Ctx.Put("longitude", longitude)
		e.Request.Ctx.Put("latitude", latitude)

		// Increment queued count when we actually queue a restaurant
		s.incrementQueuedCount()

		log.WithFields(log.Fields{
			"url":       url,
			"location":  location,
			"longitude": longitude,
			"latitude":  latitude,
			"queued":    s.getQueuedCount(),
		}).Debug("queueing restaurant detail page")

		detailCollector.Request(e.Request.Method, url, nil, e.Request.Ctx, nil)
	})

	// Extract and visit next page links
	collector.OnXML(nextPageArrowButtonXPath, func(e *colly.XMLElement) {
		// If we have a restaurant limit and have reached it (including queued), stop pagination
		if s.config.MaxRestaurants > 0 && s.getTotalCount() >= s.config.MaxRestaurants {
			log.WithFields(log.Fields{
				"processed": s.getProcessedCount(),
				"queued":    s.getQueuedCount(),
				"total":     s.getTotalCount(),
				"limit":     s.config.MaxRestaurants,
			}).Info("restaurant limit reached, stopping pagination")
			return
		}

		log.WithFields(log.Fields{
			"url": e.Attr("href"),
		}).Debug("queueing next page")
		e.Request.Visit(e.Attr("href"))
	})

	collector.OnError(s.createErrorHandler())
}

func (s *Scraper) setupDetailHandlers(ctx context.Context, detailCollector *colly.Collector, q *queue.Queue) {
	detailCollector.OnRequest(func(r *colly.Request) {
		attempt := r.Ctx.GetAny("attempt")
		if attempt == nil {
			r.Ctx.Put("attempt", 1)
			attempt = 1
		}
		log.WithFields(log.Fields{
			"attempt":       attempt,
			"url":           r.URL.String(),
			"restaurant_id": r.Ctx.Get("restaurant_id"),
		}).Debug("fetching restaurant detail")
	})

	detailCollector.OnXML(restaurantAwardPublishedYearXPath, func(e *colly.XMLElement) {
		jsonLD := e.Text
		year, err := parser.ParsePublishedYearFromJSONLD(jsonLD)
		if err == nil && year > 0 {
			e.Request.Ctx.Put("jsonLD", jsonLD)
			e.Request.Ctx.Put("publishedYear", year)
		}
	})

	// Extract details of each restaurant and save to database
	detailCollector.OnXML(restaurantDetailXPath, func(e *colly.XMLElement) {
		data := s.extractRestaurantData(e)

		log.WithFields(log.Fields{
			"distinction":   data.Distinction,
			"name":          data.Name,
			"restaurant_id": e.Request.Ctx.Get("restaurant_id"),
			"url":           data.URL,
		}).Debug("restaurant detail extracted")

		if err := s.repository.UpsertRestaurantWithAward(ctx, data); err != nil {
			log.WithFields(log.Fields{
				"error": err,
				"url":   data.URL,
			}).Error("failed to upsert restaurant award")
		} else {
			// Move from queued to processed
			s.mu.Lock()
			s.queuedCount--
			s.processedCount++
			currentProcessed := s.processedCount
			currentQueued := s.queuedCount
			s.mu.Unlock()

			log.WithFields(log.Fields{
				"distinction": data.Distinction,
				"name":        data.Name,
				"url":         data.URL,
				"year":        data.Year,
				"processed":   currentProcessed,
				"queued":      currentQueued,
			}).Info("upserted restaurant award")

			// Log progress if we have a limit set
			if s.config.MaxRestaurants > 0 {
				log.WithFields(log.Fields{
					"processed": currentProcessed,
					"queued":    currentQueued,
					"limit":     s.config.MaxRestaurants,
				}).Info("progress update")
			}
		}
	})

	detailCollector.OnError(s.createErrorHandler())
}

// createErrorHandler creates a reusable error handler for collectors with retry logic.
func (s *Scraper) createErrorHandler() func(*colly.Response, error) {
	return func(r *colly.Response, err error) {
		attempt := 1
		if v := r.Ctx.GetAny("attempt"); v != nil {
			if a, ok := v.(int); ok {
				attempt = a
			}
		}

		fields := log.Fields{
			"attempt":     attempt,
			"error":       err,
			"status_code": r.StatusCode,
			"url":         r.Request.URL.String(),
		}

		// Special handling for 403 Forbidden errors
		if r.StatusCode == http.StatusForbidden {
			log.WithFields(fields).Warn("request forbidden (403) - website may be blocking scraping. Consider clearing cache, using VPN, or increasing delays")
			// For 403 errors, we still retry but with exponential backoff
			if attempt < s.config.MaxRetry {
				if err := s.client.ClearCache(r.Request); err != nil {
					log.WithFields(fields).Error("failed to clear cache for request")
				}

				// Exponential backoff for 403 errors: 8s, 16s, 32s
				backoff := time.Duration(attempt*attempt*8) * time.Second
				log.WithFields(fields).Warnf("403 forbidden error, retrying after %v with fresh headers", backoff)
				time.Sleep(backoff)

				r.Ctx.Put("attempt", attempt+1)
				r.Request.Retry()
				return
			} else {
				log.WithFields(fields).Errorf("request forbidden after %d attempts - website blocking detected", attempt)
				return
			}
		}

		// Do not retry if already visited.
		if strings.Contains(err.Error(), "already visited") {
			log.WithFields(fields).Debug("request already visited, skipping retry")
			return
		}

		shouldRetry := attempt < s.config.MaxRetry
		if shouldRetry {
			if err := s.client.ClearCache(r.Request); err != nil {
				log.WithFields(fields).Error("failed to clear cache for request")
			}

			backoff := time.Duration(attempt) * s.config.Delay
			log.WithFields(fields).Warnf("request failed, retrying after %v", backoff)
			time.Sleep(backoff)

			r.Ctx.Put("attempt", attempt+1)
			r.Request.Retry()
		} else {
			log.WithFields(fields).Errorf("request failed after %d attempts, giving up", attempt)
		}
	}
}
