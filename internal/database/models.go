package database

import (
	"time"

	"github.com/uptrace/bun"
)

type Profile struct {
	bun.BaseModel `bun:"table:profiles"`

	ID             int64    `bun:"id,pk,autoincrement"`
	Name           string   `bun:"name,unique,notnull"`
	Description    string   `bun:"description,notnull"`
	Models         []string `bun:"models,type:text"` // stored as JSON
	TPMLimit       *int64   `bun:"tpm_limit"`
	RPMLimit       *int64   `bun:"rpm_limit"`
	MaxBudget      *float64 `bun:"max_budget"`      // retained for commercial models
	BudgetDuration *string  `bun:"budget_duration"` // retained for commercial models

	// KeyDurationDays is how long a generated key stays valid. Zero means fall
	// back to the server-wide KEY_DURATION_DAYS.
	KeyDurationDays int `bun:"key_duration_days,notnull"`

	// QuotaTokens and QuotaPeriod are superseded by Quotas and no longer read.
	// Kept so a rollback to the previous release finds its quotas intact.
	QuotaTokens int64  `bun:"quota_tokens,notnull"`
	QuotaPeriod string `bun:"quota_period,notnull"`

	// Quotas are the fair-use allowances, each reset on its own period. Empty
	// means unlimited. Several windows apply at once, so the tightest one
	// binds — LiteLLM enforces each independently.
	Quotas []ProfileQuota `bun:"rel:has-many,join:id=profile_id"`

	IsDefault bool      `bun:"is_default,notnull"`
	CreatedAt time.Time `bun:"created_at,notnull"`
	UpdatedAt time.Time `bun:"updated_at,notnull"`
}

// ProfileQuota is one allowance window on a profile.
type ProfileQuota struct {
	bun.BaseModel `bun:"table:profile_quotas"`

	ID        int64  `bun:"id,pk,autoincrement"`
	ProfileID int64  `bun:"profile_id,notnull"`
	Tokens    int64  `bun:"tokens,notnull"`
	Period    string `bun:"period,notnull"` // "1h" | "24h" | "7d" | "30d"
}

type User struct {
	bun.BaseModel `bun:"table:users"`

	ID        int64     `bun:"id,pk,autoincrement"`
	OIDCSub   string    `bun:"oidc_sub,unique,notnull"`
	Email     string    `bun:"email,notnull"`
	Name      string    `bun:"name,notnull"`
	ProfileID *int64    `bun:"profile_id"`
	Profile   *Profile  `bun:"rel:belongs-to,join:profile_id=id"`
	CreatedAt time.Time `bun:"created_at,notnull"`
	UpdatedAt time.Time `bun:"updated_at,notnull"`
}

type APIKey struct {
	bun.BaseModel `bun:"table:api_keys"`

	ID         int64     `bun:"id,pk,autoincrement"`
	UserID     int64     `bun:"user_id,notnull"`
	LiteLLMKey string    `bun:"litellm_key,notnull"`
	KeyPrefix  string    `bun:"key_prefix,notnull"`
	ExpiresAt  time.Time `bun:"expires_at,notnull"`
	CreatedAt  time.Time `bun:"created_at,notnull"`
}

type Session struct {
	bun.BaseModel `bun:"table:sessions"`

	ID        int64     `bun:"id,pk,autoincrement"`
	UserID    int64     `bun:"user_id,notnull"`
	Token     string    `bun:"token,unique,notnull"`
	IDToken   string    `bun:"id_token,notnull"`
	ExpiresAt time.Time `bun:"expires_at,notnull"`
	CreatedAt time.Time `bun:"created_at,notnull"`
}

// AuditAction values recorded in AuditEvent.Action.
const (
	AuditKeyGenerated = "key.generated"
	AuditKeyExtended  = "key.extended"
	AuditKeyDeleted   = "key.deleted"
	AuditKeyRevoked   = "key.revoked" // by an admin, not the owner
	AuditProfileSet   = "user.profile_set"
)

// AuditEvent is an append-only record of a key or profile change.
//
// Emails are stored rather than referenced so history survives the deletion of
// the user or key it describes.
type AuditEvent struct {
	bun.BaseModel `bun:"table:audit_events"`

	ID           int64     `bun:"id,pk,autoincrement"`
	CreatedAt    time.Time `bun:"created_at,notnull"`
	Action       string    `bun:"action,notnull"`
	ActorEmail   string    `bun:"actor_email,notnull"`   // who performed it
	SubjectEmail string    `bun:"subject_email,notnull"` // whose key/profile it was
	SubjectID    *int64    `bun:"subject_id"`
	Detail       string    `bun:"detail,notnull"`
}

// ExpiryNotice records that a user was warned about a key nearing expiry,
// so the reminder job does not mail them again for the same threshold.
type ExpiryNotice struct {
	bun.BaseModel `bun:"table:expiry_notices"`

	ID         int64     `bun:"id,pk,autoincrement"`
	APIKeyID   int64     `bun:"api_key_id,notnull"`
	DaysBefore int       `bun:"days_before,notnull"`
	SentAt     time.Time `bun:"sent_at,notnull"`
}

// ExpiringKey pairs a key with its owner, for the reminder job.
type ExpiringKey struct {
	APIKey
	Email string
	Name  string
}
