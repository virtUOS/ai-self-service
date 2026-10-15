package handlers

import (
	"context"
	"net/http"
	"net/http/httptest"
	"net/url"
	"testing"
	"time"

	"github.com/virtuos/keyosk/internal/config"
	"github.com/virtuos/keyosk/internal/database"
	"github.com/virtuos/keyosk/internal/keyprovider"
	oidcpkg "github.com/virtuos/keyosk/internal/oidc"
	"github.com/virtuos/keyosk/internal/oidc/oidctest"
	"github.com/virtuos/keyosk/internal/session"
)

// loginTest is a UI whose logins go through an in-process OIDC issuer.
type loginTest struct {
	ui    *UI
	store *database.Store
	idp   *oidctest.Server
	kicks int
}

func newLoginTest(t *testing.T, name string) *loginTest {
	t.Helper()
	lt := &loginTest{store: newAuthTestStore(t, name), idp: oidctest.NewServer(t)}
	cfg := &config.Config{
		OIDCIssuerURL:    lt.idp.Issuer(),
		OIDCClientID:     "keyosk",
		OIDCClientSecret: "secret",
		OIDCRedirectURL:  "http://localhost:8080/callback",
		FrontendURL:      "http://localhost:8080",
		KeyDurationDays:  90,
	}
	provider, err := oidcpkg.NewProvider(context.Background(), cfg, lt.store)
	if err != nil {
		t.Fatal(err)
	}
	csrf, err := session.NewCSRF(false, "test-seed")
	if err != nil {
		t.Fatal(err)
	}
	lt.ui = NewUI(cfg, lt.store, session.NewManager(lt.store, time.Hour, false),
		provider, keyprovider.NewFake(), csrf, func() { lt.kicks++ })
	return lt
}

// login runs /login, has the issuer hand out a code for claims, and returns
// the response to the browser's return to /callback.
func (lt *loginTest) login(t *testing.T, claims oidctest.Claims) *httptest.ResponseRecorder {
	t.Helper()
	rec := httptest.NewRecorder()
	lt.ui.Login(rec, httptest.NewRequest(http.MethodGet, "http://localhost:8080/login", nil))
	loc, err := url.Parse(rec.Header().Get("Location"))
	if err != nil {
		t.Fatal(err)
	}
	code := "code-" + claims.Sub + "-" + claims.Email
	lt.idp.IssueCode(code, claims)

	q := url.Values{"state": {loc.Query().Get("state")}, "code": {code}}
	req := httptest.NewRequest(http.MethodGet, "http://localhost:8080/callback?"+q.Encode(), nil)
	for _, c := range rec.Result().Cookies() {
		req.AddCookie(c)
	}
	rec = httptest.NewRecorder()
	lt.ui.Callback(rec, req)
	return rec
}

// sessionOf returns the session the response's cookie opens, or nil.
func (lt *loginTest) sessionOf(t *testing.T, rec *httptest.ResponseRecorder) *session.SessionUser {
	t.Helper()
	for _, c := range rec.Result().Cookies() {
		if c.Name == "session_token" && c.Value != "" {
			su, err := lt.ui.sessions.Get(context.Background(), c.Value)
			if err != nil {
				t.Fatalf("session cookie does not open a session: %v", err)
			}
			return su
		}
	}
	return nil
}

// A completed login creates the user, opens a session for them and starts a
// limit sync, which pushes a changed name or address to the gateway.
func TestLoginStartsASession(t *testing.T) {
	lt := newLoginTest(t, "login1")
	rec := lt.login(t, oidctest.Claims{Sub: "sub-1", Email: "a@uni-osnabrueck.de", PreferredUsername: "alovelace"})

	if rec.Code != http.StatusFound || rec.Header().Get("Location") != "/" {
		t.Fatalf("callback answered %d to %q, want a redirect to /", rec.Code, rec.Header().Get("Location"))
	}
	su := lt.sessionOf(t, rec)
	if su == nil {
		t.Fatal("no session cookie set")
	}
	if su.User.OIDCSub != "sub-1" || su.User.Email != "a@uni-osnabrueck.de" || su.User.Name != "alovelace" {
		t.Errorf("session user = %+v, want sub-1 / a@uni-osnabrueck.de / alovelace", su.User)
	}
	// Logout sends it as the hint, and the admin check reads roles from it.
	if su.IDToken == "" {
		t.Error("session holds no id token")
	}
	if lt.kicks != 1 {
		t.Errorf("limit sync started %d times, want 1", lt.kicks)
	}
}

// A user is the OIDC subject, not the address: logging in again with a new
// address or name updates the same user, so their key and profile stay theirs.
func TestLoginUpdatesAReturningUser(t *testing.T) {
	lt := newLoginTest(t, "login2")
	first := lt.sessionOf(t, lt.login(t, oidctest.Claims{Sub: "sub-1", Email: "old@uni-osnabrueck.de", Name: "Old"}))
	second := lt.sessionOf(t, lt.login(t, oidctest.Claims{Sub: "sub-1", Email: "new@uni-osnabrueck.de", Name: "New"}))

	if first == nil || second == nil {
		t.Fatal("a login opened no session")
	}
	if second.User.ID != first.User.ID {
		t.Errorf("second login is user %d, want the same user %d", second.User.ID, first.User.ID)
	}
	if second.User.Email != "new@uni-osnabrueck.de" || second.User.Name != "New" {
		t.Errorf("user after second login = %+v, want the new address and name", second.User)
	}
	if users, _ := lt.store.ListUsers(context.Background()); len(users) != 1 {
		t.Errorf("%d users after two logins of one subject, want 1", len(users))
	}
}

// A callback that fails the state check opens no session and creates no user.
func TestFailedLoginStartsNoSession(t *testing.T) {
	lt := newLoginTest(t, "login3")
	lt.idp.IssueCode("code", oidctest.Claims{Sub: "sub-1", Email: "a@uni-osnabrueck.de"})

	// A callback link sent to a victim: right code, but no state cookie.
	req := httptest.NewRequest(http.MethodGet, "http://localhost:8080/callback?state=forged&code=code", nil)
	rec := httptest.NewRecorder()
	lt.ui.Callback(rec, req)

	if rec.Code != http.StatusBadRequest {
		t.Errorf("status %d, want 400", rec.Code)
	}
	if su := lt.sessionOf(t, rec); su != nil {
		t.Errorf("session opened for %+v", su.User)
	}
	if users, _ := lt.store.ListUsers(context.Background()); len(users) != 0 {
		t.Errorf("%d users created by a failed login, want 0", len(users))
	}
	if lt.kicks != 0 {
		t.Errorf("limit sync started %d times, want 0", lt.kicks)
	}
}
