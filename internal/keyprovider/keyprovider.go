// Package keyprovider defines how the portal issues and revokes API keys,
// independently of which gateway actually does it.
//
// The types here are the application's own vocabulary. They deliberately do not
// mirror any provider's request shape: a provider adapter translates. That
// keeps provider quirks — LiteLLM enforcing quotas as spend rather than tokens,
// for instance — from leaking into handlers and templates.
package keyprovider

import (
	"context"
	"time"
)

// Limits are the constraints applied to an issued key.
type Limits struct {
	// Models the key may use. Empty means no restriction.
	Models []string

	// TokensPerMinute and RequestsPerMinute bound bursts. Nil means unlimited.
	TokensPerMinute   *int64
	RequestsPerMinute *int64

	// QuotaTokens is a fair-use allowance consumed over QuotaPeriod, after
	// which requests fail until the period resets. Zero means unlimited.
	QuotaTokens int64
	QuotaPeriod string // "1h" | "24h" | "7d" | "30d"
}

// KeyRequest describes a key to be created.
type KeyRequest struct {
	// Alias identifies the key in the provider's own UI.
	Alias string
	// Owner is recorded as metadata so a key can be traced back to a person.
	Owner     string
	ExpiresAt time.Time
	Limits    Limits
}

// KeyResult is what a provider returns when a key is created.
type KeyResult struct {
	// Secret is shown to the user once and never stored in full.
	Secret string
	// Ref identifies the key for later revocation. For providers that can
	// revoke by an opaque id this is that id; otherwise it is the secret.
	Ref string
}

// ModelLister is implemented by providers that can enumerate the models they
// serve, so the admin UI can offer a real list instead of a free-text field.
// It is separate from Provider because not every gateway can do this.
type ModelLister interface {
	ListModels(ctx context.Context) ([]string, error)
}

// Quota is a key's consumption against the allowance the gateway enforces.
// It is reported separately from usage because it resets on the budget period,
// which need not match the window usage is reported over.
type Quota struct {
	// UsedTokens is consumption in the current window.
	UsedTokens int64
	// LimitTokens is the allowance. Zero means the key is unlimited, which is
	// different from an allowance that has been fully consumed.
	LimitTokens int64
	// ResetsAt is when the window rolls over. Zero when unknown or unlimited.
	ResetsAt time.Time
}

// DailyUsage is one day's token consumption for a key.
type DailyUsage struct {
	// Day is the UTC date the tokens were spent on, as YYYY-MM-DD.
	Day string
	// Tokens is the total consumed that day, prompt and completion together.
	Tokens int64
}

// UsageReporter is implemented by providers that can report what a key has
// consumed. Separate from Provider because not every gateway records usage,
// and reporting is not needed to issue or revoke keys.
type UsageReporter interface {
	// Usage returns per-day token totals for the key, oldest first, covering
	// the given number of days back from today. Days with no traffic are
	// omitted rather than reported as zero.
	//
	// An empty result does not mean no usage: a gateway may record spend
	// without keeping a per-request log. Callers should fall back to
	// TotalUsage before concluding a key is unused.
	Usage(ctx context.Context, ref string, days int) ([]DailyUsage, error)

	// TotalUsage is the key's cumulative token count. It is the coarse figure
	// that survives when per-request logging is unavailable, so it is reported
	// separately rather than derived from Usage.
	TotalUsage(ctx context.Context, ref string) (int64, error)

	// Quota reports consumption against the enforced allowance, in the window
	// the gateway resets on.
	Quota(ctx context.Context, ref string) (Quota, error)
}

// Provider issues and revokes keys on an upstream gateway.
type Provider interface {
	// CreateKey issues a new key.
	CreateKey(ctx context.Context, req KeyRequest) (KeyResult, error)
	// DeleteKey revokes a key by the Ref returned from CreateKey.
	DeleteKey(ctx context.Context, ref string) error
	// UpdateExpiry moves a key's expiry.
	UpdateExpiry(ctx context.Context, ref string, expiresAt time.Time) error

	// UpdateLimits re-applies limits to an existing key. Issuing a key is not
	// the only time its limits change: users move between profiles and
	// profiles get edited, and without this the portal would advertise limits
	// the gateway does not enforce.
	UpdateLimits(ctx context.Context, ref string, limits Limits) error
}
