package litellm

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
	"time"

	"github.com/virtuos/ai-self-service/internal/keyprovider"
)

// The spend log is the whole log for a key — LiteLLM ignores page_size, limit
// and size on this route, and the date-bounded shape drops token counts, so
// there is no way to ask for less. Fetching it once per window meant three
// identical multi-megabyte downloads for a profile with three windows, on
// every dashboard load.
func TestWindowsFetchesTheSpendLogOnce(t *testing.T) {
	var logFetches int64

	now := time.Now().UTC()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/spend/logs":
			atomic.AddInt64(&logFetches, 1)
			json.NewEncoder(w).Encode([]spendRow{
				{APIKey: "h", TotalTokens: 100, Spend: 0.00001, StartTime: now.Add(-30 * time.Minute).Format(time.RFC3339)},
			})
		case "/key/info":
			fmt.Fprintf(w, `{"info":{"budget_limits":[
			  {"budget_duration":"1h","max_budget":0.001,"reset_at":%q},
			  {"budget_duration":"24h","max_budget":0.002,"reset_at":%q},
			  {"budget_duration":"7d","max_budget":0.005,"reset_at":%q}
			]}}`,
				now.Add(30*time.Minute).Format(time.RFC3339),
				now.Add(6*time.Hour).Format(time.RFC3339),
				now.Add(72*time.Hour).Format(time.RFC3339))
		default:
			w.Write([]byte(`{}`))
		}
	}))
	defer srv.Close()

	got, err := NewProvider(NewClient(srv.URL, "mk")).Windows(context.Background(), "sk-1", "")
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 3 {
		t.Fatalf("got %d windows, want 3", len(got))
	}
	if n := atomic.LoadInt64(&logFetches); n != 1 {
		t.Errorf("fetched the spend log %d times, want once for all three windows", n)
	}
}

// Each window still counts only the traffic inside it, so the shared fetch
// must not blur them together.
func TestWindowsCountPerWindowFromOneFetch(t *testing.T) {
	now := time.Now().UTC()
	rows := []spendRow{
		// Inside the hour, so it counts towards every window.
		{APIKey: "h", TotalTokens: 100, Spend: 0.00001, StartTime: now.Add(-10 * time.Minute).Format(time.RFC3339)},
		// Hours ago: outside the 1h window, inside the wider ones.
		{APIKey: "h", TotalTokens: 500, Spend: 0.00005, StartTime: now.Add(-5 * time.Hour).Format(time.RFC3339)},
	}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/spend/logs":
			json.NewEncoder(w).Encode(rows)
		case "/key/info":
			fmt.Fprintf(w, `{"info":{"budget_limits":[
			  {"budget_duration":"1h","max_budget":0.001,"reset_at":%q},
			  {"budget_duration":"24h","max_budget":0.002,"reset_at":%q}
			]}}`,
				now.Add(50*time.Minute).Format(time.RFC3339),
				now.Add(12*time.Hour).Format(time.RFC3339))
		default:
			w.Write([]byte(`{}`))
		}
	}))
	defer srv.Close()

	got, err := NewProvider(NewClient(srv.URL, "mk")).Windows(context.Background(), "sk-1", "")
	if err != nil {
		t.Fatal(err)
	}

	by := map[string]keyprovider.WindowUsage{}
	for _, w := range got {
		by[w.Period] = w
	}
	if !nearly(by["1h"].Used, 0.00001) {
		t.Errorf("1h window counted %v, want only the recent 0.00001", by["1h"].Used)
	}
	if !nearly(by["24h"].Used, 0.00006) {
		t.Errorf("24h window counted %v, want both rows (0.00006)", by["24h"].Used)
	}
}

// A key that has never been used has an empty spend log, which is otherwise
// indistinguishable from logging being switched off. The key's own spend
// counter settles it: the gateway maintains it whether or not per-request
// logging is on, so zero spend means the key really has consumed nothing and
// the windows can be shown at zero rather than hidden.
func TestWindowsTrustsZeroOnAnUnusedKey(t *testing.T) {
	now := time.Now().UTC()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/spend/logs":
			w.Write([]byte(`[]`))
		case "/key/info":
			// spend: 0 — the key has never been used.
			fmt.Fprintf(w, `{"info":{"spend":0,"budget_limits":[
			  {"budget_duration":"1h","max_budget":0.001,"reset_at":%q}
			]}}`, now.Add(30*time.Minute).Format(time.RFC3339))
		default:
			w.Write([]byte(`{}`))
		}
	}))
	defer srv.Close()

	got, err := NewProvider(NewClient(srv.URL, "mk")).Windows(context.Background(), "sk-new", "")
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 1 {
		t.Fatalf("got %d windows, want 1", len(got))
	}
	if !got[0].UsedKnown {
		t.Error("an unused key's zero was reported as unknown, so the bars stay hidden")
	}
	if got[0].Used != 0 {
		t.Errorf("used = %v, want 0", got[0].Used)
	}
}

// A key that HAS spent but whose log is empty means logging is switched off.
// The figure is genuinely unknown then, and a bar drawn from the silent zero
// would promise an allowance the user may not have.
func TestWindowsDistrustsZeroWhenTheKeyHasSpent(t *testing.T) {
	now := time.Now().UTC()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/spend/logs":
			w.Write([]byte(`[]`))
		case "/key/info":
			// Real spend, but no log rows: logging is off.
			fmt.Fprintf(w, `{"info":{"spend":0.05,"budget_limits":[
			  {"budget_duration":"1h","max_budget":0.001,"reset_at":%q}
			]}}`, now.Add(30*time.Minute).Format(time.RFC3339))
		default:
			w.Write([]byte(`{}`))
		}
	}))
	defer srv.Close()

	got, err := NewProvider(NewClient(srv.URL, "mk")).Windows(context.Background(), "sk-used", "")
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 1 {
		t.Fatalf("got %d windows, want 1", len(got))
	}
	if got[0].UsedKnown {
		t.Error("a spent key with no log rows was reported as known-zero, which would draw a full bar")
	}
}

// The chart and the per-model table are two views of the same log, and the log
// cannot be narrowed server-side, so asking for them separately meant two
// identical multi-megabyte downloads on every dashboard load. History reads it
// once and aggregates both shapes from the rows in hand.
func TestHistoryFetchesTheSpendLogOnce(t *testing.T) {
	var logFetches int64

	now := time.Now().UTC()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/spend/logs":
			atomic.AddInt64(&logFetches, 1)
			json.NewEncoder(w).Encode([]spendRow{
				{APIKey: "h", Model: "qwen", PromptTokens: 60, CompletionTokens: 40, TotalTokens: 100, StartTime: now.Add(-30 * time.Minute).Format(time.RFC3339)},
			})
		default:
			t.Errorf("unexpected path %s", r.URL.Path)
			w.Write([]byte(`{}`))
		}
	}))
	defer srv.Close()

	got, err := NewClient(srv.URL, "mk").History(context.Background(), "sk-1", 30)
	if err != nil {
		t.Fatal(err)
	}
	if len(got.Days) != 1 || got.Days[0].Tokens != 100 {
		t.Errorf("days = %+v, want one day of 100 tokens", got.Days)
	}
	if len(got.Models) != 1 || got.Models[0].Model != "qwen" {
		t.Errorf("models = %+v, want one qwen row", got.Models)
	}
	if n := atomic.LoadInt64(&logFetches); n != 1 {
		t.Errorf("fetched the spend log %d times, want once for both views", n)
	}
}
