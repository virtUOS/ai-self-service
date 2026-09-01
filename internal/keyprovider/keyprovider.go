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

	// Quotas are fair-use allowances applied together, each resetting on its
	// own period, after which requests fail until that period rolls over. The
	// tightest window binds. Empty means unlimited.
	//
	// Where they are enforced is a provider concern: a provider that can bind
	// an allowance to a person may enforce the widest window there and the
	// rest on the key, so that the wide one survives a rotation.
	Quotas []QuotaWindow
}

// KeyRequest describes a key to be created.
type KeyRequest struct {
	// Alias identifies the key in the provider's own UI.
	Alias string
	// Owner is recorded as metadata so a key can be traced back to a person.
	Owner string
	// OwnerID identifies the person to the provider, stably across key
	// rotations. A provider that can hold an allowance against a person rather
	// than a key enforces the widest quota window there, so that regenerating
	// a key no longer resets it. Empty means the provider falls back to
	// enforcing everything on the key alone.
	OwnerID   string
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

// EmbeddingLister is implemented by providers that can say which of their
// models are embedding models, so the dashboard's example request shows the
// right endpoint and body for each. Separate from ModelLister because a
// gateway may be able to list models without classifying them.
type EmbeddingLister interface {
	EmbeddingModels(ctx context.Context) (map[string]bool, error)
}

// QuotaWindow is one allowance and the period it resets on.
type QuotaWindow struct {
	Tokens int64
	Period string // "1h" | "24h" | "7d" | "30d"
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

// WindowUsage is consumption against one of a profile's quota windows.
//
// Reported per window because a profile can hold several at once and the
// tightest one binds: a user with headroom on their monthly allowance can
// still be blocked by an hourly cap, so showing only one window misstates
// what they have left.
type WindowUsage struct {
	// Period is the window this covers ("1h" | "24h" | "7d" | "30d").
	Period string
	// UsedTokens is consumption since the window last reset. It is derived
	// from the spend log rather than read from the gateway, which keeps a
	// per-window counter but does not expose it.
	UsedTokens int64
	// LimitTokens is the allowance for this window.
	LimitTokens int64
	// ResetsAt is when this window rolls over.
	ResetsAt time.Time
	// UsedKnown reports whether UsedTokens is a real figure. Spend logging can
	// be switched off, and a bar drawn from a silent zero would claim a full
	// allowance the user may not have.
	UsedKnown bool
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

	// Windows reports consumption against every quota window that applies to
	// the key and its owner, tightest period first.
	//
	// Returns nothing when the key has no quota, or when the windows cannot be
	// determined — callers fall back to Quota.
	Windows(ctx context.Context, ref, ownerID string) ([]WindowUsage, error)

	// Quota reports consumption against the enforced allowance, in the window
	// the gateway resets on.
	//
	// ownerID names the person the key belongs to. Where an allowance is held
	// against the person, that is the figure reported: it is the one that
	// binds after a rotation, and the one a user cannot reset by regenerating.
	Quota(ctx context.Context, ref, ownerID string) (Quota, error)
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
	//
	// ownerID identifies the person the key belongs to, so that a provider
	// enforcing part of the allowance against the person can update that too.
	// Empty means enforce everything on the key.
	UpdateLimits(ctx context.Context, ref, ownerID string, limits Limits) error
}
