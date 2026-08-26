package litellm

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/virtuos/ai-self-service/internal/keyprovider"
)

// gatewayStub records what the portal sends to each endpoint.
type gatewayStub struct {
	srv        *httptest.Server
	userBodies []map[string]any
	keyBodies  []map[string]any
}

func newGatewayStub(t *testing.T) *gatewayStub {
	t.Helper()
	g := &gatewayStub{}
	g.srv = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var body map[string]any
		if b, _ := io.ReadAll(r.Body); len(b) > 0 {
			json.Unmarshal(b, &body)
		}
		switch r.URL.Path {
		case "/user/new", "/user/update":
			g.userBodies = append(g.userBodies, body)
			w.Write([]byte(`{}`))
		case "/key/generate":
			g.keyBodies = append(g.keyBodies, body)
			w.Write([]byte(`{"key":"sk-new"}`))
		case "/key/update":
			g.keyBodies = append(g.keyBodies, body)
			w.Write([]byte(`{}`))
		default:
			w.Write([]byte(`{}`))
		}
	}))
	t.Cleanup(g.srv.Close)
	return g
}

// Issue #26: regenerating a key reset the quota, so anyone at their limit
// could issue a new key and carry on. The widest window must therefore be
// enforced against the person, where a new key cannot reset it, and only the
// short burst windows may live on the key itself.
func TestCreateKeyPutsWidestWindowOnTheOwner(t *testing.T) {
	g := newGatewayStub(t)

	_, err := NewProvider(NewClient(g.srv.URL, "mk")).CreateKey(context.Background(), keyprovider.KeyRequest{
		Alias:   "someone@uni-osnabrueck.de",
		Owner:   "someone@uni-osnabrueck.de",
		OwnerID: "oidc-sub-123",
		Limits: keyprovider.Limits{Quotas: []keyprovider.QuotaWindow{
			{Tokens: 1_000, Period: "1h"},
			{Tokens: 1_000_000, Period: "30d"},
			{Tokens: 10_000, Period: "7d"},
		}},
	})
	if err != nil {
		t.Fatal(err)
	}

	if len(g.userBodies) != 1 {
		t.Fatalf("made %d user calls, want exactly one", len(g.userBodies))
	}
	user := g.userBodies[0]
	if user["user_id"] != "oidc-sub-123" {
		t.Errorf("user_id = %v, want the owner's stable id", user["user_id"])
	}
	// The 30d window is the one worth rotating a key to escape.
	if user["budget_duration"] != "30d" {
		t.Errorf("owner budget window = %v, want 30d (the widest)", user["budget_duration"])
	}
	if user["max_budget"] != TokensToBudget(1_000_000) {
		t.Errorf("owner budget = %v, want the 30d allowance", user["max_budget"])
	}

	if len(g.keyBodies) != 1 {
		t.Fatalf("made %d key calls, want exactly one", len(g.keyBodies))
	}
	key := g.keyBodies[0]
	if key["user_id"] != "oidc-sub-123" {
		t.Errorf("key user_id = %v; without it the owner budget is not enforced", key["user_id"])
	}
	// Only the two shorter windows remain on the key.
	limits, _ := key["budget_limits"].([]any)
	if len(limits) != 2 {
		t.Fatalf("key carries %d windows, want the 2 shorter ones", len(limits))
	}
	for _, l := range limits {
		if w, _ := l.(map[string]any); w["budget_duration"] == "30d" {
			t.Error("the widest window is still on the key, so a rotation resets it")
		}
	}
}

