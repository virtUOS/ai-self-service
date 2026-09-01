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
	lister   keyprovider.ModelLister
	embedder keyprovider.EmbeddingLister

	mu         sync.Mutex
	models     []string
	embeddings map[string]bool
	fetchedAt  time.Time
	embFetched time.Time
}

func newModelCache(l keyprovider.ModelLister) *modelCache {
	e, _ := l.(keyprovider.EmbeddingLister)
	return &modelCache{lister: l, embedder: e}
}

// Embeddings reports which models the gateway serves as embedding models, so
// the dashboard can show the right example request for each.
//
// A failed lookup returns nil rather than an error: every model then gets the
// chat example, which is what the page did before this existed. A wrong example
// for one model is a smaller harm than no model list at all.
func (c *modelCache) Embeddings(ctx context.Context) map[string]bool {
	c.mu.Lock()
	defer c.mu.Unlock()

	if c.embedder == nil {
		return nil
	}
	if time.Since(c.embFetched) < modelCacheTTL && c.embeddings != nil {
		return c.embeddings
	}

	emb, err := c.embedder.EmbeddingModels(ctx)
	if err != nil {
		slog.Error("list embedding models", "err", err)
		return c.embeddings
	}
	c.embeddings, c.embFetched = emb, time.Now()
	return emb
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
