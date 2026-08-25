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

// TotalUsage reports the key's cumulative token count.
func (p *Provider) TotalUsage(ctx context.Context, ref string) (int64, error) {
	return p.client.KeySpendTokens(ctx, ref)
}

// ListModels reports the models the gateway serves.
func (p *Provider) ListModels(ctx context.Context) ([]string, error) {
	return p.client.ListModels(ctx)
}

func (p *Provider) CreateKey(ctx context.Context, req keyprovider.KeyRequest) (keyprovider.KeyResult, error) {
	secret, err := p.client.CreateKey(ctx, req.Alias, toKeyParams(req), req.ExpiresAt)
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
func toKeyParams(req keyprovider.KeyRequest) KeyParams {
	models := req.Limits.Models
	if len(models) == 0 {
		// LiteLLM reads an empty list as "no models"; nil means "all".
		models = nil
	}

	params := KeyParams{
		Models:   models,
		TPMLimit: req.Limits.TokensPerMinute,
		RPMLimit: req.Limits.RequestsPerMinute,
		Metadata: map[string]any{"user_email": req.Owner},
	}

	// A token allowance is expressed upstream as a spend cap that resets each
	// period. Priced identically for input and output, so the conversion is
	// exact regardless of how the tokens are actually used.
	if req.Limits.QuotaTokens > 0 && req.Limits.QuotaPeriod != "" {
		budget := TokensToBudget(req.Limits.QuotaTokens)
		period := req.Limits.QuotaPeriod
		params.MaxBudget = &budget
		params.BudgetDuration = &period
	}
	return params
}
