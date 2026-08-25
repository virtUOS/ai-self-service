package litellm

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
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
	rows := []spendRow{
		{APIKey: "h", TotalTokens: 100, StartTime: "2026-08-01T10:00:00Z"},
		{APIKey: "h", TotalTokens: 50, StartTime: "2026-08-01T18:30:00Z"},
		{APIKey: "h", TotalTokens: 7, StartTime: "2026-08-03T09:00:00Z"},
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
	if got[0].Day != "2026-08-01" || got[0].Tokens != 150 {
		t.Errorf("day 1 = %+v, want 2026-08-01/150", got[0])
	}
	if got[1].Day != "2026-08-03" || got[1].Tokens != 7 {
		t.Errorf("day 2 = %+v, want 2026-08-03/7", got[1])
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
	rows := []spendRow{
		{APIKey: "h", TotalTokens: 0, StartTime: "2026-08-01T10:00:00Z"},
		{APIKey: "h", TotalTokens: 12, StartTime: "2026-08-02T10:00:00Z"},
	}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		json.NewEncoder(w).Encode(rows)
	}))
	defer srv.Close()

	got, err := NewClient(srv.URL, "mk").Usage(context.Background(), "sk-x", 30)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 1 || got[0].Day != "2026-08-02" {
		t.Errorf("got %+v, want only the day with tokens", got)
	}
}
