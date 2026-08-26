package handlers

import (
	"net/url"
	"testing"
)

// The admin form posts repeating quota rows. Blank rows are how an admin
// removes a window, so they must be dropped rather than stored as a
// zero-token quota — which upstream would read as "no allowance at all".
func TestParseQuotaWindows(t *testing.T) {
	form := url.Values{
		"quota_tokens": {"100000", "1000000"},
		"quota_period": {"24h", "30d"},
	}
	got, err := parseQuotaWindows(form)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 2 {
		t.Fatalf("got %d windows, want 2", len(got))
	}
	if got[0].Tokens != 100_000 || got[0].Period != "24h" {
		t.Errorf("first = %+v", got[0])
	}
	if got[1].Tokens != 1_000_000 || got[1].Period != "30d" {
		t.Errorf("second = %+v", got[1])
	}
}

func TestParseQuotaWindowsDropsBlankRows(t *testing.T) {
	form := url.Values{
		"quota_tokens": {"100000", "", "0"},
		"quota_period": {"24h", "30d", "1h"},
	}
	got, err := parseQuotaWindows(form)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 1 {
		t.Fatalf("got %d windows, want only the filled one: %+v", len(got), got)
	}
	if got[0].Period != "24h" {
		t.Errorf("kept the wrong row: %+v", got[0])
	}
}

// An unknown period must be refused, not sent upstream where it would be
// rejected or silently ignored.
func TestParseQuotaWindowsRejectsBadPeriod(t *testing.T) {
	form := url.Values{
		"quota_tokens": {"1000"},
		"quota_period": {"fortnightly"},
	}
	if _, err := parseQuotaWindows(form); err == nil {
		t.Error("expected an error for an unknown period")
	}
}

// Two rows on the same period would be contradictory, and the database's
// unique constraint would reject them anyway.
func TestParseQuotaWindowsRejectsDuplicatePeriod(t *testing.T) {
	form := url.Values{
		"quota_tokens": {"1000", "2000"},
		"quota_period": {"24h", "24h"},
	}
	if _, err := parseQuotaWindows(form); err == nil {
		t.Error("expected an error for a duplicated period")
	}
}

// No rows at all means unlimited, which is not an error.
func TestParseQuotaWindowsEmptyIsUnlimited(t *testing.T) {
	got, err := parseQuotaWindows(url.Values{})
	if err != nil || len(got) != 0 {
		t.Errorf("got %+v, %v; want no windows and no error", got, err)
	}
}
