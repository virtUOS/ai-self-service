package litellm

import (
	"context"
	"os"
	"testing"
	"time"

	"github.com/virtuos/ai-self-service/internal/keyprovider"
)

// Manual end-to-end check against a real gateway. Skipped unless
// LITELLM_E2E=1, so the normal suite still needs nothing external.
func TestE2EOwnerQuotaSurvivesRotation(t *testing.T) {
	if os.Getenv("LITELLM_E2E") != "1" {
		t.Skip("set LITELLM_E2E=1 with LITELLM_BASE_URL and LITELLM_MASTER_KEY")
	}

	p := NewProvider(NewClient(os.Getenv("LITELLM_BASE_URL"), os.Getenv("LITELLM_MASTER_KEY")))
	ctx := context.Background()
	owner := "zz-probe-e2e-issue26"

	limits := keyprovider.Limits{Quotas: []keyprovider.QuotaWindow{
		{Tokens: 1_000, Period: "1h"},
		{Tokens: 1_000_000, Period: "30d"},
	}}
	req := keyprovider.KeyRequest{
		Alias: owner + "-1", Owner: owner, OwnerID: owner, Limits: limits,
		// The gateway rejects a zero expiry as an invalid duration.
		ExpiresAt: time.Now().AddDate(0, 0, 1),
	}

	k1, err := p.CreateKey(ctx, req)
	if err != nil {
		t.Fatalf("create first key: %v", err)
	}
	defer p.DeleteKey(ctx, k1.Ref)

	q1, err := p.Quota(ctx, k1.Ref, owner)
	if err != nil {
		t.Fatalf("quota via first key: %v", err)
	}
	t.Logf("via key 1: limit=%d used=%d", q1.LimitTokens, q1.UsedTokens)
	if q1.LimitTokens != 1_000_000 {
		t.Errorf("limit = %d, want the 30d allowance on the owner", q1.LimitTokens)
	}

	// The rotation issue #26 reported.
	req.Alias = owner + "-2"
	k2, err := p.CreateKey(ctx, req)
	if err != nil {
		t.Fatalf("create replacement key: %v", err)
	}
	defer p.DeleteKey(ctx, k2.Ref)

	q2, err := p.Quota(ctx, k2.Ref, owner)
	if err != nil {
		t.Fatalf("quota via replacement key: %v", err)
	}
	t.Logf("via key 2: limit=%d used=%d", q2.LimitTokens, q2.UsedTokens)

	if q2.LimitTokens != q1.LimitTokens {
		t.Errorf("allowance changed across rotation: %d then %d", q1.LimitTokens, q2.LimitTokens)
	}
	if q2.UsedTokens != q1.UsedTokens {
		t.Errorf("spend reset across rotation: %d then %d", q1.UsedTokens, q2.UsedTokens)
	}
}
