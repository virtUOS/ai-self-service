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
		{Budget: 0.0001, Period: "1h"},
		{Budget: 0.1, Period: "30d"},
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
	t.Logf("via key 1: limit=%v used=%v", q1.Limit, q1.Used)
	if q1.Limit != 0.1 {
		t.Errorf("limit = %v, want the 30d allowance on the owner", q1.Limit)
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
	t.Logf("via key 2: limit=%v used=%v", q2.Limit, q2.Used)

	if q2.Limit != q1.Limit {
		t.Errorf("allowance changed across rotation: %v then %v", q1.Limit, q2.Limit)
	}
	if q2.Used != q1.Used {
		t.Errorf("spend reset across rotation: %v then %v", q1.Used, q2.Used)
	}
}
