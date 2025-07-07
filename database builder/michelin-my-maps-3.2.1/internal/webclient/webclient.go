package webclient

import (
	"crypto/sha1"
	"encoding/hex"
	"math/rand"
	"os"
	"path"
	"path/filepath"
	"time"

	"github.com/gocolly/colly/v2"
	"github.com/gocolly/colly/v2/queue"
)

// Config defines the minimal config needed for webClient.
type Config struct {
	CachePath      string
	AllowedDomains []string
	Delay          time.Duration
	RandomDelay    time.Duration
	ThreadCount    int
	MaxURLs        int
}

// webClient provides HTTP client functionality for web scraping.
type WebClient struct {
	collector *colly.Collector
	queue     *queue.Queue
	config    *Config
}

// Modern, realistic user agents that are less likely to be blocked
var userAgents = []string{
	"Mozilla/5.0 (Macintosh; Intel Mac OS X 10_15_7) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/120.0.0.0 Safari/537.36",
	"Mozilla/5.0 (Windows NT 10.0; Win64; x64) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/120.0.0.0 Safari/537.36",
	"Mozilla/5.0 (Macintosh; Intel Mac OS X 10_15_7) AppleWebKit/605.1.15 (KHTML, like Gecko) Version/17.1 Safari/605.1.15",
	"Mozilla/5.0 (Windows NT 10.0; Win64; x64; rv:109.0) Gecko/20100101 Firefox/121.0",
	"Mozilla/5.0 (Macintosh; Intel Mac OS X 10.15; rv:109.0) Gecko/20100101 Firefox/121.0",
	"Mozilla/5.0 (X11; Linux x86_64) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/120.0.0.0 Safari/537.36",
	"Mozilla/5.0 (Windows NT 10.0; Win64; x64) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/120.0.0.0 Safari/537.36 Edg/120.0.0.0",
}

// getRandomUserAgent returns a random modern user agent
func getRandomUserAgent() string {
	return userAgents[rand.Intn(len(userAgents))]
}

// setupRealisticHeaders configures realistic browser headers
func setupRealisticHeaders(c *colly.Collector) {
	c.OnRequest(func(r *colly.Request) {
		// Set a realistic user agent
		r.Headers.Set("User-Agent", getRandomUserAgent())

		// Add realistic browser headers
		r.Headers.Set("Accept", "text/html,application/xhtml+xml,application/xml;q=0.9,image/avif,image/webp,image/apng,*/*;q=0.8,application/signed-exchange;v=b3;q=0.7")
		r.Headers.Set("Accept-Language", "en-US,en;q=0.9")
		r.Headers.Set("Accept-Encoding", "gzip, deflate, br")
		r.Headers.Set("DNT", "1")
		r.Headers.Set("Connection", "keep-alive")
		r.Headers.Set("Upgrade-Insecure-Requests", "1")
		r.Headers.Set("Sec-Fetch-Dest", "document")
		r.Headers.Set("Sec-Fetch-Mode", "navigate")
		r.Headers.Set("Sec-Fetch-Site", "none")
		r.Headers.Set("Sec-Fetch-User", "?1")
		r.Headers.Set("Cache-Control", "max-age=0")

		// Add a referer for detail pages
		if r.URL.Path != "/" && r.URL.Path != "" {
			r.Headers.Set("Referer", "https://guide.michelin.com/")
		}
	})
}

// New creates a new web client instance.
func New(cfg *Config) (*WebClient, error) {
	cacheDir := filepath.Join(cfg.CachePath)

	c := colly.NewCollector(
		colly.CacheDir(cacheDir),
		colly.AllowedDomains(cfg.AllowedDomains...),
	)

	c.Limit(&colly.LimitRule{
		Delay:       cfg.Delay,
		RandomDelay: cfg.RandomDelay,
	})

	// Setup realistic headers instead of default extensions
	setupRealisticHeaders(c)

	// Initialize random seed
	rand.Seed(time.Now().UnixNano())

	q, err := queue.New(
		cfg.ThreadCount,
		&queue.InMemoryQueueStorage{MaxSize: cfg.MaxURLs},
	)
	if err != nil {
		return nil, err
	}

	return &WebClient{
		collector: c,
		queue:     q,
		config:    cfg,
	}, nil
}

// GetQueue returns the queue for managing URLs.
func (w *WebClient) GetQueue() *queue.Queue {
	return w.queue
}

// GetCollector returns the colly collector for direct access.
func (w *WebClient) GetCollector() *colly.Collector {
	return w.collector
}

// CreateDetailCollector creates a cloned collector for detail page scraping.
func (w *WebClient) CreateDetailCollector() *colly.Collector {
	dc := w.collector.Clone()

	// Setup realistic headers for detail collector too
	setupRealisticHeaders(dc)

	return dc
}

// ClearCache removes the cache file for a given colly.Request.
func (w *WebClient) ClearCache(r *colly.Request) error {
	url := r.URL.String()
	sum := sha1.Sum([]byte(url))
	hash := hex.EncodeToString(sum[:])

	cacheDir := path.Join(w.config.CachePath, hash[:2])
	filename := path.Join(cacheDir, hash)

	if err := os.Remove(filename); err != nil && !os.IsNotExist(err) {
		return err
	}

	return nil
}

// AddURL adds a URL to the scraping queue.
func (w *WebClient) AddURL(url string) {
	w.queue.AddURL(url)
}

// Run starts the web scraping process.
func (w *WebClient) Run() {
	w.queue.Run(w.collector)
}
