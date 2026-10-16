package main

import (
	"context"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/go-chi/chi/v5"

	"github.com/virtuos/keyosk/internal/config"
	"github.com/virtuos/keyosk/internal/database"
	"github.com/virtuos/keyosk/internal/handlers"
	"github.com/virtuos/keyosk/internal/keyprovider"
	oidcpkg "github.com/virtuos/keyosk/internal/oidc"
	"github.com/virtuos/keyosk/internal/oidc/oidctest"
	"github.com/virtuos/keyosk/internal/session"
)

// These tests walk the real route table, so a route added later is checked
// without anyone remembering to list it here. Only the exceptions are listed.

// publicRoutes answer without a session.
var publicRoutes = map[string]bool{
	"GET /login":               true,
	"GET /callback":            true,
	"GET /privacy":             true,
	"GET /healthz":             true,
	"GET /readyz":              true,
	"POST /logout":             true,
	"POST /backchannel-logout": true,
}

// readOnlyPaths answer any method and change nothing, so they need neither a
// session nor a CSRF token.
var readOnlyPaths = map[string]bool{
	"/metrics":     true,
	"/static/*":    true,
	"/favicon.ico": true,
}

// metricsToken is the bearer token the test router's /metrics requires.
const metricsToken = "metrics-token"

// csrfExempt are the routes that change state without a CSRF token.
// /backchannel-logout is called server to server by the identity provider and
// is authenticated by the signed logout token it carries.
var csrfExempt = map[string]bool{
	"POST /backchannel-logout": true,
}

type routeTest struct {
	router   *chi.Mux
	sessions *session.Manager
	admin    int64
	user     int64
	csrf     *session.CSRF
}

type route struct{ method, pattern string }

func (r route) String() string { return r.method + " " + r.pattern }

// path fills in the pattern's parameters.
func (r route) path() string {
	p := strings.ReplaceAll(r.pattern, "{id}", "1")
	return strings.ReplaceAll(p, "*", "style.css")
}

