package scraper

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"sync"
	"sync/atomic"

	"github.com/gocolly/colly/v2"
	"github.com/ngshiheng/michelin-my-maps/v3/internal/models"
	"github.com/ngshiheng/michelin-my-maps/v3/internal/storage"
	log "github.com/sirupsen/logrus"
)

// diffState carries per-run state for a differential refresh. It lives on the
// Scraper for the duration of RunDiff and is torn down afterwards so repeat
// calls start from a clean slate.
type diffState struct {
	year           int
	existing       map[string]storage.LatestAwardInfo
	seen           sync.Map // map[string]struct{} — URLs discovered in this run's listings
	distinctionFor func(listingURL string) string

	// Counters (atomic so listing/detail handlers can touch them without locks).
	newCount       atomic.Int64 // URL not in DB
	awardChanged   atomic.Int64 // URL in DB, distinction differs
	unchanged      atomic.Int64 // URL in DB, distinction matches, no detail fetch
	alreadyCurrent atomic.Int64 // URL in DB, latest award is already for target year
	dropped        atomic.Int64 // URL in DB but not seen in any listing (post-walk sweep)
}

// RunDiff performs a differential refresh for `year`: walks the five
// distinction listings, then for each discovered restaurant URL either
// fetches the detail page (new restaurants / changed awards) or writes a
// listing-only award row cloning the prior year's price and greenStar. URLs
// present in the DB but absent from every listing are marked in_guide=false.
//
// Order-of-magnitude cheaper than a full Run because only the ~5 listing
// URLs × N pages are walked unconditionally — detail fetches are reserved
// for the subset of restaurants that actually changed.
func (s *Scraper) RunDiff(ctx context.Context, year int) error {
	if year < 1900 || year > 2200 {
		return fmt.Errorf("diff: year %d out of range", year)
	}

	repo, ok := s.repository.(*storage.SQLiteRepository)
	if !ok {
		return fmt.Errorf("diff: repository is not *storage.SQLiteRepository — diff mode requires the concrete SQLite repository")
	}

	existing, err := repo.GetLatestAwardsByURL(ctx)
	if err != nil {
		return fmt.Errorf("diff: failed to load existing awards: %w", err)
	}

	log.WithFields(log.Fields{
		"target_year":      year,
		"existing_records": len(existing),
	}).Info("starting differential scrape")

	state := &diffState{
		year:           year,
		existing:       existing,
		distinctionFor: buildDistinctionResolver(),
	}

	// Run a single listing pass. We deliberately skip the main scraper's
	// multi-pass retry loop: differential scrapes are small enough that a
	// user can just re-run on transient failure, and we'd rather keep the
	// branching logic simple.
	collector := s.client.GetCollector()
	q := s.client.GetQueue()

	detailCollector := s.client.CreateDetailCollector()
	s.setupDiffMainHandlers(ctx, collector, detailCollector, state)
	s.setupDetailHandlers(ctx, detailCollector, q)

	for _, u := range s.michelinURLs {
		if err := q.AddURL(u.URL); err != nil {
			return fmt.Errorf("diff: failed to queue listing url %s: %w", u.URL, err)
		}
	}

	if err := q.Run(collector); err != nil {
		return fmt.Errorf("diff: listing pass failed: %w", err)
	}

	// Post-walk sweep: any URL in DB not seen in any listing is no longer
	// in the guide.
	droppedCount := s.sweepDroppedRestaurants(ctx, repo, state)
	state.dropped.Store(int64(droppedCount))

	logDiffSummary(state)
	return nil
}

// buildDistinctionResolver returns a function that maps a listing or
// pagination URL back to the distinction being walked. The five starter URLs
// live under distinct path segments (e.g. `/restaurants/1-star-michelin`),
// which pagination preserves, so a substring match is sufficient.
func buildDistinctionResolver() func(string) string {
	type entry struct {
		needle      string
		distinction string
	}
	entries := make([]entry, 0, len(models.DistinctionURL))
	for distinction, listingURL := range models.DistinctionURL {
		// Use the trailing path segment so we don't falsely match on
		// other parts of the URL.
		idx := strings.LastIndex(listingURL, "/")
		needle := listingURL
		if idx >= 0 {
			needle = listingURL[idx+1:]
		}
		entries = append(entries, entry{needle: needle, distinction: distinction})
	}
	return func(u string) string {
		for _, e := range entries {
			if strings.Contains(u, e.needle) {
				return e.distinction
			}
		}
		return ""
	}
}

