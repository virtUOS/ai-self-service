package litellm

import (
	"context"
	"time"

	"github.com/virtuos/ai-self-service/internal/keyprovider"
)

// Provider adapts the LiteLLM client to keyprovider.Provider.
//
// This is where LiteLLM's vocabulary is translated into the application's. In
// particular a token quota becomes a spend cap over a reset window, because
// LiteLLM enforces budgets in currency and has no native token quota.
type Provider struct {
	client *Client
}

// NewProvider wraps a client as a keyprovider.Provider.
func NewProvider(c *Client) *Provider { return &Provider{client: c} }

var (
	_ keyprovider.Provider      = (*Provider)(nil)
	_ keyprovider.ModelLister   = (*Provider)(nil)
	_ keyprovider.UsageReporter = (*Provider)(nil)
)

// Usage reports what a key has consumed, per day.
func (p *Provider) Usage(ctx context.Context, ref string, days int) ([]keyprovider.DailyUsage, error) {
	return p.client.Usage(ctx, ref, days)
}

// UpdateLimits re-applies limits to an existing key, and to the allowance held
// against its owner.
//
// Both halves are updated: a profile edit can change the widest window, which
// lives on the user, as easily as a burst window, which lives on the key.
func (p *Provider) UpdateLimits(ctx context.Context, ref, ownerID string, l keyprovider.Limits) error {
	widest, rest := WidestWindow(effectiveWindows(l))

	if ownerID != "" {
		if err := p.client.UpsertUser(ctx, ownerID, userBudget(widest)); err != nil {
			return err
		}
		l.Quotas = rest
	}
	return p.client.UpdateKeyLimits(ctx, ref, l)
}

// Quota reports consumption against the enforced allowance.
//
// The owner's allowance is the one reported when there is one: it is the
// widest window, so it is the limit users actually reach, and unlike the key's
// own counter it survives a rotation. Keys issued before the portal tracked
// owners have no user upstream, and fall back to the key's own figure.
func (p *Provider) Quota(ctx context.Context, ref, ownerID string) (keyprovider.Quota, error) {
	if ownerID != "" {
		q, err := p.client.UserQuota(ctx, ownerID)
		if err != nil {
			return keyprovider.Quota{}, err
		}
		if q.LimitTokens > 0 {
			return q, nil
		}
	}
	return p.client.KeyQuota(ctx, ref)
}

// userBudget is the allowance to hold against the person, or nil when the
// profile sets no quota at all.
func userBudget(w keyprovider.QuotaWindow) *UserBudget {
	if w.Tokens <= 0 || w.Period == "" {
		return nil
	}
	return &UserBudget{Tokens: w.Tokens, Period: w.Period}
}

// TotalUsage reports the key's cumulative token count.
func (p *Provider) TotalUsage(ctx context.Context, ref string) (int64, error) {
	return p.client.KeySpendTokens(ctx, ref)
}

// ListModels reports the models the gateway serves.
func (p *Provider) ListModels(ctx context.Context) ([]string, error) {
	return p.client.ListModels(ctx)
}

// CreateKey issues a key, and binds it to its owner so that the widest quota
// window is enforced against the person rather than the key.
//
// The user is upserted first: a key created before its user exists would be
// enforced against no allowance at all until the next update.
//
// A failure after the upsert leaves the allowance behind with no key attached.
// That is deliberate and is not rolled back: the upsert is idempotent, so the
// next attempt reuses it, and an orphaned allowance restricts nobody. Undoing
// it would be the dangerous direction — a retry would then find no allowance
// and could issue a key that enforces nothing.
func (p *Provider) CreateKey(ctx context.Context, req keyprovider.KeyRequest) (keyprovider.KeyResult, error) {
	if req.OwnerID != "" {
		widest, rest := WidestWindow(effectiveWindows(req.Limits))
		if err := p.client.UpsertUser(ctx, req.OwnerID, userBudget(widest)); err != nil {
			return keyprovider.KeyResult{}, err
		}
		req.Limits.Quotas = rest
	}

	secret, err := p.client.CreateKey(ctx, req.Alias, p.toKeyParams(req), req.ExpiresAt)
	if err != nil {
		return keyprovider.KeyResult{}, err
	}
	// LiteLLM revokes by the key itself, so the ref is the secret.
	return keyprovider.KeyResult{Secret: secret, Ref: secret}, nil
}

func (p *Provider) DeleteKey(ctx context.Context, ref string) error {
	return p.client.DeleteKey(ctx, ref)
}

func (p *Provider) UpdateExpiry(ctx context.Context, ref string, expiresAt time.Time) error {
	return p.client.UpdateKeyExpiry(ctx, ref, expiresAt)
}

// toKeyParams converts the neutral request into LiteLLM's wire shape.
func (p *Provider) toKeyParams(req keyprovider.KeyRequest) KeyParams {
	models := req.Limits.Models
	if len(models) == 0 {
		// LiteLLM reads an empty list as "no models"; nil means "all".
		models = nil
	}

	params := KeyParams{
		Models:   models,
		TPMLimit: req.Limits.TokensPerMinute,
		RPMLimit: req.Limits.RequestsPerMinute,
		UserID:   req.OwnerID,
		Metadata: map[string]any{"user_email": req.Owner},
	}

	// A token allowance is expressed upstream as a spend cap that resets each
	// period. Priced identically for input and output, so the conversion is
	// exact regardless of how the tokens are actually used.
	//
	// Several windows go as budget_limits, which the gateway enforces
	// independently; a single one keeps the flat pair it has always used.
	switch windows := effectiveWindows(req.Limits); len(windows) {
	case 0:
	case 1:
		budget := p.client.TokensToBudget(windows[0].Tokens)
		period := windows[0].Period
		params.MaxBudget = &budget
		params.BudgetDuration = &period
	default:
		limits := make([]BudgetWindow, 0, len(windows))
		for _, w := range windows {
			limits = append(limits, BudgetWindow{
				BudgetDuration: w.Period,
				MaxBudget:      p.client.TokensToBudget(w.Tokens),
			})
		}
		params.BudgetLimits = limits
	}
	return params
}
