package handlers

import (
	"net/url"
	"testing"
)

// The admin form posts repeating quota rows. Blank rows are how an admin
// removes a window, so they must be dropped rather than stored as a
// zero-budget quota — which upstream would read as "no allowance at all".
func TestParseQuotaWindows(t *testing.T) {
	form := url.Values{
		"quota_budget": {"0.10", "5"},
		"quota_period": {"24h", "30d"},
	}
	got, err := parseQuotaWindows(form)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 2 {
		t.Fatalf("got %d windows, want 2", len(got))
	}
	if got[0].Budget != 0.1 || got[0].Period != "24h" {
		t.Errorf("first = %+v", got[0])
	}
	if got[1].Budget != 5 || got[1].Period != "30d" {
		t.Errorf("second = %+v", got[1])
	}
}

func TestParseQuotaWindowsDropsBlankRows(t *testing.T) {
	form := url.Values{
		"quota_budget": {"0.10", "", "0"},
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
		"quota_budget": {"0.10"},
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
		"quota_budget": {"0.10", "0.20"},
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

// A shorter window with a larger allowance than a longer one can never bind:
// spending the hourly cap would already exceed the weekly. Refuse it rather
// than store a limit that does nothing.
func TestParseQuotaWindowsRejectsUnreachableWindow(t *testing.T) {
	form := url.Values{
		"quota_budget": {"5", "1"},
		"quota_period": {"1h", "30d"},
	}
	if _, err := parseQuotaWindows(form); err == nil {
		t.Error("expected an error: an hourly cap above the monthly one can never bind")
	}
}

// Equal allowances on different periods are pointless but not contradictory —
// the shorter one simply binds first. Allowed.
func TestParseQuotaWindowsAllowsEqualAllowances(t *testing.T) {
	form := url.Values{
		"quota_budget": {"1", "1"},
		"quota_period": {"1h", "24h"},
	}
	if _, err := parseQuotaWindows(form); err != nil {
		t.Errorf("equal allowances rejected: %v", err)
	}
}

// The ordinary case must still pass: tighter caps on shorter periods.
func TestParseQuotaWindowsAllowsSensibleLadder(t *testing.T) {
	form := url.Values{
		"quota_budget": {"0.10", "1", "5"},
		"quota_period": {"1h", "7d", "30d"},
	}
	got, err := parseQuotaWindows(form)
	if err != nil {
		t.Fatalf("sensible ladder rejected: %v", err)
	}
	if len(got) != 3 {
		t.Errorf("got %d windows, want 3", len(got))
	}
}

// The amount is money-shaped, so it must accept decimals and reject text.
func TestParseQuotaWindowsReadsDecimals(t *testing.T) {
	got, err := parseQuotaWindows(url.Values{
		"quota_budget": {"0.25"}, "quota_period": {"24h"},
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 1 || got[0].Budget != 0.25 {
		t.Errorf("got %+v, want one 0.25 window", got)
	}
}

func TestParseQuotaWindowsRejectsNonNumericAmount(t *testing.T) {
	if _, err := parseQuotaWindows(url.Values{
		"quota_budget": {"ten"}, "quota_period": {"24h"},
	}); err == nil {
		t.Error("a non-numeric amount was accepted")
	}
}

func TestParseQuotaWindowsRejectsInfiniteAmount(t *testing.T) {
	if _, err := parseQuotaWindows(url.Values{
		"quota_budget": {"Inf"}, "quota_period": {"24h"},
	}); err == nil {
		t.Error("an infinite amount was accepted")
	}
}