// setupDiffMainHandlers wires the listing-page handlers for the diff pass.
// It is structurally parallel to setupMainHandlers but replaces the
// unconditional `detailCollector.Request` with a four-way branch based on
// what the DB already knows about the URL.
func (s *Scraper) setupDiffMainHandlers(
	ctx context.Context,
	collector *colly.Collector,
	detailCollector *colly.Collector,
	state *diffState,
) {
	collector.OnRequest(func(r *colly.Request) {
		// Attach the current listing's distinction to the request context
		// so the detail handler downstream can still write a correct
		// award row even when the detail page XPath disagrees.
		if d := state.distinctionFor(r.URL.String()); d != "" {
			r.Ctx.Put("listing_distinction", d)
		}
	})

	collector.OnResponse(func(r *colly.Response) {
		s.checkWAFChallenge(r, true)
	})

	collector.OnXML(restaurantXPath, func(e *colly.XMLElement) {
		url := e.Request.AbsoluteURL(e.ChildAttr(restaurantDetailUrlXPath, "href"))
		if url == "" {
			return
		}
		// Dedupe by URL — the same restaurant can surface on multiple
		// listing pages (pagination, cross-listing) and we only want one
		// decision per URL. DB writes are idempotent either way; the
		// counters would otherwise over-report.
		if _, loaded := state.seen.LoadOrStore(url, struct{}{}); loaded {
			return
		}

		location := e.ChildText(restaurantLocationXPath)
		longitude := e.Attr("data-lng")
		latitude := e.Attr("data-lat")

		distinction := e.Request.Ctx.Get("listing_distinction")
		if distinction == "" {
			// Can't classify this URL without a distinction — fall back to
			// fetching the detail page, same as a full scrape would.
			distinction = state.distinctionFor(e.Request.URL.String())
		}

		info, known := state.existing[url]

		// Shared context plumbing for paths that fetch the detail page.
		queueDetail := func(reason string, fields log.Fields) {
			e.Request.Ctx.Put("location", location)
			e.Request.Ctx.Put("longitude", longitude)
			e.Request.Ctx.Put("latitude", latitude)
			// extractRestaurantData uses this to override whatever the
			// detail-page JSON-LD says the year is. See extract.go.
			e.Request.Ctx.Put("diff_target_year", state.year)
			s.incrementQueuedCount()
			fields["url"] = url
			fields["reason"] = reason
			log.WithFields(fields).Debug("diff: fetching detail page")
			detailCollector.Request(e.Request.Method, url, nil, e.Request.Ctx, nil)
		}

		switch {
		case !known:
			state.newCount.Add(1)
			queueDetail("new_restaurant", log.Fields{"distinction": distinction})

		case info.Distinction != distinction:
			state.awardChanged.Add(1)
			queueDetail("award_changed", log.Fields{
				"old_distinct": info.Distinction,
				"new_distinct": distinction,
			})

		case info.Year >= state.year:
			// Already have an award row for (or beyond) the target year.
			// Listing walk still recorded the URL as seen, so in_guide
			// stays true. No DB write needed.
			state.alreadyCurrent.Add(1)

		default:
			// Unchanged distinction, but no award row for target year yet.
			// Clone last year's award forward (option A from design).
			if err := s.writeListingOnlyAward(ctx, info, state.year, distinction); err != nil {
				log.WithFields(log.Fields{
					"url":   url,
					"error": err,
				}).Error("diff: failed to write listing-only award")
				return
			}
			state.unchanged.Add(1)
		}
	})

	collector.OnXML(nextPageArrowButtonXPath, func(e *colly.XMLElement) {
		href := e.Attr("href")
		if err := e.Request.Visit(href); err != nil {
			var alreadyVisited *colly.AlreadyVisitedError
			if !errors.As(err, &alreadyVisited) {
				log.WithFields(log.Fields{"url": href, "error": err}).Debug("diff: failed to visit next page")
			}
		}
	})

	collector.OnError(s.createErrorHandler(true))

	_ = ctx // kept in signature for parity with setupMainHandlers
}

// writeListingOnlyAward inserts/updates an award row for the target year by
// cloning price/greenStar from the prior-year award. The restaurant row's
// in_guide flag is also lifted to true (a restaurant that was previously
// marked as dropped may reappear on this year's listing).
func (s *Scraper) writeListingOnlyAward(
	ctx context.Context,
	info storage.LatestAwardInfo,
	year int,
	distinction string,
) error {
	repo, ok := s.repository.(*storage.SQLiteRepository)
	if !ok {
		return fmt.Errorf("diff: repository is not *storage.SQLiteRepository")
	}

	award := &models.RestaurantAward{
		RestaurantID: info.RestaurantID,
		Year:         year,
		Distinction:  distinction,
		Price:        info.Price,
		GreenStar:    info.GreenStar,
	}
	if err := repo.SaveAward(ctx, award); err != nil {
		return fmt.Errorf("SaveAward: %w", err)
	}

	if !info.InGuide {
		if err := repo.SetInGuide(ctx, info.RestaurantID, true); err != nil {
			return fmt.Errorf("SetInGuide(true): %w", err)
		}
	}
	return nil
}

// sweepDroppedRestaurants flips in_guide=false on every restaurant whose URL
// was in the DB at startup but never surfaced in any of this run's listings.
// Returns the number of rows actually updated.
func (s *Scraper) sweepDroppedRestaurants(
	ctx context.Context,
	repo *storage.SQLiteRepository,
	state *diffState,
) int {
	var dropped int
	for url, info := range state.existing {
		if _, seen := state.seen.Load(url); seen {
			continue
		}
		if !info.InGuide {
			// Already marked as not in guide on a prior diff run.
			continue
		}
		if err := repo.SetInGuide(ctx, info.RestaurantID, false); err != nil {
			log.WithFields(log.Fields{
				"url":   url,
				"error": err,
			}).Error("diff: failed to mark restaurant as not in guide")
			continue
		}
		dropped++
	}
	return dropped
}

func logDiffSummary(state *diffState) {
	log.WithFields(log.Fields{
		"target_year":     state.year,
		"new":             state.newCount.Load(),
		"award_changed":   state.awardChanged.Load(),
		"already_current": state.alreadyCurrent.Load(),
		"unchanged":       state.unchanged.Load(),
		"dropped":         state.dropped.Load(),
	}).Info("differential scrape complete")
}

