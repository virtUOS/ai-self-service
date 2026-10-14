package litellm

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"testing"
	"time"
)

// activityDay is one day of /user/daily/activity as LiteLLM 1.105 reports it,
// trimmed to what matters here. The breakdown also lists deployments under
// "models", which the dashboard must not show.
func activityDay(date string, total int64, groups string) string {
	return fmt.Sprintf(`{"date":%q,
	  "metrics":{"spend":0.6,"prompt_tokens":0,"completion_tokens":0,"total_tokens":%d,
	    "successful_requests":0,"failed_requests":0,"api_requests":0},
	  "breakdown":{
	    "models":{"openai/mock-small":{"metrics":{"total_tokens":999,"api_requests":99}}},
	    "model_groups":{%s}}}`, date, total, groups)
}

func activityGroup(name string, requests, prompt, completion int64) string {
	return fmt.Sprintf(`%q:{"metrics":{"spend":0.1,"prompt_tokens":%d,"completion_tokens":%d,
	  "total_tokens":%d,"successful_requests":%d,"failed_requests":0,"api_requests":%d}}`,
		name, prompt, completion, prompt+completion, requests, requests)
}

func activityPage(hasMore bool, days ...string) string {
	return fmt.Sprintf(`{"results":[%s],"metadata":{"page":1,"has_more":%t}}`,
		strings.Join(days, ","), hasMore)
}

// The history is the person's, across every key, from the daily totals: per
// day for the chart, per public model name for the table, each model's
// requests counted as LiteLLM's api_requests.
func TestHistoryReadsDailyActivity(t *testing.T) {
	var query map[string][]string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/user/daily/activity" {
			t.Errorf("unexpected request to %s", r.URL.Path)
			http.NotFound(w, r)
			return
		}
		query = r.URL.Query()
		w.Write([]byte(activityPage(false,
			activityDay("2026-10-13", 30, activityGroup("mock-large", 1, 10, 20)),
			activityDay("2026-10-14", 120, activityGroup("mock-small", 3, 30, 60)+","+
				activityGroup("mock-large", 1, 10, 20)),
		)))
	}))
	defer srv.Close()

	h, err := NewClient(srv.URL, "mk").History(context.Background(), "owner-1", 30)
	if err != nil {
		t.Fatal(err)
	}

	if got := query["user_id"]; len(got) != 1 || got[0] != "owner-1" {
		t.Errorf("user_id = %v, want the owner", got)
	}
	today := time.Now().UTC()
	if got := query["end_date"]; len(got) != 1 || got[0] != today.Format("2006-01-02") {
		t.Errorf("end_date = %v, want today", got)
	}
	if got := query["start_date"]; len(got) != 1 || got[0] != today.AddDate(0, 0, -30).Format("2006-01-02") {
		t.Errorf("start_date = %v, want 30 days back", got)
	}
	if _, ok := query["api_key"]; ok {
		t.Error("history is filtered by key, so a rotation would start it over")
	}

	if len(h.Days) != 2 || h.Days[0].Day != "2026-10-13" || h.Days[0].Tokens != 30 ||
		h.Days[1].Day != "2026-10-14" || h.Days[1].Tokens != 120 {
		t.Errorf("days = %+v, want 30 on the 13th and 120 on the 14th", h.Days)
	}

	if len(h.Models) != 2 {
		t.Fatalf("models = %+v, want mock-small and mock-large", h.Models)
	}
	// Largest total first; mock-large is summed over both days.
	small, large := h.Models[0], h.Models[1]
	if small.Model != "mock-small" || small.Requests != 3 || small.PromptTokens != 30 ||
		small.CompletionTokens != 60 || small.TotalTokens != 90 {
		t.Errorf("first model = %+v, want mock-small: 3 requests, 30+60 tokens", small)
	}
	if large.Model != "mock-large" || large.Requests != 2 || large.TotalTokens != 60 {
		t.Errorf("second model = %+v, want mock-large: 2 requests over two days, 60 tokens", large)
	}
}

// A page ends after a number of rows, not at a day's end, so one day can come
// in two parts. Both parts count, and every page is read.
func TestHistorySumsADaySplitOverPages(t *testing.T) {
	var pages []string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		page := r.URL.Query().Get("page")
		pages = append(pages, page)
		switch page {
		case "1":
			w.Write([]byte(activityPage(true,
				activityDay("2026-10-14", 30, activityGroup("mock-small", 1, 10, 20)))))
		case "2":
			w.Write([]byte(activityPage(false,
				activityDay("2026-10-14", 60, activityGroup("mock-small", 2, 20, 40)))))
		default:
			t.Errorf("asked for page %q", page)
			w.Write([]byte(activityPage(false)))
		}
	}))
	defer srv.Close()

	h, err := NewClient(srv.URL, "mk").History(context.Background(), "owner-1", 30)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Join(pages, ",") != "1,2" {
		t.Errorf("read pages %v, want 1 then 2", pages)
	}
	if len(h.Days) != 1 || h.Days[0].Tokens != 90 {
		t.Errorf("days = %+v, want one day of 90 tokens", h.Days)
	}
	if len(h.Models) != 1 || h.Models[0].Requests != 3 || h.Models[0].TotalTokens != 90 {
		t.Errorf("models = %+v, want mock-small: 3 requests, 90 tokens", h.Models)
	}
}

// A gateway that never stops answering has_more must not hold the dashboard
// forever.
func TestHistoryStopsAtAPageLimit(t *testing.T) {
	var calls int
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls++
		w.Write([]byte(activityPage(true)))
	}))
	defer srv.Close()

	if _, err := NewClient(srv.URL, "mk").History(context.Background(), "owner-1", 30); err == nil {
		t.Error("an endless has_more was accepted")
	}
	if calls != activityMaxPages {
		t.Errorf("read %d pages, want the limit of %d", calls, activityMaxPages)
	}
}

// Without a user_id the route reports every user's usage, so a missing owner
// must not reach the gateway.
func TestHistoryWithoutOwnerAsksNothing(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		t.Errorf("unexpected request to %s?%s", r.URL.Path, r.URL.RawQuery)
	}))
	defer srv.Close()

	h, err := NewClient(srv.URL, "mk").History(context.Background(), "", 30)
	if err != nil {
		t.Fatal(err)
	}
	if len(h.Days) != 0 || len(h.Models) != 0 {
		t.Errorf("history = %+v, want empty", h)
	}
}

// No usage is an empty history, not an error; a failed read is an error, not
// an empty history.
func TestHistoryEmptyAndFailed(t *testing.T) {
	status := http.StatusOK
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(status)
		w.Write([]byte(activityPage(false)))
	}))
	defer srv.Close()
	c := NewClient(srv.URL, "mk")

	h, err := c.History(context.Background(), "owner-1", 30)
	if err != nil {
		t.Fatal(err)
	}
	if len(h.Days) != 0 || len(h.Models) != 0 {
		t.Errorf("history = %+v, want empty", h)
	}

	status = http.StatusInternalServerError
	if _, err := c.History(context.Background(), "owner-1", 30); err == nil ||
		!strings.Contains(err.Error(), strconv.Itoa(status)) {
		t.Errorf("err = %v, want the gateway's %d", err, status)
	}
}
