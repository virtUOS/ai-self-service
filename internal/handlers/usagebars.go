package handlers

import (
	"fmt"
	"time"

	"github.com/virtuos/ai-self-service/internal/keyprovider"
)

// usageBar is one column of the usage chart.
//
// The chart spans the whole reporting window, empty buckets included: a
// single day of traffic then reads as one bar at the end of an otherwise
// empty strip, which says both how much and when. Drawing only the days
// with traffic made every lone bar full-height and identical.
type usageBar struct {
	// Label is printed under the bar. Blank on bars whose label was thinned
	// out to keep a wide window readable; Title still carries the date.
	Label string
	// Title is the bucket's date or date range, shown on hover.
	Title  string
	Tokens int64
}

// Bucket sizes, chosen from the window so the bar count stays readable: a
// year of daily bars is a smear, and a month of monthly bars is one bar.
const (
	maxDailyWindowDays  = 62
	maxWeeklyWindowDays = 730
	maxLabelledBars     = 31
)

// usageBars spreads per-day token totals over the whole window of windowDays
// ending today, bucketed by day, ISO week or calendar month depending on how
// long the window is. Days outside the window are ignored.
func usageBars(days []keyprovider.DailyUsage, windowDays int, now time.Time) []usageBar {
	if windowDays < 1 {
		windowDays = 1
	}
	today := now.UTC().Truncate(24 * time.Hour)
	start := today.AddDate(0, 0, -(windowDays - 1))

	byDay := make(map[string]int64, len(days))
	for _, d := range days {
		byDay[d.Day] += d.Tokens
	}

	var bars []usageBar
	switch {
	case windowDays <= maxDailyWindowDays:
		for d := start; !d.After(today); d = d.AddDate(0, 0, 1) {
			day := d.Format("2006-01-02")
			bars = append(bars, usageBar{Label: d.Format("02"), Title: day, Tokens: byDay[day]})
		}
	case windowDays <= maxWeeklyWindowDays:
		// Weeks start on Monday, like the gateway's weekly quota reset.
		for w := mondayOf(start); !w.After(today); w = w.AddDate(0, 0, 7) {
			end := w.AddDate(0, 0, 6)
			bars = append(bars, usageBar{
				Label:  w.Format("01-02"),
				Title:  fmt.Sprintf("%s – %s", w.Format("2006-01-02"), end.Format("2006-01-02")),
				Tokens: sumDays(byDay, w, end),
			})
		}
	default:
		for m := time.Date(start.Year(), start.Month(), 1, 0, 0, 0, 0, time.UTC); !m.After(today); m = m.AddDate(0, 1, 0) {
			end := m.AddDate(0, 1, -1)
			bars = append(bars, usageBar{
				Label:  m.Format("2006-01"),
				Title:  m.Format("2006-01"),
				Tokens: sumDays(byDay, m, end),
			})
		}
	}

	// Too many labels overlap into noise; keep every nth so the axis still
	// reads, and let the title carry the exact date for the rest.
	if n := len(bars); n > maxLabelledBars {
		every := (n + maxLabelledBars - 1) / maxLabelledBars
		for i := range bars {
			if i%every != 0 {
				bars[i].Label = ""
			}
		}
	}
	return bars
}

// mondayOf is the Monday on or before d.
func mondayOf(d time.Time) time.Time {
	back := (int(d.Weekday()) + 6) % 7
	return d.AddDate(0, 0, -back)
}

// sumDays totals the tokens of every day from first to last inclusive.
func sumDays(byDay map[string]int64, first, last time.Time) int64 {
	var total int64
	for d := first; !d.After(last); d = d.AddDate(0, 0, 1) {
		total += byDay[d.Format("2006-01-02")]
	}
	return total
}
