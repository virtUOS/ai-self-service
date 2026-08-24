package handlers

import (
	"html/template"
	"net/http"

	"github.com/virtuos/ai-self-service/internal/i18n"
)

// langFuncs are the template helpers every page needs for translation.
//
// T is called as {{T .Lang "some.key"}} rather than being bound per-request,
// so templates are parsed once at startup instead of per render.
func langFuncs() template.FuncMap {
	return template.FuncMap{
		"T": i18n.T,
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
