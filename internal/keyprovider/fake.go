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
	// Created, Deleted, Extended and Relimited record calls in order.
	Created   []KeyRequest
	Deleted   []string
	Extended  []string
	Relimited []string
	// RelimitedOwners records the owner passed alongside each Relimited ref.
	RelimitedOwners []string

	// LimitsByRef is the live limits per key, updated by UpdateLimits.
	LimitsByRef map[string]Limits
	// LimitsErr forces UpdateLimits to fail.
	LimitsErr error

	// AvailableModels is what ListModels returns; ModelsErr forces it to fail.
	AvailableModels []string
	ModelsErr       error

	// UsageByRef is what Usage returns per key ref; UsageErr forces it to fail.
	UsageByRef map[string][]DailyUsage
	UsageErr   error

	// TotalByRef is what TotalUsage returns per key ref, standing in for a
	// gateway that records spend but keeps no per-request log.
	TotalByRef map[string]int64
	TotalErr   error

	// QuotaByRef is what Quota returns per key ref; QuotaErr forces failure.
	QuotaByRef map[string]Quota
	QuotaErr   error

	// QuotaByOwner is the allowance held against a person rather than a key.
	// It takes precedence over QuotaByRef, mirroring the real gateway, so a
	// test can assert that a figure survives a key rotation.
	QuotaByOwner map[string]Quota

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

// UpdateLimits records a re-application of limits to an existing key.
func (f *Fake) UpdateLimits(_ context.Context, ref, ownerID string, l Limits) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.LimitsErr != nil {
		return f.LimitsErr
	}
	if f.LimitsByRef == nil {
		f.LimitsByRef = make(map[string]Limits)
	}
	f.LimitsByRef[ref] = l
	f.Relimited = append(f.Relimited, ref)
	f.RelimitedOwners = append(f.RelimitedOwners, ownerID)
	return nil
}

// Quota returns the allowance figures for a key, preferring the one held
// against its owner where there is one — as the real gateway does, so that the
// figure does not reset when the key is rotated.
func (f *Fake) Quota(_ context.Context, ref, ownerID string) (Quota, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.QuotaErr != nil {
		return Quota{}, f.QuotaErr
	}
	if q, ok := f.QuotaByOwner[ownerID]; ok && ownerID != "" {
		return q, nil
	}
	return f.QuotaByRef[ref], nil
}

// TotalUsage returns the canned cumulative total for a key.
func (f *Fake) TotalUsage(_ context.Context, ref string) (int64, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.TotalErr != nil {
		return 0, f.TotalErr
	}
	return f.TotalByRef[ref], nil
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
