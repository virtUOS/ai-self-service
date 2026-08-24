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

	// QuotaTokens is a fair-use allowance in tokens, reset every QuotaPeriod.
	// Zero means unlimited. The provider adapter converts it to whatever unit
	// the upstream enforces in.
	QuotaTokens int64  `bun:"quota_tokens,notnull"`
	QuotaPeriod string `bun:"quota_period,notnull"` // "1h" | "24h" | "7d" | "30d"

	IsDefault bool      `bun:"is_default,notnull"`
	CreatedAt time.Time `bun:"created_at,notnull"`
	UpdatedAt time.Time `bun:"updated_at,notnull"`
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
