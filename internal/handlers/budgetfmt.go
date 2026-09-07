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
	if unit == "" {
		return s
	}
	if r := []rune(unit); len(r) == 1 && !unicode.IsLetter(r[0]) {
		return unit + s
	}
	return s + " " + unit
}
