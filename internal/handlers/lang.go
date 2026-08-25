package handlers

import (
	"html/template"
	"net/http"
	"strconv"
	"strings"

	"github.com/virtuos/ai-self-service/internal/i18n"
)

// langFuncs are the template helpers every page needs for translation.
//
// T is called as {{T .Lang "some.key"}} rather than being bound per-request,
// so templates are parsed once at startup instead of per render.
func langFuncs() template.FuncMap {
	return template.FuncMap{
		"T": i18n.T,
		// barPct scales a day's tokens against the busiest day, for the usage
		// chart. A floor of 2% keeps a quiet day visible rather than invisible.
		"barPct": func(tokens, peak int64) int {
			if peak <= 0 {
				return 0
			}
			pct := int(tokens * 100 / peak)
			if pct < 2 {
				pct = 2
			}
			return pct
		},
		// add sums two token counts, for showing the allowance as used+remaining
		// rather than passing the same figure through the template twice.
		"add": func(a, b int64) int64 { return a + b },
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
	}
}

// SetLanguage handles POST /lang, recording an explicit choice and returning
// the user to where they came from.
//
// A form post rather than a link so it is covered by CSRF like every other
// state change, and so the choice cannot be set by a crafted URL.
func SetLanguage(secure bool) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		lang := i18n.Lang(r.FormValue("lang"))
		if !i18n.Valid(lang) {
			lang = i18n.Default
		}
		http.SetCookie(w, &http.Cookie{
			Name:     i18n.CookieName,
			Value:    string(lang),
			Path:     "/",
			MaxAge:   365 * 24 * 60 * 60,
			HttpOnly: false, // read by nothing, but harmless and survives JS-less use
			Secure:   secure,
			SameSite: http.SameSiteLaxMode,
		})

		// Only same-origin paths, so this cannot become an open redirect.
		dest := r.FormValue("return_to")
		if dest == "" || dest[0] != '/' || (len(dest) > 1 && dest[1] == '/') {
			dest = "/"
		}
		http.Redirect(w, r, dest, http.StatusFound)
	}
}
