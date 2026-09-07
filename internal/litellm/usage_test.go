package litellm

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/virtuos/ai-self-service/internal/keyprovider"
)

// LiteLLM identifies a key in its spend log by the SHA-256 of the key itself.
// The portal stores the key, so it derives the hash rather than storing it.
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

// Raw spend rows must aggregate into per-day totals. LiteLLM's own daily
// aggregation reports spend only and drops token counts, so it cannot be used.
func TestUsageAggregatesRowsPerDay(t *testing.T) {
	// Dates are relative to today, not fixed: Usage drops anything older than
	// the window it is asked for, so hardcoded days eventually fall outside it
	// and the test starts failing on a date that has nothing to do with the
	// code. Two days back and four days back sit inside any window worth
	// testing.
	dayOne := time.Now().UTC().AddDate(0, 0, -4).Format("2006-01-02")
	dayTwo := time.Now().UTC().AddDate(0, 0, -2).Format("2006-01-02")
	rows := []spendRow{
		{APIKey: "h", TotalTokens: 100, StartTime: dayOne + "T10:00:00Z"},
		{APIKey: "h", TotalTokens: 50, StartTime: dayOne + "T18:30:00Z"},
		{APIKey: "h", TotalTokens: 7, StartTime: dayTwo + "T09:00:00Z"},
	}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if got := r.URL.Query().Get("api_key"); got == "" {
			t.Errorf("request did not filter by api_key")
		}
		json.NewEncoder(w).Encode(rows)
	}))
	defer srv.Close()

	got, err := NewClient(srv.URL, "mk").Usage(context.Background(), "sk-whatever", 30)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 2 {
		t.Fatalf("got %d days, want 2 (two rows share a day)", len(got))
	}
	if got[0].Day != dayOne || got[0].Tokens != 150 {
		t.Errorf("day 1 = %+v, want %s/150", got[0], dayOne)
	}
	if got[1].Day != dayTwo || got[1].Tokens != 7 {
		t.Errorf("day 2 = %+v, want %s/7", got[1], dayTwo)
	}
}

// A key with no traffic is normal, not an error.
func TestUsageEmptyIsNotAnError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte("[]"))
	}))
	defer srv.Close()

	got, err := NewClient(srv.URL, "mk").Usage(context.Background(), "sk-x", 30)
	if err != nil {
		t.Fatalf("empty log returned error: %v", err)
	}
	if len(got) != 0 {
		t.Errorf("got %d days, want none", len(got))
	}
}

// Rows without usable token counts must not become phantom zero-token days.
func TestUsageSkipsRowsWithoutTokens(t *testing.T) {
	// Relative to today, not fixed: Usage drops anything older than the window
	// it is asked for, so hardcoded dates eventually fall outside it and the
	// test fails on a date that has nothing to do with the code.
	now := time.Now().UTC()
	empty := now.AddDate(0, 0, -2)
	used := now.AddDate(0, 0, -1)
	rows := []spendRow{
		{APIKey: "h", TotalTokens: 0, StartTime: empty.Format(time.RFC3339)},
		{APIKey: "h", TotalTokens: 12, StartTime: used.Format(time.RFC3339)},
	}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		json.NewEncoder(w).Encode(rows)
	}))
	defer srv.Close()

	got, err := NewClient(srv.URL, "mk").Usage(context.Background(), "sk-x", 30)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 1 || got[0].Day != used.Format("2006-01-02") {
		t.Errorf("got %+v, want only the day with tokens", got)
	}
}

// Spend logs can be switched off upstream — they were here, to bound a
// LiteLLM memory leak. The key itself still records cumulative spend, so fall
// back to that rather than reporting no usage at all.
func TestUsageFallsBackToKeySpend(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case strings.HasPrefix(r.URL.Path, "/spend/logs"):
			w.Write([]byte("[]")) // logging disabled: no rows, no error
		case strings.HasPrefix(r.URL.Path, "/key/info"):
			json.NewEncoder(w).Encode(map[string]any{
				"info": map[string]any{"spend": 5.45e-05},
			})
		default:
			t.Errorf("unexpected path %s", r.URL.Path)
		}
	}))
	defer srv.Close()

	total, err := NewClient(srv.URL, "mk").KeySpend(context.Background(), "sk-x")
	if err != nil {
		t.Fatal(err)
	}
	// The gateway's own figure, in its own unit: nothing converts it.
	if total != 5.45e-05 {
		t.Errorf("KeySpend = %v, want 5.45e-05", total)
	}
}

// A key that has never been used reports zero, not an error.
func TestKeySpendZeroForUnusedKey(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		json.NewEncoder(w).Encode(map[string]any{"info": map[string]any{"spend": 0.0}})
	}))
	defer srv.Close()

	total, err := NewClient(srv.URL, "mk").KeySpend(context.Background(), "sk-x")
	if err != nil || total != 0 {
		t.Errorf("got %v, %v; want 0, nil", total, err)
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

	err := NewClient(srv.URL, "mk").UpdateKeyLimits(context.Background(), "sk-x", keyprovider.Limits{
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
		context.Background(), "sk-x", keyprovider.Limits{}); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(raw, `"max_budget":null`) {
		t.Errorf("payload %s does not clear max_budget", raw)
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
		context.Background(), "sk-x", keyprovider.Limits{}); err != nil {
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
		context.Background(), "sk-x", keyprovider.Limits{Models: []string{"gpt-4o"}}); err != nil {
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

	err := NewClient(srv.URL, "mk").UpdateKeyLimits(context.Background(), "sk-x", keyprovider.Limits{
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

	if err := NewClient(srv.URL, "mk").UpdateKeyLimits(context.Background(), "sk-x", keyprovider.Limits{
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
		context.Background(), "sk-x", keyprovider.Limits{}); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(raw, `"max_budget":null`) {
		t.Errorf("payload does not clear max_budget: %s", raw)
	}
	if !strings.Contains(raw, `"budget_limits":null`) {
		t.Errorf("payload does not clear budget_limits: %s", raw)
	}
}
