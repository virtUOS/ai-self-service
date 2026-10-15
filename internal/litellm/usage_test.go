package litellm

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/virtuos/keyosk/internal/keyprovider"
)

// LiteLLM identifies a key by the SHA-256 of the key itself. The portal stores
// that hash as the key's ref, and derives it when the gateway does not say.
func TestKeyHashIsSHA256(t *testing.T) {
	// Known-good vector: sha256("sk-test"), computed independently.
	const key = "sk-test"
	const want = "f3abf2a6cc4f00987743db5f544ba345b4899ae31f326d8ee9c4816de153c9e0"
	got := keyHash(key)
	if got != want {
		t.Errorf("keyHash(%q) = %q, want %q", key, got, want)
	}
	// The live gateway's spend log uses 64-char lowercase hex.
	if len(got) != 64 {
		t.Fatalf("hash %q is %d chars, want 64", got, len(got))
	}
	if keyHash(key+"x") == got {
		t.Error("hash does not depend on the whole key")
	}
}

// A key with no budget has nothing remaining to report; the caller must be
// able to tell that apart from a fully-consumed quota.
func TestKeyQuotaUnlimited(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		json.NewEncoder(w).Encode(map[string]any{"info": map[string]any{
			"spend": 1e-05, "max_budget": nil,
		}})
	}))
	defer srv.Close()

	got, err := NewClient(srv.URL, "mk").KeyQuota(context.Background(), "sk-x")
	if err != nil {
		t.Fatal(err)
	}
	if got.Limit != 0 {
		t.Errorf("Limit = %v, want 0 for an unlimited key", got.Limit)
	}
	if got.Used != 1e-05 {
		t.Errorf("Used = %v, want 1e-05", got.Used)
	}
}

// A profile's limits must be pushable onto a key that already exists.
// Without this a profile change never reaches issued keys, and the portal
// advertises a quota the gateway does not enforce.
func TestUpdateKeyLimitsSendsBudget(t *testing.T) {
	var got map[string]any
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/key/update" {
			t.Errorf("posted to %s, want /key/update", r.URL.Path)
		}
		json.NewDecoder(r.Body).Decode(&got)
		w.Write([]byte("{}"))
	}))
	defer srv.Close()

	err := NewClient(srv.URL, "mk").UpdateKeyLimits(context.Background(), "sk-x", "", keyprovider.Limits{
		Quotas: []keyprovider.QuotaWindow{{Budget: 0.001, Period: "1h"}},
		Models: []string{"gpt-4o"},
	})
	if err != nil {
		t.Fatal(err)
	}
	if got["key"] != "sk-x" {
		t.Errorf("key = %v", got["key"])
	}
	if got["max_budget"] != 0.001 {
		t.Errorf("max_budget = %v, want 0.001", got["max_budget"])
	}
	if got["budget_duration"] != "1h" {
		t.Errorf("budget_duration = %v", got["budget_duration"])
	}
}

// Clearing a quota must send an explicit null, not omit the field: omitting it
// leaves the old budget in place, so a profile losing its quota would keep
// enforcing the previous one.
func TestUpdateKeyLimitsClearsBudget(t *testing.T) {
	var raw string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		b, _ := io.ReadAll(r.Body)
		raw = string(b)
		w.Write([]byte("{}"))
	}))
	defer srv.Close()

	if err := NewClient(srv.URL, "mk").UpdateKeyLimits(
		context.Background(), "sk-x", "", keyprovider.Limits{}); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(raw, `"max_budget":null`) {
		t.Errorf("payload %s does not clear max_budget", raw)
	}
}

// A push binds the key to its owner, so a key follows the owner to a new
// internal user when the portal's user IDs change. Without an owner the
// binding is left as it is.
func TestUpdateKeyLimitsBindsTheOwner(t *testing.T) {
	for _, tc := range []struct{ owner, want string }{
		{"testing:sub-a", `"user_id":"testing:sub-a"`},
		{"", ""},
	} {
		var raw string
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			b, _ := io.ReadAll(r.Body)
			raw = string(b)
			w.Write([]byte("{}"))
		}))

		if err := NewClient(srv.URL, "mk").UpdateKeyLimits(
			context.Background(), "sk-x", tc.owner, keyprovider.Limits{}); err != nil {
			t.Fatal(err)
		}
		srv.Close()
		if tc.want != "" && !strings.Contains(raw, tc.want) {
			t.Errorf("owner %q: payload %s does not bind the key", tc.owner, raw)
		}
		if tc.want == "" && strings.Contains(raw, `"user_id"`) {
			t.Errorf("no owner: payload %s sends a user_id", raw)
		}
	}
}

