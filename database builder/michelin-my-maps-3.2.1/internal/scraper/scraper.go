package scraper

import (
	"context"
	"encoding/csv"
	"fmt"
	"math/rand"
	"net/http"
	"os"
	"path/filepath"
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

// MaxRetryPasses is the number of outer scrape passes. Each pass re-queues
// URLs that failed permanently in the previous pass so that a single
// invocation can recover from transient failures without requiring the user
// to rerun the tool.
const MaxRetryPasses = 5

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
		Delay:          2 * time.Second,
		MaxRetry:       5,
		MaxURLs:        30_000,
		MaxRestaurants: 0,
		RandomDelay:    3 * time.Second, // total 2-5s delay
		ThreadCount:    2,
	}
}

// ConservativeConfig returns a very conservative config for heavily protected sites
func ConservativeConfig() *Config {
	return &Config{
		AllowedDomains: []string{"guide.michelin.com"},
		CachePath:      "cache/scrape",
		DatabasePath:   "data/michelin.db",
		Delay:          8 * time.Second,
		MaxRetry:       5,
		MaxURLs:        30_000,
		MaxRestaurants: 0,
		RandomDelay:    8 * time.Second, // total 8-16s delay
		ThreadCount:    1,
	}
}

// failedRequest records a URL that exhausted its per-request retry budget so
// an outer pass can re-attempt it.
type failedRequest struct {
	URL       string
	IsListing bool              // true when the URL is a listing/pagination page
	CtxData   map[string]string // preserved colly.Context values (location, lat, lng, ...)
	LastErr   string
	LastCode  int
}

// Scraper orchestrates the web scraping process.
type Scraper struct {
	config         *Config
	client         *webclient.WebClient
	repository     storage.RestaurantRepository
	michelinURLs   []models.GuideURL
	processedCount int
	queuedCount    int // Track queued restaurant detail pages
	failedRequests []failedRequest
	mu             sync.Mutex
}

// preservedCtxKeys are the keys we round-trip when a request is retried in a
// later pass. They carry the listing metadata a detail page handler expects.
var preservedCtxKeys = []string{"location", "longitude", "latitude", "restaurant_id"}

// recordFailure appends the URL that just exhausted its retries to
// failedRequests so the outer retry loop can re-attempt it in the next pass.
func (s *Scraper) recordFailure(r *colly.Request, isListing bool, statusCode int, err error) {
	ctxData := make(map[string]string, len(preservedCtxKeys))
	for _, k := range preservedCtxKeys {
		if v := r.Ctx.Get(k); v != "" {
			ctxData[k] = v
		}
	}
	errStr := ""
	if err != nil {
		errStr = err.Error()
	}
	s.mu.Lock()
	s.failedRequests = append(s.failedRequests, failedRequest{
		URL:       r.URL.String(),
		IsListing: isListing,
		CtxData:   ctxData,
		LastErr:   errStr,
		LastCode:  statusCode,
	})
	s.mu.Unlock()
}

// takeFailures atomically extracts and clears the failed request list.
func (s *Scraper) takeFailures() []failedRequest {
	s.mu.Lock()
	defer s.mu.Unlock()
	out := s.failedRequests
	s.failedRequests = nil
	return out
}

// wafChallengeMarkers are strings that uniquely identify the AWS WAF JS
// interstitial the origin serves when it rate-limits the scraper. The
// response status is 200 and the body is tiny (~2.5kB) with no real content,
// so OnXML handlers silently find nothing — we have to detect it explicitly.
var wafChallengeMarkers = [][]byte{
	[]byte("awsWafCookieDomainList"),
	[]byte("goku-sdk"),
	[]byte("gokuProps"),
}

// checkWAFChallenge inspects a response; if it looks like a WAF challenge
// page the URL is recorded as a failure (so the outer retry loop can retry
// after a cooldown), its cache entry is invalidated, and the request is
// aborted so downstream OnXML handlers don't process the empty body.
func (s *Scraper) checkWAFChallenge(r *colly.Response, isListing bool) {
	if r == nil || len(r.Body) == 0 || len(r.Body) > 10000 {
		return
	}
	body := r.Body
	hit := false
	for _, m := range wafChallengeMarkers {
		if bytesContains(body, m) {
			hit = true
			break
		}
	}
	if !hit {
		return
	}
	log.WithFields(log.Fields{
		"url":         r.Request.URL.String(),
		"status_code": r.StatusCode,
		"body_size":   len(r.Body),
		"listing":     isListing,
	}).Warn("AWS WAF challenge page returned — deferring URL to next pass")

	if cerr := s.client.ClearCache(r.Request); cerr != nil {
		log.WithFields(log.Fields{"url": r.Request.URL.String(), "error": cerr}).Warn("failed to clear cache for WAF-challenged URL")
	}
	s.recordFailure(r.Request, isListing, r.StatusCode, errWAFChallenge)
}

// errWAFChallenge is the sentinel error recorded against URLs served a WAF
// challenge page.
var errWAFChallenge = &scraperError{msg: "AWS WAF challenge page"}

type scraperError struct{ msg string }

