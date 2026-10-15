package handlers

import (
	"context"
	"log/slog"
	"sync"
	"time"

	"github.com/virtuos/keyosk/internal/keyprovider"
)

// usageWindowDays is how far back the dashboard reports unless configured
// otherwise (USAGE_HISTORY_DAYS). Long enough to cover a month's key
// validity.
const usageWindowDays = 30

// usageCacheTTL bounds how stale a usage report can be. LiteLLM takes about
// fifteen seconds to propagate spend anyway, so a fresh read per page load
// would be both wasteful and no more accurate.
const usageCacheTTL = 60 * time.Second

// usageCache serves usage without calling upstream on every page load.
//
// The history is keyed by the person it belongs to, as it is upstream, so a
// rotation keeps serving it.
//
// Every accessor holds the same invariant: a failed refresh returns the error
// and no figure rather than a stale one, and leaves no entry behind for a
// later read to pick up. The dashboard says so instead of showing a silently
// shorter report.
type usageCache struct {
	reporter keyprovider.UsageReporter
	// windowDays is how far back History reaches.
	windowDays int

	mu      sync.Mutex
	entries map[string]usageEntry
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
	}
}

// History returns the cached per-day and per-model usage of a person,
// refreshing when stale.
//
// Both halves come from one upstream read, so they are cached as one entry and
// can never disagree about the window they cover. A failed refresh returns an
// empty History with the error and caches nothing.
func (c *usageCache) History(ctx context.Context, ownerID string) (keyprovider.History, error) {
	if c.reporter == nil || ownerID == "" {
		return keyprovider.History{}, nil
	}

	c.mu.Lock()
	defer c.mu.Unlock()

	if e, ok := c.entries[ownerID]; ok && time.Since(e.fetchedAt) < usageCacheTTL {
		return e.history, nil
	}

	h, err := c.reporter.History(ctx, ownerID, c.windowDays)
	if err != nil {
		slog.Error("read usage history", "err", err)
		return keyprovider.History{}, err
	}
	c.entries[ownerID] = usageEntry{history: h, fetchedAt: time.Now()}
	return h, nil
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
