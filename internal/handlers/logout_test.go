package handlers

import (
	"context"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"

	"github.com/virtuos/keyosk/internal/oidc/oidctest"
)

// sessionCookie returns the session cookie a login response set.
func sessionCookie(t *testing.T, rec *httptest.ResponseRecorder) *http.Cookie {
	t.Helper()
	for _, c := range rec.Result().Cookies() {
		if c.Name == "session_token" && c.Value != "" {
			return c
		}
	}
	t.Fatal("login set no session cookie")
	return nil
}

// request sends a request to h, with the session cookie if one is given.
func request(h http.HandlerFunc, method, path string, cookie *http.Cookie) *httptest.ResponseRecorder {
	req := httptest.NewRequest(method, path, nil)
	if cookie != nil {
		req.AddCookie(cookie)
	}
	rec := httptest.NewRecorder()
	h(rec, req)
	return rec
}

// open reports whether the session behind cookie still opens.
func (lt *loginTest) open(cookie *http.Cookie) bool {
	_, err := lt.ui.sessions.Get(context.Background(), cookie.Value)
	return err == nil
}

// backchannelLogout posts a logout token as the identity provider does.
func (lt *loginTest) backchannelLogout(token string) *httptest.ResponseRecorder {
	form := url.Values{"logout_token": {token}}
	req := httptest.NewRequest(http.MethodPost, "/backchannel-logout", strings.NewReader(form.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	rec := httptest.NewRecorder()
	lt.ui.BackchannelLogout(rec, req)
	return rec
}

// Signing out ends the session in the database, not only in the browser, and
// hands over to the provider to end the session there too.
func TestLogoutEndsTheSession(t *testing.T) {
	lt := newLoginTest(t, "logout1")
	login := lt.login(t, oidctest.Claims{Sub: "sub-1", Email: "a@uni-osnabrueck.de"})
	cookie := sessionCookie(t, login)
	idToken := lt.sessionOf(t, login).IDToken

	rec := request(lt.ui.Logout, http.MethodPost, "/logout", cookie)

	if lt.open(cookie) {
		t.Error("session still opens after logout; a copied cookie would keep working")
	}
	cleared := false
	for _, c := range rec.Result().Cookies() {
		if c.Name == "session_token" && c.MaxAge < 0 {
			cleared = true
		}
	}
	if !cleared {
		t.Error("session cookie not cleared")
	}
	if got := rec.Header().Get("Clear-Site-Data"); got != `"cache"` {
		t.Errorf("Clear-Site-Data = %q, want \"cache\" so Back cannot show the dashboard from bfcache", got)
	}

	loc, err := url.Parse(rec.Header().Get("Location"))
	if err != nil {
		t.Fatal(err)
	}
	if rec.Code != http.StatusFound || !strings.HasPrefix(loc.String(), lt.idp.Issuer()+"/protocol/openid-connect/logout") {
		t.Fatalf("logout answered %d to %q, want a redirect to the provider's logout", rec.Code, loc)
	}
	if got := loc.Query().Get("id_token_hint"); got != idToken {
		t.Error("provider logout does not carry the session's id token as the hint")
	}
	if got := loc.Query().Get("post_logout_redirect_uri"); got != "http://localhost:8080" {
		t.Errorf("post_logout_redirect_uri = %q, want the frontend URL", got)
	}
}

// Without a session there is nothing to end at the provider, so logout just
// clears the cookie and returns home.
func TestLogoutWithoutASession(t *testing.T) {
	lt := newLoginTest(t, "logout2")
	rec := request(lt.ui.Logout, http.MethodPost, "/logout", &http.Cookie{Name: "session_token", Value: "stale"})

	if rec.Code != http.StatusFound || rec.Header().Get("Location") != "/" {
		t.Errorf("logout answered %d to %q, want a redirect to /", rec.Code, rec.Header().Get("Location"))
	}
	if cookies := rec.Result().Cookies(); len(cookies) != 1 || cookies[0].MaxAge >= 0 {
		t.Errorf("cookies = %+v, want the session cookie cleared", cookies)
	}
}

// A back-channel logout from the provider ends every session of that subject,
// on every device, and no one else's.
func TestBackchannelLogoutEndsTheSubjectsSessions(t *testing.T) {
	lt := newLoginTest(t, "logout3")
	laptop := sessionCookie(t, lt.login(t, oidctest.Claims{Sub: "sub-1", Email: "a@uni-osnabrueck.de"}))
	phone := sessionCookie(t, lt.login(t, oidctest.Claims{Sub: "sub-1", Email: "a@uni-osnabrueck.de"}))
	other := sessionCookie(t, lt.login(t, oidctest.Claims{Sub: "sub-2", Email: "b@uni-osnabrueck.de"}))

	rec := lt.backchannelLogout(lt.idp.SignLogoutToken(t, "sub-1", "keyosk", false))

	if rec.Code != http.StatusOK {
		t.Fatalf("back-channel logout answered %d, want 200", rec.Code)
	}
	if lt.open(laptop) || lt.open(phone) {
		t.Error("a session of the logged-out subject still opens")
	}
	if !lt.open(other) {
		t.Error("another subject's session was ended too")
	}
}

// A token the provider would not send as a logout, or one for another client,
// ends nothing.
func TestBackchannelLogoutRejectsABadToken(t *testing.T) {
	lt := newLoginTest(t, "logout4")
	cookie := sessionCookie(t, lt.login(t, oidctest.Claims{Sub: "sub-1", Email: "a@uni-osnabrueck.de"}))

	for name, token := range map[string]string{
		"with a nonce":       lt.idp.SignLogoutToken(t, "sub-1", "keyosk", true),
		"for another client": lt.idp.SignLogoutToken(t, "sub-1", "another-client", false),
		"not a token":        "junk",
	} {
		if rec := lt.backchannelLogout(token); rec.Code != http.StatusBadRequest {
			t.Errorf("%s: answered %d, want 400", name, rec.Code)
		}
	}
	if !lt.open(cookie) {
		t.Error("a refused logout token ended the session")
	}
}

// The dashboard polls /session/status and goes to the login page on a 401, so
// it must answer 401 once the session is gone, however it went.
func TestSessionStatus(t *testing.T) {
	lt := newLoginTest(t, "logout5")
	cookie := sessionCookie(t, lt.login(t, oidctest.Claims{Sub: "sub-1", Email: "a@uni-osnabrueck.de"}))

	if rec := request(lt.ui.SessionStatus, http.MethodGet, "/session/status", cookie); rec.Code != http.StatusOK {
		t.Errorf("with a session: %d, want 200", rec.Code)
	}
	if rec := request(lt.ui.SessionStatus, http.MethodGet, "/session/status", nil); rec.Code != http.StatusUnauthorized {
		t.Errorf("without a cookie: %d, want 401", rec.Code)
	}
	lt.backchannelLogout(lt.idp.SignLogoutToken(t, "sub-1", "keyosk", false))
	if rec := request(lt.ui.SessionStatus, http.MethodGet, "/session/status", cookie); rec.Code != http.StatusUnauthorized {
		t.Errorf("after a back-channel logout: %d, want 401", rec.Code)
	}
}