// The user is created before the key. A key attached to a user that does not
// exist yet would be enforced against no allowance at all.
func TestCreateKeyUpsertsOwnerBeforeIssuingKey(t *testing.T) {
	var order []string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		order = append(order, r.URL.Path)
		if r.URL.Path == "/key/generate" {
			w.Write([]byte(`{"key":"sk-new"}`))
			return
		}
		w.Write([]byte(`{}`))
	}))
	defer srv.Close()

	_, err := NewProvider(NewClient(srv.URL, "mk")).CreateKey(context.Background(), keyprovider.KeyRequest{
		Alias: "a", Owner: "a", OwnerID: "sub-1",
		Limits: keyprovider.Limits{Quotas: []keyprovider.QuotaWindow{{Tokens: 100, Period: "30d"}}},
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(order) < 2 || order[0] != "/user/new" || order[1] != "/key/generate" {
		t.Errorf("call order = %v, want the user upserted before the key", order)
	}
}

// A profile with a single window puts it on the owner, leaving the key with
// none: that window is the whole allowance, and it must survive a rotation.
func TestCreateKeySingleWindowGoesToTheOwner(t *testing.T) {
	g := newGatewayStub(t)

	_, err := NewProvider(NewClient(g.srv.URL, "mk")).CreateKey(context.Background(), keyprovider.KeyRequest{
		Alias: "a", Owner: "a", OwnerID: "sub-1",
		Limits: keyprovider.Limits{Quotas: []keyprovider.QuotaWindow{{Tokens: 500_000, Period: "7d"}}},
	})
	if err != nil {
		t.Fatal(err)
	}
	if g.userBodies[0]["budget_duration"] != "7d" {
		t.Errorf("owner window = %v, want 7d", g.userBodies[0]["budget_duration"])
	}
	key := g.keyBodies[0]
	if key["max_budget"] != nil || key["budget_limits"] != nil {
		t.Errorf("key kept an allowance (%v / %v); a rotation would reset it",
			key["max_budget"], key["budget_limits"])
	}
}

// A profile with no quota leaves the owner unlimited, and must still clear any
// allowance previously set on them.
func TestCreateKeyWithoutQuotaClearsOwnerBudget(t *testing.T) {
	g := newGatewayStub(t)

	_, err := NewProvider(NewClient(g.srv.URL, "mk")).CreateKey(context.Background(), keyprovider.KeyRequest{
		Alias: "a", Owner: "a", OwnerID: "sub-1",
	})
	if err != nil {
		t.Fatal(err)
	}
	user := g.userBodies[0]
	for _, f := range []string{"max_budget", "budget_duration"} {
		v, ok := user[f]
		if !ok || v != nil {
			t.Errorf("%s = %v (present=%v), want an explicit null", f, v, ok)
		}
	}
}

// The quota shown is the owner's, since that is the binding one after a
// rotation. The key's own counter must not be consulted when the owner has an
// allowance — that counter is exactly what resets.
func TestQuotaPrefersTheOwnerAllowance(t *testing.T) {
	var asked []string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		asked = append(asked, r.URL.Path)
		if r.URL.Path == "/user/info" {
			w.Write([]byte(`{"user_info":{"spend":0.05,"max_budget":0.1}}`))
			return
		}
		w.Write([]byte(`{"info":{"spend":0.0,"max_budget":0.1}}`))
	}))
	defer srv.Close()

	q, err := NewProvider(NewClient(srv.URL, "mk")).Quota(context.Background(), "sk-fresh", "sub-1")
	if err != nil {
		t.Fatal(err)
	}
	if q.UsedTokens != BudgetToTokens(0.05) {
		t.Errorf("used = %d, want the owner's spend carried across the rotation", q.UsedTokens)
	}
	for _, p := range asked {
		if p == "/key/info" {
			t.Error("fell back to the key's own counter, which a rotation resets")
		}
	}
}

// Keys issued before owners were tracked have no user upstream. They must keep
// reporting the key's own figure rather than showing nothing.
func TestQuotaFallsBackToTheKeyForUntrackedOwners(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/user/info" {
			w.WriteHeader(http.StatusNotFound)
			w.Write([]byte(`{"error":{"message":"not found"}}`))
			return
		}
		w.Write([]byte(`{"info":{"spend":0.03,"max_budget":0.1}}`))
	}))
	defer srv.Close()

	q, err := NewProvider(NewClient(srv.URL, "mk")).Quota(context.Background(), "sk-old", "sub-unknown")
	if err != nil {
		t.Fatal(err)
	}
	if q.UsedTokens != BudgetToTokens(0.03) {
		t.Errorf("used = %d, want the key's own figure as a fallback", q.UsedTokens)
	}
}

// A profile edit has to reach both halves of the allowance.
func TestUpdateLimitsAppliesBothHalves(t *testing.T) {
	g := newGatewayStub(t)

	err := NewProvider(NewClient(g.srv.URL, "mk")).UpdateLimits(context.Background(), "sk-1", "sub-1",
		keyprovider.Limits{Quotas: []keyprovider.QuotaWindow{
			{Tokens: 1_000, Period: "1h"},
			{Tokens: 2_000_000, Period: "30d"},
		}})
	if err != nil {
		t.Fatal(err)
	}
	if len(g.userBodies) != 1 || g.userBodies[0]["max_budget"] != TokensToBudget(2_000_000) {
		t.Errorf("owner budget not re-applied: %v", g.userBodies)
	}
	if len(g.keyBodies) != 1 {
		t.Fatalf("made %d key calls, want one", len(g.keyBodies))
	}
	if g.keyBodies[0]["max_budget"] != TokensToBudget(1_000) {
		t.Errorf("key kept %v, want only the 1h burst window",
			g.keyBodies[0]["max_budget"])
	}
}
