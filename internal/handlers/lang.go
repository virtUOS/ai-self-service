package handlers

import (
	"encoding/json"
	"html/template"
	"strconv"
	"strings"
	"unicode"

	"github.com/virtuos/keyosk/internal/database"
	"github.com/virtuos/keyosk/internal/i18n"
)

// langFuncs are the template helpers every page needs for translation.
//
// T is called as {{T .Lang "some.key"}} rather than being bound per-request,
// so templates are parsed once at startup instead of per render.
func langFuncs() template.FuncMap {
	return template.FuncMap{
		"T": i18n.T,
		// barPct scales a bar's tokens against the busiest bar, for the usage
		// chart. A floor of 2% keeps a quiet bar visible rather than invisible;
		// an empty bucket stays empty, since a gap is the information.
		"barPct": func(tokens, peak int64) int {
			if peak <= 0 || tokens <= 0 {
				return 0
			}
			pct := int(tokens * 100 / peak)
			if pct < 2 {
				pct = 2
			}
			return pct
		},
		// quotaJSON renders a profile's quota windows for the admin form's
		// JavaScript. A variable-length list cannot be passed as positional
		// arguments the way the other profile fields are.
		"quotaJSON": func(qs []database.ProfileQuota) template.JS {
			if len(qs) == 0 {
				return template.JS("[]")
			}
			b, err := json.Marshal(qs)
			if err != nil {
				return template.JS("[]")
			}
			return template.JS(b)
		},
		// thousands renders a token count with separators (1234567 -> 1,234,567).
		"thousands": func(n int64) string {
			s := strconv.FormatInt(n, 10)
			if len(s) <= 3 {
				return s
			}
			var b strings.Builder
			for i, c := range s {
				if i > 0 && (len(s)-i)%3 == 0 {
					b.WriteByte(',')
				}
				b.WriteRune(c)
			}
			return b.String()
		},
		// fmtBudget renders a quota amount with the deployment's unit label.
		"fmtBudget": FormatBudget,
		// fmtPct renders a window's percentage, "<1%" for a touched window
		// that rounds to zero.
		"fmtPct": FormatPct,
		// initial is the letter in the account menu's avatar.
		"initial": initial,
	}
}

// initial is the first letter of name, upper-cased, for the avatar that
// stands in for the username on a narrow screen. It skips leading spaces and
// punctuation, and falls back to "?" for a name with no letter or digit.
func initial(name string) string {
	for _, r := range name {
		if unicode.IsLetter(r) || unicode.IsDigit(r) {
			return string(unicode.ToUpper(r))
		}
	}
	return "?"
}