func newRouteTest(t *testing.T) *routeTest {
	t.Helper()
	ctx := context.Background()
	db, err := database.Open(filepath.Join(t.TempDir(), "routes.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { db.Close() })
	store := database.NewStore(db)
	if err := store.RunMigrations(ctx); err != nil {
		t.Fatal(err)
	}
	if err := store.SeedDefaultProfile(ctx); err != nil {
		t.Fatal(err)
	}
	admin, err := store.GetOrCreateUser(ctx, "sub-admin", "admin@uni-osnabrueck.de", "Admin")
	if err != nil {
		t.Fatal(err)
	}
	if err := store.SetUserAdmin(ctx, admin.ID, true); err != nil {
		t.Fatal(err)
	}
	user, err := store.GetOrCreateUser(ctx, "sub-user", "user@uni-osnabrueck.de", "User")
	if err != nil {
		t.Fatal(err)
	}

	idp := oidctest.NewServer(t)
	cfg := &config.Config{
		OIDCIssuerURL:    idp.Issuer(),
		OIDCClientID:     "keyosk",
		OIDCClientSecret: "secret",
		OIDCRedirectURL:  "http://localhost:8080/callback",
		FrontendURL:      "http://localhost:8080",
		KeyDurationDays:  90,
		BudgetUnit:       "$",
		MetricsToken:     metricsToken,
	}
	provider, err := oidcpkg.NewProvider(ctx, cfg, store)
	if err != nil {
		t.Fatal(err)
	}
	csrf, err := session.NewCSRF(false, "test-seed")
	if err != nil {
		t.Fatal(err)
	}
	sessions := session.NewManager(store, time.Hour, false)
	keys := keyprovider.NewFake()
	ui := handlers.NewUI(cfg, store, sessions, provider, keys, csrf, nil)
	adm := handlers.NewAdmin(cfg, store, sessions, keys, csrf, idleSync{})

	return &routeTest{router: newRouter(cfg, ui, adm, csrf, db), sessions: sessions,
		admin: admin.ID, user: user.ID, csrf: csrf}
}

// idleSync is a limit sync that never runs.
type idleSync struct{}

func (idleSync) Kick()                     {}
func (idleSync) Status() (bool, time.Time) { return false, time.Time{} }

// routes lists every method and pattern the router serves.
func (rt *routeTest) routes(t *testing.T) []route {
	t.Helper()
	var out []route
	err := chi.Walk(rt.router, func(method, pattern string, _ http.Handler, _ ...func(http.Handler) http.Handler) error {
		out = append(out, route{method, pattern})
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	return out
}

// requestOpts says what a request carries besides the method and path.
type requestOpts struct {
	user      int64 // a fresh session for this user; 0 for none
	csrfToken bool  // a valid CSRF cookie and token
	crossSite bool  // marked by the browser as coming from another site
}

// do sends one request through the router. Each request gets its own session,
// so a route that ends one (/logout) does not affect the next.
func (rt *routeTest) do(t *testing.T, r route, o requestOpts) *httptest.ResponseRecorder {
	t.Helper()
	req := httptest.NewRequest(r.method, "http://localhost:8080"+r.path(), nil)
	if o.user != 0 {
		token, err := rt.sessions.Create(context.Background(), o.user, "")
		if err != nil {
			t.Fatal(err)
		}
		req.AddCookie(&http.Cookie{Name: "session_token", Value: token})
	}
	// The cookie is always sent, so a refusal is about the token alone.
	warm := httptest.NewRecorder()
	token := rt.csrf.Token(warm, httptest.NewRequest(http.MethodGet, "/", nil))
	req.AddCookie(warm.Result().Cookies()[0])
	if o.csrfToken {
		req.Header.Set("X-CSRF-Token", token)
	}
	if o.crossSite {
		req.Header.Set("Sec-Fetch-Site", "cross-site")
	}
	rec := httptest.NewRecorder()
	rt.router.ServeHTTP(rec, req)
	return rec
}

func redirectsToLogin(rec *httptest.ResponseRecorder) bool {
	return rec.Code == http.StatusFound && rec.Header().Get("Location") == "/login"
}

// Every route that is not deliberately public sends a visitor without a
// session to the login page, or answers 401 where a script is asking.
func TestEveryRouteRequiresALogin(t *testing.T) {
	rt := newRouteTest(t)
	seen := map[string]bool{}
	for _, r := range rt.routes(t) {
		seen[r.String()] = true
		if publicRoutes[r.String()] || readOnlyPaths[r.pattern] {
			continue
		}
		rec := rt.do(t, r, requestOpts{csrfToken: true})
		if r.String() == "GET /session/status" {
			if rec.Code != http.StatusUnauthorized {
				t.Errorf("%s without a session: %d, want 401", r, rec.Code)
			}
			continue
		}
		if !redirectsToLogin(rec) {
			t.Errorf("%s without a session: %d to %q, want a redirect to /login",
				r, rec.Code, rec.Header().Get("Location"))
		}
	}
	for r := range publicRoutes {
		if !seen[r] {
			t.Errorf("public route %s is not served; remove it from the list", r)
		}
	}
}

// /metrics answers the configured bearer token and nothing else.
func TestMetricsRequireTheToken(t *testing.T) {
	rt := newRouteTest(t)
	for _, tc := range []struct {
		auth string
		user int64
		want int
	}{
		{"", 0, http.StatusUnauthorized},
		// A session is no substitute: an admin is not a scraper.
		{"", rt.admin, http.StatusUnauthorized},
		{"Bearer wrong", 0, http.StatusUnauthorized},
		{"Bearer " + metricsToken, 0, http.StatusOK},
	} {
		req := httptest.NewRequest(http.MethodGet, "http://localhost:8080/metrics", nil)
		if tc.auth != "" {
			req.Header.Set("Authorization", tc.auth)
		}
		if tc.user != 0 {
			token, err := rt.sessions.Create(context.Background(), tc.user, "")
			if err != nil {
				t.Fatal(err)
			}
			req.AddCookie(&http.Cookie{Name: "session_token", Value: token})
		}
		rec := httptest.NewRecorder()
		rt.router.ServeHTTP(rec, req)
		if rec.Code != tc.want {
			t.Errorf("Authorization %q, user %d: %d, want %d", tc.auth, tc.user, rec.Code, tc.want)
		}
	}
}

// Every admin route refuses a signed-in user who is not an admin. The same
// request from an admin gets through, so the refusal is the admin check's.
func TestEveryAdminRouteRequiresAnAdmin(t *testing.T) {
	rt := newRouteTest(t)
	count := 0
	for _, r := range rt.routes(t) {
		if !strings.HasPrefix(r.pattern, "/admin") {
			continue
		}
		count++
		if rec := rt.do(t, r, requestOpts{user: rt.user, csrfToken: true}); rec.Code != http.StatusForbidden {
			t.Errorf("%s as a non-admin: %d, want 403", r, rec.Code)
		}
		rec := rt.do(t, r, requestOpts{user: rt.admin, csrfToken: true})
		if rec.Code == http.StatusForbidden || redirectsToLogin(rec) {
			t.Errorf("%s as an admin: %d to %q, want it handled", r, rec.Code, rec.Header().Get("Location"))
		}
	}
	// The panel and its seven actions; fewer means the walk missed some.
	if count < 8 {
		t.Errorf("found %d admin routes, want at least 8", count)
	}
}

// Every route that changes state refuses a request without a valid CSRF
// token, or one the browser marks as cross-site, even from a signed-in admin.
// With the token, the same request gets through.
func TestEveryStateChangingRouteRequiresCSRF(t *testing.T) {
	rt := newRouteTest(t)
	seen := map[string]bool{}
	for _, r := range rt.routes(t) {
		seen[r.String()] = true
		switch r.method {
		case http.MethodGet, http.MethodHead, http.MethodOptions, http.MethodTrace:
			continue
		}
		if csrfExempt[r.String()] || readOnlyPaths[r.pattern] {
			continue
		}
		if rec := rt.do(t, r, requestOpts{user: rt.admin}); rec.Code != http.StatusForbidden {
			t.Errorf("%s without a CSRF token: %d, want 403", r, rec.Code)
		}
		if rec := rt.do(t, r, requestOpts{user: rt.admin, csrfToken: true, crossSite: true}); rec.Code != http.StatusForbidden {
			t.Errorf("%s cross-site: %d, want 403", r, rec.Code)
		}
		if rec := rt.do(t, r, requestOpts{user: rt.admin, csrfToken: true}); rec.Code == http.StatusForbidden {
			t.Errorf("%s with a CSRF token: 403, want it handled", r)
		}
	}
	for r := range csrfExempt {
		if !seen[r] {
			t.Errorf("CSRF-exempt route %s is not served; remove it from the list", r)
		}
	}
}

// storableRoutes may be stored by a browser or proxy: they carry no user data
// and are not reached with a session cookie.
var storableRoutes = map[string]bool{
	"GET /healthz":             true,
	"GET /readyz":              true,
	"POST /backchannel-logout": true,
}

// Every route a browser reaches with the session cookie forbids storing its
// response, so a page that showed a new key once cannot come back from a
// cache. Static files stay cacheable.
func TestEveryUserRouteIsNotStored(t *testing.T) {
	rt := newRouteTest(t)
	seen := map[string]bool{}
	for _, r := range rt.routes(t) {
		seen[r.String()] = true
		if storableRoutes[r.String()] || readOnlyPaths[r.pattern] {
			continue
		}
		rec := rt.do(t, r, requestOpts{user: rt.admin, csrfToken: true})
		if got := rec.Header().Get("Cache-Control"); got != "no-store" {
			t.Errorf("%s: Cache-Control %q, want no-store", r, got)
		}
	}
	for r := range storableRoutes {
		if !seen[r] {
			t.Errorf("storable route %s is not served; remove it from the list", r)
		}
	}
	rec := rt.do(t, route{http.MethodGet, "/static/*"}, requestOpts{user: rt.admin})
	if rec.Code != http.StatusOK {
		t.Fatalf("GET /static/style.css: %d, want 200", rec.Code)
	}
	if got := rec.Header().Get("Cache-Control"); strings.Contains(got, "no-store") {
		t.Errorf("static file: Cache-Control %q, want it cacheable", got)
	}
}

// A huge body is refused before anything parses it, on every route that
// takes one, including the CSRF check and the unauthenticated logout hook.
func TestEveryStateChangingRouteLimitsTheBody(t *testing.T) {
	rt := newRouteTest(t)
	big := "x=" + strings.Repeat("a", handlers.MaxRequestBody+1)
	for _, r := range rt.routes(t) {
		switch r.method {
		case http.MethodGet, http.MethodHead, http.MethodOptions, http.MethodTrace:
			continue
		}
		if readOnlyPaths[r.pattern] {
			continue
		}
		for _, chunked := range []bool{false, true} {
			req := httptest.NewRequest(r.method, "http://localhost:8080"+r.path(), strings.NewReader(big))
			req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
			if chunked {
				req.ContentLength = -1
			}
			// A valid cookie, but the token is in the oversized body, so the
			// CSRF check has to read it.
			warm := httptest.NewRecorder()
			rt.csrf.Token(warm, httptest.NewRequest(http.MethodGet, "/", nil))
			req.AddCookie(warm.Result().Cookies()[0])
			rec := httptest.NewRecorder()
			rt.router.ServeHTTP(rec, req)

			want := http.StatusRequestEntityTooLarge
			if chunked && csrfExempt[r.String()] {
				want = http.StatusBadRequest // the handler's own ParseForm fails
			}
			if rec.Code != want {
				t.Errorf("%s (chunked=%v) with an oversized body: %d, want %d", r, chunked, rec.Code, want)
			}
		}
	}
}

func TestStaticAssets(t *testing.T) {
	rt := newRouteTest(t)
	for _, tc := range []struct {
		path, wantType, wantCache string
	}{
		{"/favicon.ico", "image/", ""},
		{"/static/favicon.svg", "image/svg+xml", ""},
		{"/static/fonts/space-grotesk-var-latin.woff2", "font/woff2", "immutable"},
	} {
		req := httptest.NewRequest(http.MethodGet, tc.path, nil)
		rec := httptest.NewRecorder()
		rt.router.ServeHTTP(rec, req)
		if rec.Code != http.StatusOK {
			t.Errorf("GET %s: %d, want 200", tc.path, rec.Code)
			continue
		}
		if got := rec.Header().Get("Content-Type"); !strings.HasPrefix(got, tc.wantType) {
			t.Errorf("GET %s: Content-Type %q, want %s", tc.path, got, tc.wantType)
		}
		got := rec.Header().Get("Cache-Control")
		if tc.wantCache == "" && got != "" {
			t.Errorf("GET %s: Cache-Control %q, want none", tc.path, got)
		}
		if !strings.Contains(got, tc.wantCache) {
			t.Errorf("GET %s: Cache-Control %q, want %q", tc.path, got, tc.wantCache)
		}
	}
}