func (e *scraperError) Error() string { return e.msg }

// bytesContains is a tiny substring search so we don't pull in "bytes" just
// for Contains (keeps the diff minimal against the rest of the package).
func bytesContains(haystack, needle []byte) bool {
	if len(needle) == 0 {
		return true
	}
	for i := 0; i+len(needle) <= len(haystack); i++ {
		match := true
		for j := 0; j < len(needle); j++ {
			if haystack[i+j] != needle[j] {
				match = false
				break
			}
		}
		if match {
			return true
		}
	}
	return false
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
//
// Behaviour:
//   - Pass 1 queues every distinction listing URL and walks pagination.
//   - After each pass, URLs that exhausted their per-request retry budget are
//     collected in s.failedRequests. Pass 2+ re-attempts only those (listing
//     pages go back through pagination; detail pages are requested directly),
//     using fresh colly collectors so the "already visited" set is clean.
//   - Stops early when a pass has no failures. After MaxRetryPasses the
//     unresolved URLs are written to data/failed_urls_<timestamp>.txt for
//     visibility (or retry via `scrape -csv`).
func (s *Scraper) Run(ctx context.Context) error {
	// Pass 1: normal listing crawl.
	if err := s.runListingPass(ctx, s.client.GetCollector(), s.client.GetQueue()); err != nil {
		return err
	}

	for pass := 2; pass <= MaxRetryPasses; pass++ {
		failed := s.takeFailures()
		if len(failed) == 0 {
			log.WithField("passes", pass-1).Info("scraping completed with no unresolved failures")
			return nil
		}

		listing, detail := splitFailures(failed)
		log.WithFields(log.Fields{
			"pass":           pass,
			"failed_listing": len(listing),
			"failed_detail":  len(detail),
		}).Info("retrying failed URLs in new pass")

		// Brief cooldown before retry pass — gives the origin server (and any
		// rate limiter) a chance to settle between bursts.
		time.Sleep(15 * time.Second)

		if err := s.runRetryPass(ctx, listing, detail); err != nil {
			return err
		}
	}

	// Any failures remaining after the final pass are persisted for visibility.
	final := s.takeFailures()
	if len(final) > 0 {
		path, werr := s.writeFailureReport(final)
		log.WithFields(log.Fields{
			"remaining": len(final),
			"report":    path,
			"writeErr":  werr,
		}).Warn("scraping completed with unresolved failures")
	} else {
		log.Info("scraping completed with no unresolved failures")
	}
	return nil
}

// runListingPass wires handlers onto the given collector/queue, seeds the
// listing URLs, and runs the crawl synchronously.
func (s *Scraper) runListingPass(ctx context.Context, collector *colly.Collector, q *queue.Queue) error {
	detailCollector := s.client.CreateDetailCollector()

	s.setupMainHandlers(ctx, collector, q, detailCollector)
	s.setupDetailHandlers(ctx, detailCollector, q)

	for _, url := range s.michelinURLs {
		if err := q.AddURL(url.URL); err != nil {
			return fmt.Errorf("failed to queue listing url %s: %w", url.URL, err)
		}
	}

	return q.Run(collector)
}

// runRetryPass retries URLs that failed permanently in an earlier pass using
// a fresh main collector / queue / detail collector (colly's "already
// visited" set is per-collector, so a fresh one is required).
func (s *Scraper) runRetryPass(ctx context.Context, listingFails, detailFails []failedRequest) error {
	// Clone collectors for a clean slate on the visited set.
	collector := s.client.GetCollector().Clone()
	detailCollector := s.client.CreateDetailCollector()

	// Fresh queue so re-added listing URLs aren't rejected as duplicates.
	q, err := queue.New(1, &queue.InMemoryQueueStorage{MaxSize: s.config.MaxURLs})
	if err != nil {
		return fmt.Errorf("failed to create retry queue: %w", err)
	}

	s.setupMainHandlers(ctx, collector, q, detailCollector)
	s.setupDetailHandlers(ctx, detailCollector, q)

	// Clear any cached failed responses so retries hit the live site.
	for _, f := range listingFails {
		_ = s.clearCacheForURL(f.URL)
	}
	for _, f := range detailFails {
		_ = s.clearCacheForURL(f.URL)
	}

	// Re-queue listing pages (walks pagination again and rediscovers details).
	for _, f := range listingFails {
		if err := q.AddURL(f.URL); err != nil {
			log.WithFields(log.Fields{"url": f.URL, "error": err}).Warn("failed to re-queue listing URL")
		}
	}
	if len(listingFails) > 0 {
		if err := q.Run(collector); err != nil {
			return fmt.Errorf("retry listing pass failed: %w", err)
		}
	}

	// Retry failed detail URLs directly, restoring the context values the
	// detail handler expects from the listing (location, lat, lng, ...).
	for _, f := range detailFails {
		reqCtx := colly.NewContext()
		for k, v := range f.CtxData {
			reqCtx.Put(k, v)
		}
		if err := detailCollector.Request("GET", f.URL, nil, reqCtx, nil); err != nil {
			log.WithFields(log.Fields{"url": f.URL, "error": err}).Warn("failed to re-request detail URL")
		}
	}
	return nil
}

// splitFailures partitions a failure list into listing and detail buckets.
func splitFailures(fs []failedRequest) (listing, detail []failedRequest) {
	for _, f := range fs {
		if f.IsListing {
			listing = append(listing, f)
		} else {
			detail = append(detail, f)
		}
	}
	return
}

// clearCacheForURL removes the cached response for a URL so a retry hits the
// live origin instead of replaying the 403/5xx that got cached on the first pass.
func (s *Scraper) clearCacheForURL(rawURL string) error {
	return s.client.ClearCacheByURL(rawURL)
}

// writeFailureReport writes permanently failed URLs to a timestamped file so
// the user can inspect or feed them back in via the `-csv` mode.
func (s *Scraper) writeFailureReport(failures []failedRequest) (string, error) {
	dir := filepath.Dir(s.config.DatabasePath)
	if dir == "" {
		dir = "."
	}
	path := filepath.Join(dir, fmt.Sprintf("failed_urls_%s.txt", time.Now().UTC().Format("2006-01-02_15-04-05")))
	f, err := os.Create(path)
	if err != nil {
		return "", err
	}
	defer f.Close()
	fmt.Fprintf(f, "# unresolved URLs after %d scrape passes (generated %s UTC)\n", MaxRetryPasses, time.Now().UTC().Format(time.RFC3339))
	fmt.Fprintf(f, "# format: <is_listing>\t<status_code>\t<url>\t<last_error>\n")
	for _, fr := range failures {
		fmt.Fprintf(f, "%t\t%d\t%s\t%s\n", fr.IsListing, fr.LastCode, fr.URL, fr.LastErr)
	}
	return path, nil
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

	detailCollector.OnError(s.createErrorHandler(false))
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
		s.checkWAFChallenge(r, true)
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

	collector.OnError(s.createErrorHandler(true))
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

	detailCollector.OnResponse(func(r *colly.Response) {
		s.checkWAFChallenge(r, false)
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

	detailCollector.OnError(s.createErrorHandler(false))
}

// createErrorHandler creates a reusable error handler for collectors with
// retry logic. isListing differentiates pagination/listing URLs (whose
// failures cascade — losing a page loses every restaurant under it) from
// detail URLs. When the per-request retry budget is exhausted, the URL is
// recorded for the outer retry pass via recordFailure.
func (s *Scraper) createErrorHandler(isListing bool) func(*colly.Response, error) {
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
			"listing":     isListing,
		}

		// Never retry the "already visited" error — it's a benign dedup
		// signal from colly, not a real fetch failure.
		if err != nil && strings.Contains(err.Error(), "already visited") {
			log.WithFields(fields).Debug("request already visited, skipping retry")
			return
		}

		// 403 / 429: site is actively rate-limiting or blocking. Longer
		// exponential backoff with jitter; fresh headers via cache clear.
		isRateLimit := r.StatusCode == http.StatusForbidden || r.StatusCode == http.StatusTooManyRequests
		if isRateLimit {
			log.WithFields(fields).Warn("request blocked or rate-limited by origin")
			if attempt < s.config.MaxRetry {
				if cerr := s.client.ClearCache(r.Request); cerr != nil {
					log.WithFields(fields).Warn("failed to clear cache for request")
				}
				// 8s, 16s, 32s, 64s, ... + up to 4s jitter.
				base := time.Duration(attempt*attempt*8) * time.Second
				jitter := time.Duration(rand.Intn(4000)) * time.Millisecond
				backoff := base + jitter
				log.WithFields(fields).Warnf("rate-limit backoff, retrying in %v", backoff)
				time.Sleep(backoff)
				r.Ctx.Put("attempt", attempt+1)
				if rerr := r.Request.Retry(); rerr != nil {
					log.WithFields(fields).Warn("retry submit failed")
				}
				return
			}
			log.WithFields(fields).Errorf("rate-limited after %d attempts, deferring to next pass", attempt)
			s.recordFailure(r.Request, isListing, r.StatusCode, err)
			return
		}

		// Generic retryable failure (network hiccup, 5xx, parse error, ...).
		if attempt < s.config.MaxRetry {
			if cerr := s.client.ClearCache(r.Request); cerr != nil {
				log.WithFields(fields).Warn("failed to clear cache for request")
			}
			// Jittered exponential-ish backoff: attempt * Delay + random up to Delay.
			base := time.Duration(attempt) * s.config.Delay
			jitter := time.Duration(rand.Int63n(int64(s.config.Delay)))
			backoff := base + jitter
			log.WithFields(fields).Warnf("request failed, retrying in %v", backoff)
			time.Sleep(backoff)
			r.Ctx.Put("attempt", attempt+1)
			if rerr := r.Request.Retry(); rerr != nil {
				log.WithFields(fields).Warn("retry submit failed")
			}
			return
		}

		log.WithFields(fields).Errorf("request failed after %d attempts, deferring to next pass", attempt)
		s.recordFailure(r.Request, isListing, r.StatusCode, err)
	}
}
