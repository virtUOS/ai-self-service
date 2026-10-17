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
	"errors"
	"time"
)

// ErrKeyNotFound reports that a key no longer exists in the gateway, so no
// retry can bring it back: it was deleted outside the portal. Providers wrap
// it, so callers test with errors.Is.
var ErrKeyNotFound = errors.New("key not found in the gateway")

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

// User is the person a key belongs to, as the provider should know them.
type User struct {
	// ID identifies the person to the provider, stably across key rotations.
	// A provider that can hold an allowance against a person rather than a
	// key enforces the widest quota window there, so that regenerating a key
	// no longer resets it. Empty means the provider falls back to enforcing
	// everything on the key alone.
	ID string
	// Name labels the person in the provider's own UI. Empty leaves the label
	// as it is.
	Name string
	// Email is recorded on the person in the provider, where it may have to
	// be unique. Empty leaves it as it is.
	Email string
}

// KeyRequest describes a key to be created.
type KeyRequest struct {
	// Alias identifies the key in the provider's own UI.
	Alias string
	// User is the person the key is bound to.
	User      User
	ExpiresAt time.Time
	Limits    Limits
}

// KeyResult is what a provider returns when a key is created.
type KeyResult struct {
	// Secret is shown to the user once and never stored.
	Secret string
	// Ref identifies the key to the provider for every later call: revoke,
	// update, usage. It is what the portal stores, so it must not work as a
	// credential: an opaque id or a hash of the secret, never the secret.
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

// QuotaWindow is one allowance and the period it resets on. Budget is spend,
// not tokens: LiteLLM enforces spend, and with models priced differently a
// token figure would not be exact.
type QuotaWindow struct {
	Budget float64 // spend allowed per period, in the gateway's pricing unit
	Period string  // "1h" | "24h" | "7d" | "30d"
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
	// Used is consumption since the window last reset, as the spend the
	// gateway counts against this window.
	Used float64
	// Limit is the allowance for this window.
	Limit float64
	// ResetsAt is when this window rolls over.
	ResetsAt time.Time
}

// DailyUsage is one day's token consumption for a person.
type DailyUsage struct {
	// Day is the UTC date the tokens were spent on, as YYYY-MM-DD.
	Day string
	// Tokens is the total consumed that day, prompt and completion together.
	Tokens int64
}

// ModelUsage is one model's share of a person's consumption.
type ModelUsage struct {
	Model            string
	Requests         int64
	PromptTokens     int64
	CompletionTokens int64
	TotalTokens      int64
}

// History is what a person consumed over a window, in the two shapes the
// dashboard draws: a per-day series and a per-model breakdown. They are one
// value because they come from one read of the same daily totals.
type History struct {
	// Days holds per-day token totals, oldest first. Days with no traffic are
	// omitted rather than reported as zero.
	Days []DailyUsage
	// Models holds per-model totals, largest TotalTokens first.
	Models []ModelUsage
}

// UsageReporter is implemented by providers that can report what has been
// consumed. Separate from Provider because not every gateway records usage,
// and reporting is not needed to issue or revoke keys.
type UsageReporter interface {
	// History returns what ownerID consumed over the last days days, across
	// every key they have held: per-day totals oldest first and per-model
	// totals largest first. It follows the person rather than the key, so a
	// rotation does not start the chart over.
	History(ctx context.Context, ownerID string, days int) (History, error)

	// Windows reports consumption against every quota window that applies to
	// the key and its owner, tightest period first. It is the whole of the
	// allowance: nothing when neither the key nor its owner has a quota.
	//
	// ownerID names the person the key belongs to. Where an allowance is held
	// against the person, it is reported with the person's spend: it is the
	// one that binds after a rotation, and the one a user cannot reset by
	// regenerating.
	//
	// Every window comes with the spend the gateway counts against it. When
	// that cannot be read for a window, Windows fails rather than report a
	// silent zero.
	Windows(ctx context.Context, ref, ownerID string) ([]WindowUsage, error)
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
	// user is the person the key belongs to, so that a provider enforcing
	// part of the allowance against the person can update that too, along
	// with how the person is labelled there. An empty ID means enforce
	// everything on the key.
	UpdateLimits(ctx context.Context, ref string, user User, limits Limits) error

	// UpdateAlias relabels an existing key, so the label follows its owner's
	// username (see NewAlias). The alias must be unique, as on creation.
	UpdateAlias(ctx context.Context, ref, alias string) error

	// CheckKey reads whether the gateway still has a key, without changing
	// anything: nil when it does, an error wrapping ErrKeyNotFound when it
	// does not, and any other error when the answer is unknown. The sync uses
	// it on keys it has marked as missing, which it must not push to.
	CheckKey(ctx context.Context, ref string) error
}