// LiteLLM's /key/update rejects an explicit null for models with a 400
// ("A value is required but not set"), unlike /key/generate where the field is
// simply omitted. An unrestricted profile must therefore omit it rather than
// send null, or every sync fails and no limits are ever applied.
func TestUpdateKeyLimitsOmitsNullModels(t *testing.T) {
	var raw string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		b, _ := io.ReadAll(r.Body)
		raw = string(b)
		w.Write([]byte("{}"))
	}))
	defer srv.Close()

	if err := NewClient(srv.URL, "mk").UpdateKeyLimits(
		context.Background(), "sk-x", "", keyprovider.Limits{}); err != nil {
		t.Fatal(err)
	}
	if strings.Contains(raw, `"models":null`) {
		t.Errorf("payload sends null models, which LiteLLM rejects: %s", raw)
	}
	// Empty must still be sent, or dropping a restriction never clears it.
	if !strings.Contains(raw, `"models":[]`) {
		t.Errorf("payload does not clear the model list: %s", raw)
	}
}

// A restricting profile must still send its list.
func TestUpdateKeyLimitsSendsModelList(t *testing.T) {
	var got map[string]any
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		json.NewDecoder(r.Body).Decode(&got)
		w.Write([]byte("{}"))
	}))
	defer srv.Close()

	if err := NewClient(srv.URL, "mk").UpdateKeyLimits(
		context.Background(), "sk-x", "", keyprovider.Limits{Models: []string{"gpt-4o"}}); err != nil {
		t.Fatal(err)
	}
	m, ok := got["models"].([]any)
	if !ok || len(m) != 1 || m[0] != "gpt-4o" {
		t.Errorf("models = %v, want [gpt-4o]", got["models"])
	}
}

// Several windows go upstream as budget_limits, which LiteLLM enforces
// independently. Confirmed against the live gateway on v1.97.0: capping 1h
// tight and 24h loose blocks on the hour, and the reverse blocks on the day.
func TestUpdateKeyLimitsSendsStackedWindows(t *testing.T) {
	var got map[string]any
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		json.NewDecoder(r.Body).Decode(&got)
		w.Write([]byte("{}"))
	}))
	defer srv.Close()

	err := NewClient(srv.URL, "mk").UpdateKeyLimits(context.Background(), "sk-x", "", keyprovider.Limits{
		Quotas: []keyprovider.QuotaWindow{
			{Budget: 0.01, Period: "24h"},
			{Budget: 0.1, Period: "30d"},
		},
	})
	if err != nil {
		t.Fatal(err)
	}

	limits, ok := got["budget_limits"].([]any)
	if !ok || len(limits) != 2 {
		t.Fatalf("budget_limits = %v, want two windows", got["budget_limits"])
	}
	first := limits[0].(map[string]any)
	if first["budget_duration"] != "24h" {
		t.Errorf("first window period = %v", first["budget_duration"])
	}
	if first["max_budget"] != 0.01 {
		t.Errorf("first window budget = %v, want 0.01", first["max_budget"])
	}
	// The single-window fields must not also be set, or the two disagree.
	if got["max_budget"] != nil {
		t.Errorf("max_budget should be nil when stacked windows are used, got %v", got["max_budget"])
	}
}

// One window keeps using the plain pair: it already works, and there is no
// reason to change the shape for the common case.
func TestUpdateKeyLimitsSingleWindowStaysFlat(t *testing.T) {
	var got map[string]any
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		json.NewDecoder(r.Body).Decode(&got)
		w.Write([]byte("{}"))
	}))
	defer srv.Close()

	if err := NewClient(srv.URL, "mk").UpdateKeyLimits(context.Background(), "sk-x", "", keyprovider.Limits{
		Quotas: []keyprovider.QuotaWindow{{Budget: 0.001, Period: "1h"}},
	}); err != nil {
		t.Fatal(err)
	}
	if got["max_budget"] != 0.001 {
		t.Errorf("max_budget = %v, want 0.001", got["max_budget"])
	}
	if got["budget_duration"] != "1h" {
		t.Errorf("budget_duration = %v", got["budget_duration"])
	}
}

// Dropping from several windows to none must clear both shapes, or the key
// keeps enforcing whichever one was left behind.
func TestUpdateKeyLimitsClearsStackedWindows(t *testing.T) {
	var raw string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		b, _ := io.ReadAll(r.Body)
		raw = string(b)
		w.Write([]byte("{}"))
	}))
	defer srv.Close()

	if err := NewClient(srv.URL, "mk").UpdateKeyLimits(
		context.Background(), "sk-x", "", keyprovider.Limits{}); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(raw, `"max_budget":null`) {
		t.Errorf("payload does not clear max_budget: %s", raw)
	}
	if !strings.Contains(raw, `"budget_limits":null`) {
		t.Errorf("payload does not clear budget_limits: %s", raw)
	}
}
