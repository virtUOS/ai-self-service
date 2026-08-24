package i18n

import (
	"net/http"
	"net/http/httptest"
	"testing"
)

// German is the default: a visitor with no stated preference gets German.
func TestDefaultIsGerman(t *testing.T) {
	if Default != DE {
		t.Fatalf("Default = %q, want de", Default)
	}
	req := httptest.NewRequest(http.MethodGet, "/", nil)
	if got := FromRequest(req); got != DE {
		t.Errorf("no headers: got %q, want de", got)
	}
}

func TestAcceptLanguage(t *testing.T) {
	cases := map[string]Lang{
		"":                        DE,
		"de":                      DE,
		"de-DE,de;q=0.9":          DE,
		"de-AT":                   DE, // regional variants resolve to German
		"en":                      EN,
		"en-GB,en;q=0.9":          EN,
		"en-US,en;q=0.9,de;q=0.8": EN,
		"de;q=0.8,en;q=0.9":       EN, // q-values decide, not order
		"fr,es":                   DE, // nothing supported -> default
		"fr,en;q=0.5":             EN,
	}
	for header, want := range cases {
		req := httptest.NewRequest(http.MethodGet, "/", nil)
		if header != "" {
			req.Header.Set("Accept-Language", header)
		}
		if got := FromRequest(req); got != want {
			t.Errorf("Accept-Language %q: got %q, want %q", header, got, want)
		}
	}
}

// An explicit choice must beat the browser's preference.
func TestCookieOverridesHeader(t *testing.T) {
	req := httptest.NewRequest(http.MethodGet, "/", nil)
	req.Header.Set("Accept-Language", "de-DE,de;q=0.9")
	req.AddCookie(&http.Cookie{Name: CookieName, Value: "en"})
	if got := FromRequest(req); got != EN {
		t.Errorf("cookie ignored: got %q, want en", got)
	}
}

// A junk cookie must not wedge the portal into an unsupported language.
func TestInvalidCookieFallsBack(t *testing.T) {
	req := httptest.NewRequest(http.MethodGet, "/", nil)
	req.AddCookie(&http.Cookie{Name: CookieName, Value: "klingon"})
	if got := FromRequest(req); got != DE {
		t.Errorf("got %q, want the default", got)
	}
}

// Every key must exist in both languages, or a user sees a mix.
func TestAllKeysTranslated(t *testing.T) {
	for key, m := range messages {
		for _, l := range Supported {
			if s, ok := m[l]; !ok || s == "" {
				t.Errorf("key %q missing %s translation", key, l)
			}
		}
	}
}

func TestTFallsBackVisibly(t *testing.T) {
	if got := T(DE, "nav.signout"); got != "Abmelden" {
		t.Errorf("got %q", got)
	}
	// An unknown key returns itself rather than an empty string.
	if got := T(DE, "no.such.key"); got != "no.such.key" {
		t.Errorf("unknown key returned %q, want the key", got)
	}
}
