package handlers

import "testing"

// Amounts are small (a day's allowance can be a few cents), so two decimals
// must not collapse a real figure to 0.00. A one-character symbol goes in
// front like a currency; a word goes after, so "credits" reads naturally.
func TestFormatBudget(t *testing.T) {
	cases := []struct {
		amount float64
		unit   string
		want   string
	}{
		{0.1, "$", "$0.10"},
		{1.5, "€", "€1.50"},
		{0.004, "$", "$0.0040"},
		{0, "$", "$0.00"},
		{2, "credits", "2.00 credits"},
		{0.25, "", "0.25"},
	}
	for _, c := range cases {
		if got := FormatBudget(c.amount, c.unit); got != c.want {
			t.Errorf("FormatBudget(%v, %q) = %q, want %q", c.amount, c.unit, got, c.want)
		}
	}
}
