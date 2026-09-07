package handlers

import (
	"strconv"
	"unicode"
)

// FormatBudget renders a spend amount with the deployment's unit label.
//
// Two decimals normally. Allowances can be a few cents a day, so an amount
// that would round to 0.00 is shown with four decimals instead of reading as
// nothing. A single non-letter character is a currency symbol and goes in
// front; anything else ("credits") goes after with a space.
func FormatBudget(amount float64, unit string) string {
	s := strconv.FormatFloat(amount, 'f', 2, 64)
	if amount > 0 && s == "0.00" {
		s = strconv.FormatFloat(amount, 'f', 4, 64)
	}
	// A single request costs a few millionths at typical prices, so even
	// four decimals show zeros. Say "less than" rather than "nothing".
	prefix := ""
	if amount > 0 && s == "0.0000" {
		prefix, s = "<", "0.0001"
	}
	if unit == "" {
		return prefix + s
	}
	if r := []rune(unit); len(r) == 1 && !unicode.IsLetter(r[0]) {
		return prefix + unit + s
	}
	return prefix + s + " " + unit
}

// FormatPct renders a window's consumption as a whole-number percentage.
//
// An hourly allowance is large next to one request, so early use rounds to
// 0%; that reads as "nothing spent" when something was. Say "<1%" instead,
// so the figure only ever reads 0% for a window that is genuinely untouched.
func FormatPct(used float64, pct int) string {
	if used > 0 && pct == 0 {
		return "<1%"
	}
	return strconv.Itoa(pct) + "%"
}
