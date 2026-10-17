package handlers

import (
	"net/http"
)

// Theme is the colour scheme chosen in the account settings. The empty Theme
// follows the browser, which is what a visitor without a choice gets.
type Theme string

const (
	ThemeSystem Theme = ""
	ThemeLight  Theme = "light"
	ThemeDark   Theme = "dark"

	// themeCookie remembers the choice across visits, like the language.
	themeCookie = "theme"
)

// Themes lists the choices in the order the settings page offers them.
var Themes = []Theme{ThemeSystem, ThemeLight, ThemeDark}

// validTheme reports whether t is one of the choices, the browser's included.
func validTheme(t Theme) bool {
	for _, v := range Themes {
		if v == t {
			return true
		}
	}
	return false
}

// ThemeFromRequest returns the visitor's chosen theme, or ThemeSystem when
// they have none. The page writes it onto <html data-theme>, so it is in
// place before the first paint, with no flash of the other scheme.
func ThemeFromRequest(r *http.Request) Theme {
	if c, err := r.Cookie(themeCookie); err == nil {
		if t := Theme(c.Value); validTheme(t) {
			return t
		}
	}
	return ThemeSystem
}

// setPrefCookie stores a preference for a year, or deletes it when value is
// empty, so that "follow the browser" means no cookie at all rather than one
// that has to be read and ignored.
func setPrefCookie(w http.ResponseWriter, name, value string, secure bool) {
	c := &http.Cookie{
		Name:     name,
		Value:    value,
		Path:     "/",
		MaxAge:   365 * 24 * 60 * 60,
		HttpOnly: true,
		Secure:   secure,
		SameSite: http.SameSiteLaxMode,
	}
	if value == "" {
		c.MaxAge = -1
	}
	http.SetCookie(w, c)
}
