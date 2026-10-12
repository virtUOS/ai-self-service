package oidc

import (
	"context"
	"net/http"
	"net/http/httptest"
	"net/url"
	"testing"

	"github.com/virtuos/ai-self-service/internal/oidc/oidctest"
)

// startLogin runs /login and returns the state it sent to the provider and the
// cookie that carries it back.
func startLogin(t *testing.T, p *Provider) (string, *http.Cookie) {
	t.Helper()
	rec := httptest.NewRecorder()
	p.LoginHandler(rec, httptest.NewRequest(http.MethodGet, "http://localhost:8080/login", nil))
	loc, err := url.Parse(rec.Header().Get("Location"))
	if err != nil {
		t.Fatal(err)
	}
	state := loc.Query().Get("state")
	cookies := rec.Result().Cookies()
	if state == "" || len(cookies) != 1 {
		t.Fatalf("login gave state %q and %d cookies, want a state and its cookie", state, len(cookies))
	}
	return state, cookies[0]
}

// callback returns to /callback with state and code, carrying cookie if set.
func callback(p *Provider, state, code string, cookie *http.Cookie) (*CallbackResult, *httptest.ResponseRecorder, error) {
	q := url.Values{"state": {state}, "code": {code}}
	req := httptest.NewRequest(http.MethodGet, "http://localhost:8080/callback?"+q.Encode(), nil)
	if cookie != nil {
		req.AddCookie(cookie)
	}
	rec := httptest.NewRecorder()
	res, err := p.HandleCallback(rec, req)
	return res, rec, err
}

// A callback with the login's state and a valid code yields the identity from
// the verified id_token, and spends the state cookie.
func TestCallbackReturnsTheIdentity(t *testing.T) {
	m := oidctest.NewServer(t)
	p := newTestProvider(t, m)
	state, cookie := startLogin(t, p)
	m.IssueCode("code-1", oidctest.Claims{
		Sub: "sub-1", Email: "a@uni-osnabrueck.de", Name: "Ada Lovelace", PreferredUsername: "alovelace",
	})

	res, rec, err := callback(p, state, "code-1", cookie)
	if err != nil {
		t.Fatalf("callback: %v", err)
	}
	want := UserInfo{Subject: "sub-1", Email: "a@uni-osnabrueck.de", Name: "alovelace"}
	if *res.UserInfo != want {
		t.Errorf("user info = %+v, want %+v", *res.UserInfo, want)
	}
	// The session keeps the raw token for the logout hint and the role check.
	if tok, err := p.verifier.Verify(context.Background(), res.IDToken); err != nil || tok.Subject != "sub-1" {
		t.Errorf("id token does not verify as sub-1's: %v", err)
	}

	cleared := false
	for _, c := range rec.Result().Cookies() {
		if c.Name == cookie.Name && c.MaxAge < 0 {
			cleared = true
		}
	}
	if !cleared {
		t.Error("state cookie not cleared after the callback")
	}
}

// The stored name is the login name if the IdP sends one, then the display
// name, then the address, so a user is never shown blank.
func TestCallbackNameFallsBack(t *testing.T) {
	for _, tc := range []struct {
		claims oidctest.Claims
		want   string
	}{
		{oidctest.Claims{PreferredUsername: "alovelace", Name: "Ada Lovelace", Email: "a@x.de"}, "alovelace"},
		{oidctest.Claims{Name: "Ada Lovelace", Email: "a@x.de"}, "Ada Lovelace"},
		{oidctest.Claims{Email: "a@x.de"}, "a@x.de"},
	} {
		m := oidctest.NewServer(t)
		p := newTestProvider(t, m)
		state, cookie := startLogin(t, p)
		tc.claims.Sub = "sub-1"
		m.IssueCode("code", tc.claims)

		res, _, err := callback(p, state, "code", cookie)
		if err != nil {
			t.Fatalf("%+v: callback: %v", tc.claims, err)
		}
		if res.UserInfo.Name != tc.want {
			t.Errorf("%+v: name = %q, want %q", tc.claims, res.UserInfo.Name, tc.want)
		}
	}
}

// A callback whose state is not the one this browser's login started is
// refused before the code is redeemed: otherwise an attacker could log a
// victim in to the attacker's account by sending them a callback link.
func TestCallbackRejectsABadState(t *testing.T) {
	m := oidctest.NewServer(t)
	p := newTestProvider(t, m)
	state, cookie := startLogin(t, p)
	_, otherCookie := startLogin(t, p)
	m.IssueCode("code-1", oidctest.Claims{Sub: "sub-1", Email: "a@uni-osnabrueck.de"})

	for name, tc := range map[string]struct {
		state  string
		cookie *http.Cookie
	}{
		"no cookie":           {state, nil},
		"another login's":     {state, otherCookie},
		"state not in cookie": {"forged", cookie},
		"empty state":         {"", cookie},
	} {
		if _, _, err := callback(p, tc.state, "code-1", tc.cookie); err == nil {
			t.Errorf("%s: callback accepted", name)
		}
	}

	// None of them spent the code, so the state check came first.
	if _, _, err := callback(p, state, "code-1", cookie); err != nil {
		t.Errorf("the genuine callback failed after the refused ones: %v", err)
	}
}

// A code the provider never issued fails the login.
func TestCallbackRejectsAnUnknownCode(t *testing.T) {
	m := oidctest.NewServer(t)
	p := newTestProvider(t, m)
	state, cookie := startLogin(t, p)

	if _, _, err := callback(p, state, "never-issued", cookie); err == nil {
		t.Error("callback with an unknown code accepted")
	}
}
