package handlers

import (
	"context"
	"log/slog"
	"sync"
	"time"

	"github.com/virtuos/ai-self-service/internal/keyprovider"
)

// usageWindowDays is how far back the dashboard reports. Long enough to cover
// a month's key validity, short enough that the per-request log stays small.
const usageWindowDays = 30

// usageCacheTTL bounds how stale a usage report can be. LiteLLM takes about
// fifteen seconds to propagate spend anyway, so a fresh read per page load
// would be both wasteful and no more accurate.
const usageCacheTTL = 60 * time.Second

// usageCache serves per-key usage without calling upstream on every page load.
//
// Keyed by the key's ref, so a rotation naturally misses the cache and reports
// the new key rather than serving the old one's history.
type usageCache struct {
	reporter keyprovider.UsageReporter

	mu      sync.Mutex
	entries map[string]usageEntry
}

type usageEntry struct {
	days      []keyprovider.DailyUsage
	fetchedAt time.Time
}

func newUsageCache(r keyprovider.UsageReporter) *usageCache {
	return &usageCache{reporter: r, entries: make(map[string]usageEntry)}
}

// Days returns the cached per-day usage for a key, refreshing when stale.
// A failed refresh returns nothing rather than a stale figure: a usage number
// that is quietly wrong is worse than no number.
func (c *usageCache) Days(ctx context.Context, ref string) []keyprovider.DailyUsage {
	if c.reporter == nil || ref == "" {
		return nil
	}

	c.mu.Lock()
	defer c.mu.Unlock()

	if e, ok := c.entries[ref]; ok && time.Since(e.fetchedAt) < usageCacheTTL {
		return e.days
	}

	days, err := c.reporter.Usage(ctx, ref, usageWindowDays)
	if err != nil {
		slog.Error("read key usage", "err", err)
		return nil
	}
	c.entries[ref] = usageEntry{days: days, fetchedAt: time.Now()}
	return days
}
