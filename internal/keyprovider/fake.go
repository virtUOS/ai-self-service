package keyprovider

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"time"
)

// Fake is an in-memory Provider for tests. It records what it was asked to do
// and can be made to fail on demand, so error paths — the ones that leave keys
// orphaned upstream if handled wrongly — can be exercised without a gateway.
type Fake struct {
	mu      sync.Mutex
	counter int

	// Keys holds every live key by ref.
	Keys map[string]KeyRequest
	// Created, Deleted and Extended record calls in order.
	Created  []KeyRequest
	Deleted  []string
	Extended []string

	// AvailableModels is what ListModels returns; ModelsErr forces it to fail.
	AvailableModels []string
	ModelsErr       error

	// UsageByRef is what Usage returns per key ref; UsageErr forces it to fail.
	UsageByRef map[string][]DailyUsage
	UsageErr   error

	// CreateErr, DeleteErr and ExpiryErr force the respective call to fail.
	CreateErr error
	DeleteErr error
	ExpiryErr error
}

func NewFake() *Fake {
	return &Fake{Keys: make(map[string]KeyRequest)}
}

var (
	_ Provider      = (*Fake)(nil)
	_ ModelLister   = (*Fake)(nil)
	_ UsageReporter = (*Fake)(nil)
)

// Usage returns the canned per-day totals for a key.
func (f *Fake) Usage(_ context.Context, ref string, _ int) ([]DailyUsage, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.UsageErr != nil {
		return nil, f.UsageErr
	}
	return f.UsageByRef[ref], nil
}

// Models is what ListModels returns.
func (f *Fake) ListModels(context.Context) ([]string, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.ModelsErr != nil {
		return nil, f.ModelsErr
	}
	return f.AvailableModels, nil
}

func (f *Fake) CreateKey(_ context.Context, req KeyRequest) (KeyResult, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.CreateErr != nil {
		return KeyResult{}, f.CreateErr
	}
	// LiteLLM requires aliases to be unique across all live keys and rejects a
	// collision with a 400. Rotation creates the replacement before revoking the
	// old key, so the two briefly coexist and a per-user constant alias would
	// collide. Enforce it here so that stays caught by tests.
	for _, live := range f.Keys {
		if live.Alias == req.Alias {
			return KeyResult{}, fmt.Errorf("key with alias %q already exists", req.Alias)
		}
	}
	f.counter++
	secret := fmt.Sprintf("sk-fake-%03d", f.counter)
	f.Keys[secret] = req
	f.Created = append(f.Created, req)
	return KeyResult{Secret: secret, Ref: secret}, nil
}

func (f *Fake) DeleteKey(_ context.Context, ref string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.DeleteErr != nil {
		return f.DeleteErr
	}
	if _, ok := f.Keys[ref]; !ok {
		return errors.New("key not found")
	}
	delete(f.Keys, ref)
	f.Deleted = append(f.Deleted, ref)
	return nil
}

func (f *Fake) UpdateExpiry(_ context.Context, ref string, expiresAt time.Time) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.ExpiryErr != nil {
		return f.ExpiryErr
	}
	req, ok := f.Keys[ref]
	if !ok {
		return errors.New("key not found")
	}
	req.ExpiresAt = expiresAt
	f.Keys[ref] = req
	f.Extended = append(f.Extended, ref)
	return nil
}

// LiveCount reports how many keys the provider still holds — the check that
// catches a key leaked upstream because a handler failed to roll back.
func (f *Fake) LiveCount() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return len(f.Keys)
}
