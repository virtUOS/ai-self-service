package handlers

import (
	"context"
	"log/slog"
	"sync"
	"time"

	"github.com/virtuos/ai-self-service/internal/keyprovider"
)

// usageWindowDays is how far back the dashboard reports unless configured
// otherwise (USAGE_HISTORY_DAYS). Long enough to cover a month's key
// validity, short enough that the per-request log stays small.
const usageWindowDays = 30

// usageCacheTTL bounds how stale a usage report can be. LiteLLM takes about
// fifteen seconds to propagate spend anyway, so a fresh read per page load
// would be both wasteful and no more accurate.
const usageCacheTTL = 60 * time.Second

// usageCache serves per-key usage without calling upstream on every page load.
//
// Keyed by the key's ref, so a rotation naturally misses the cache and reports
// the new key rather than serving the old one's history.
//
// Every accessor holds the same invariant: a failed refresh returns nothing
// rather than a stale figure, and leaves no entry behind for a later read to
// pick up.
type usageCache struct {
	reporter keyprovider.UsageReporter
	// windowDays is how far back History reaches.
	windowDays int

	mu      sync.Mutex
	entries map[string]usageEntry
	totals  map[string]totalEntry
}

type totalEntry struct {
	spend     float64
	fetchedAt time.Time
}

type usageEntry struct {
	history   keyprovider.History
	fetchedAt time.Time
}

func newUsageCache(r keyprovider.UsageReporter) *usageCache {
	return &usageCache{
		reporter:   r,
		windowDays: usageWindowDays,
		entries:    make(map[string]usageEntry),
		totals:     make(map[string]totalEntry),
	}
}

// history returns the cached per-day and per-model usage for a key, refreshing
// when stale.
//
// Both halves come from one upstream read, so they are cached as one entry and
// can never disagree about the window they cover. A failed refresh returns an
// empty History and caches nothing, and every accessor checks freshness
// through here, so none of them can serve a stale figure an earlier call left
// behind.
func (c *usageCache) history(ctx context.Context, ref string) keyprovider.History {
	if c.reporter == nil || ref == "" {
		return keyprovider.History{}
	}

	c.mu.Lock()
	defer c.mu.Unlock()

	if e, ok := c.entries[ref]; ok && time.Since(e.fetchedAt) < usageCacheTTL {
		return e.history
	}

	h, err := c.reporter.History(ctx, ref, c.windowDays)
	if err != nil {
		slog.Error("read key usage", "err", err)
		return keyprovider.History{}
	}
	c.entries[ref] = usageEntry{history: h, fetchedAt: time.Now()}
	return h
}

// Days returns the per-day usage for a key.
func (c *usageCache) Days(ctx context.Context, ref string) []keyprovider.DailyUsage {
	return c.history(ctx, ref).Days
}

// Models returns the per-model totals, over the same window as Days.
func (c *usageCache) Models(ctx context.Context, ref string) []keyprovider.ModelUsage {
	return c.history(ctx, ref).Models
}

// TotalSpend returns the key's cumulative spend, the coarse figure that
// survives when per-request logging is unavailable. Cached alongside the
// per-day rows and on the same terms: a failed read reports nothing.
func (c *usageCache) TotalSpend(ctx context.Context, ref string) float64 {
	if c.reporter == nil || ref == "" {
		return 0
	}

	c.mu.Lock()
	defer c.mu.Unlock()

	if e, ok := c.totals[ref]; ok && time.Since(e.fetchedAt) < usageCacheTTL {
		return e.spend
	}

	total, err := c.reporter.TotalSpend(ctx, ref)
	if err != nil {
		slog.Error("read key total spend", "err", err)
		return 0
	}
	c.totals[ref] = totalEntry{spend: total, fetchedAt: time.Now()}
	return total
}

// Quota returns consumption against the enforced allowance, for the key and
// the person who owns it.
//
// Uncached: it is the figure users act on when they are close to the limit,
// and a minute-stale number there is worse than a fresh call.
func (c *usageCache) Quota(ctx context.Context, ref, ownerID string) (keyprovider.Quota, error) {
	if c.reporter == nil || ref == "" {
		return keyprovider.Quota{}, nil
	}
	return c.reporter.Quota(ctx, ref, ownerID)
}
