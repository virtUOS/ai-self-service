package litellm

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
	"time"
)

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
