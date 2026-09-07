package handlers

import (
	"testing"
	"time"

	"github.com/virtuos/ai-self-service/internal/keyprovider"
)

var barsNow = time.Date(2026, 9, 7, 15, 0, 0, 0, time.UTC) // a Monday

// A day of traffic in a 30-day window is one bar at the end of an otherwise
// empty strip, not a lone full-width block.
func TestUsageBarsSpanTheWholeDailyWindow(t *testing.T) {
	got := usageBars([]keyprovider.DailyUsage{
		{Day: "2026-09-07", Tokens: 559},
		{Day: "2026-09-01", Tokens: 40},
		{Day: "2026-07-01", Tokens: 9_999}, // outside the window
	}, 30, barsNow)

	if len(got) != 30 {
		t.Fatalf("got %d bars, want 30", len(got))
	}
	if got[0].Title != "2026-08-09" || got[29].Title != "2026-09-07" {
		t.Errorf("window runs %s..%s, want 2026-08-09..2026-09-07", got[0].Title, got[29].Title)
	}
	if got[29].Tokens != 559 || got[23].Tokens != 40 {
		t.Errorf("tokens not placed on their days: last=%d, 09-01=%d", got[29].Tokens, got[23].Tokens)
	}
	var total int64
	for _, b := range got {
		total += b.Tokens
	}
	if total != 599 {
		t.Errorf("window total = %d, want 599 (the July day is outside)", total)
	}
	if got[29].Label != "07" || got[0].Label != "09" {
		t.Errorf("daily labels are the day of month: got %q and %q", got[0].Label, got[29].Label)
	}
}

// A window too long for daily bars is bucketed by ISO week, Monday first,
// and labels are thinned so the axis stays legible.
func TestUsageBarsBucketLongWindowsByWeek(t *testing.T) {
	got := usageBars([]keyprovider.DailyUsage{
		{Day: "2026-09-07", Tokens: 10}, // Monday: first day of the last week
		{Day: "2026-09-06", Tokens: 5},  // Sunday: last day of the week before
		{Day: "2026-09-02", Tokens: 1},
	}, 365, barsNow)

	// 365 days back from this Monday is exactly 52 weeks, so the window
	// starts on a Monday too: 53 weekly bars, the last one this week.
	if got[0].Title != "2025-09-08 – 2025-09-14" {
		t.Errorf("first week = %q", got[0].Title)
	}
	last := got[len(got)-1]
	if last.Title != "2026-09-07 – 2026-09-13" || last.Tokens != 10 {
		t.Errorf("last week = %+v, want this week's 10 tokens", last)
	}
	prev := got[len(got)-2]
	if prev.Tokens != 6 {
		t.Errorf("previous week = %d tokens, want 6 (Wed 1 + Sun 5)", prev.Tokens)
	}
	if len(got) != 53 {
		t.Errorf("got %d weeks, want 53", len(got))
	}
	labelled := 0
	for _, b := range got {
		if b.Label != "" {
			labelled++
		}
	}
	if labelled == 0 || labelled > maxLabelledBars {
		t.Errorf("%d labels for %d bars; want some, and no more than %d", labelled, len(got), maxLabelledBars)
	}
}

// Beyond two years the buckets are calendar months.
func TestUsageBarsBucketVeryLongWindowsByMonth(t *testing.T) {
	got := usageBars([]keyprovider.DailyUsage{
		{Day: "2026-09-07", Tokens: 3},
		{Day: "2026-08-31", Tokens: 4},
	}, 1000, barsNow)

	last := got[len(got)-1]
	if last.Title != "2026-09" || last.Tokens != 3 {
		t.Errorf("last month = %+v", last)
	}
	if got[len(got)-2].Tokens != 4 {
		t.Errorf("August = %d tokens, want 4", got[len(got)-2].Tokens)
	}
	if got[0].Title != "2023-12" {
		t.Errorf("first month = %q, want 2023-12 (1000 days back is Dec 2023)", got[0].Title)
	}
}

// A nonsense window still produces today's bar rather than nothing.
func TestUsageBarsMinimumWindowIsToday(t *testing.T) {
	got := usageBars(nil, 0, barsNow)
	if len(got) != 1 || got[0].Title != "2026-09-07" {
		t.Errorf("got %+v, want just today", got)
	}
}
