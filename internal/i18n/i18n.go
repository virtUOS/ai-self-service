// Package i18n provides the portal's German and English message catalogues.
//
// German is the default: the audience is a German university, and a visitor
// with no stated preference should not have to switch. English is offered
// because the models and the API itself are English-facing and a good part of
// the research community here is not German-speaking.
//
// Messages are a flat map rather than a full ICU implementation: the portal has
// a few dozen strings, no gender agreement and only trivial pluralisation, so
// anything heavier would cost more than it returns.
package i18n

import (
	"net/http"
	"strconv"
	"strings"
)

// Lang identifies a supported language.
type Lang string

const (
	DE Lang = "de"
	EN Lang = "en"

	// Default is used when nothing else is known about the visitor.
	Default = DE

	// CookieName remembers an explicit choice across visits.
	CookieName = "lang"
)

// Supported lists the languages in the order the switcher shows them.
var Supported = []Lang{DE, EN}

// Valid reports whether l is a language the portal actually has messages for.
func Valid(l Lang) bool {
	for _, s := range Supported {
		if s == l {
			return true
		}
	}
	return false
}

// Name is the language's own endonym, for the switcher.
func (l Lang) Name() string {
	switch l {
	case EN:
		return "English"
	default:
		return "Deutsch"
	}
}

// FromRequest resolves the language for a request: an explicit cookie choice
// wins, then the browser's Accept-Language, then the default.
func FromRequest(r *http.Request) Lang {
	if c, err := r.Cookie(CookieName); err == nil {
		if l := Lang(c.Value); Valid(l) {
			return l
		}
	}
	return fromAcceptLanguage(r.Header.Get("Accept-Language"))
}

// fromAcceptLanguage picks the highest-priority supported language from an
// Accept-Language header, honouring q-values.
//
// Only the primary subtag is compared, so de-AT and de-CH both resolve to
// German rather than falling through to the default.
func fromAcceptLanguage(header string) Lang {
	best, bestQ := Default, -1.0

	for _, part := range strings.Split(header, ",") {
		tag, q := strings.TrimSpace(part), 1.0
		if i := strings.Index(tag, ";"); i >= 0 {
			q = parseQ(tag[i+1:])
			tag = strings.TrimSpace(tag[:i])
		}
		if tag == "" {
			continue
		}
		primary := Lang(strings.ToLower(strings.SplitN(tag, "-", 2)[0]))
		if Valid(primary) && q > bestQ {
			best, bestQ = primary, q
		}
	}
	return best
}

func parseQ(s string) float64 {
	s = strings.TrimSpace(s)
	if !strings.HasPrefix(s, "q=") {
		return 1.0
	}
	q, err := strconv.ParseFloat(strings.TrimSpace(s[2:]), 64)
	if err != nil || q < 0 || q > 1 {
		return 1.0
	}
	return q
}
