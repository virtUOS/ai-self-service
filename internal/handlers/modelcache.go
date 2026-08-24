package handlers

import (
	"context"
	"log/slog"
	"sync"
	"time"

	"github.com/virtuos/ai-self-service/internal/keyprovider"
)

// modelCacheTTL bounds how stale the admin form's model list can be. Models
// change rarely, and a stale entry only affects which checkboxes are offered.
const modelCacheTTL = 5 * time.Minute

// modelCache serves the gateway's model list to the admin form without calling
// upstream on every page load.
//
// A failed refresh keeps serving the last good list: an admin editing an
// unrelated field should not lose the model checkboxes because the gateway
// blipped.
type modelCache struct {
	lister keyprovider.ModelLister

	mu        sync.Mutex
	models    []string
	fetchedAt time.Time
}

func newModelCache(l keyprovider.ModelLister) *modelCache {
	return &modelCache{lister: l}
}

// Models returns the cached list, refreshing it when stale.
func (c *modelCache) Models(ctx context.Context) []string {
	c.mu.Lock()
	defer c.mu.Unlock()

	if c.lister == nil {
		return nil
	}
	if time.Since(c.fetchedAt) < modelCacheTTL && c.models != nil {
		return c.models
	}

	models, err := c.lister.ListModels(ctx)
	if err != nil {
		slog.Error("list models for admin form", "err", err)
		return c.models // possibly nil on the very first failure
	}
	c.models, c.fetchedAt = models, time.Now()
	return models
}
