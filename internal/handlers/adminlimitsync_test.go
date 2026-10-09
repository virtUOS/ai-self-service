package handlers

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/go-chi/chi/v5"

	"github.com/virtuos/ai-self-service/internal/config"
	"github.com/virtuos/ai-self-service/internal/database"
	"github.com/virtuos/ai-self-service/internal/i18n"
	"github.com/virtuos/ai-self-service/internal/session"
)

// syncStub stands in for the background limit sync.
type syncStub struct {
	mu       sync.Mutex
	kicks    int
	running  bool
	finished time.Time
}

func (s *syncStub) Kick() {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.kicks++
}

func (s *syncStub) Status() (bool, time.Time) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.running, s.finished
}

func (s *syncStub) kickCount() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.kicks
}

// newSyncTestAdmin is an Admin able to render the panel, with a seeded default
// profile and a stub sync.
func newSyncTestAdmin(t *testing.T, name string) (*Admin, *database.Store, string, *syncStub) {
	t.Helper()
	a, store, token := newGrantTestAdmin(t, name)
	if err := store.SeedDefaultProfile(context.Background()); err != nil {
		t.Fatal(err)
	}
	csrf, err := session.NewCSRF(false, "test-seed")
	if err != nil {
		t.Fatal(err)
	}
	stub := &syncStub{}
	a.cfg = &config.Config{LimitSyncInterval: 5 * time.Minute, BudgetUnit: "$"}
	a.tmpl = parseAdminTemplate()
	a.models = newModelCache(nil)
	a.csrf = csrf
	a.sync = stub
	return a, store, token, stub
}

func postAdminForm(t *testing.T, h http.HandlerFunc, token, path string, id int64, form url.Values) {
	t.Helper()
	req := httptest.NewRequest(http.MethodPost, path, strings.NewReader(form.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.AddCookie(&http.Cookie{Name: "session_token", Value: token})
	rctx := chi.NewRouteContext()
	rctx.URLParams.Add("id", strconv.FormatInt(id, 10))
	req = req.WithContext(context.WithValue(req.Context(), chi.RouteCtxKey, rctx))
	rec := httptest.NewRecorder()
	h(rec, req)
	if rec.Code != http.StatusFound {
		t.Fatalf("%s: status %d, want a redirect", path, rec.Code)
	}
}

// Every admin change that can affect what keys enforce starts a sync run, so
// it reaches the gateway without waiting for the retry interval.
func TestAdminChangesStartTheLimitSync(t *testing.T) {
	a, store, token, stub := newSyncTestAdmin(t, "alsync-kick")
	ctx := context.Background()
	def, err := store.GetDefaultProfile(ctx)
	if err != nil {
		t.Fatal(err)
	}
	u, err := store.GetOrCreateUser(ctx, "sub-x", "x@uni-osnabrueck.de", "X")
	if err != nil {
		t.Fatal(err)
	}

	postAdminForm(t, a.UpdateProfile, token, "/admin/profiles/1", def.ID,
		url.Values{"name": {"default"}, "is_default": {"on"}, "tpm_limit": {"1000"}})
	if stub.kickCount() != 1 {
		t.Fatalf("kicks after a profile edit = %d, want 1", stub.kickCount())
	}

	postAdminForm(t, a.CreateProfile, token, "/admin/profiles", 0, url.Values{"name": {"extra"}})
	if stub.kickCount() != 2 {
		t.Fatalf("kicks after creating a profile = %d, want 2", stub.kickCount())
	}

	postAdminForm(t, a.SetUserProfile, token, "/admin/users/1/profile", u.ID,
		url.Values{"profile_id": {strconv.FormatInt(def.ID, 10)}})
	if stub.kickCount() != 3 {
		t.Fatalf("kicks after assigning a profile = %d, want 3", stub.kickCount())
	}

	extra, err := store.ListProfiles(ctx)
	if err != nil {
		t.Fatal(err)
	}
	for _, p := range extra {
		if p.Name == "extra" {
			postAdminForm(t, a.DeleteProfile, token, "/admin/profiles/1/delete", p.ID, nil)
		}
	}
	if stub.kickCount() != 4 {
		t.Errorf("kicks after deleting a profile = %d, want 4", stub.kickCount())
	}
}

func renderPanel(t *testing.T, a *Admin, token string) (string, i18n.Lang) {
	t.Helper()
	req := httptest.NewRequest(http.MethodGet, "/admin/", nil)
	req.AddCookie(&http.Cookie{Name: "session_token", Value: token})
	rec := httptest.NewRecorder()
	a.Panel(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("panel status %d: %s", rec.Code, rec.Body.String())
	}
	return rec.Body.String(), i18n.FromRequest(req)
}

func TestPanelSaysWhenAllKeysAreUpToDate(t *testing.T) {
	a, _, token, _ := newSyncTestAdmin(t, "alsync-ok")
	body, lang := renderPanel(t, a, token)
	for _, want := range []string{
		i18n.T(lang, "admin.sync.title"),
		i18n.T(lang, "admin.sync.queue") + ": " + i18n.T(lang, "admin.sync.ok"),
	} {
		if !strings.Contains(body, want) {
			t.Errorf("panel lacks %q", want)
		}
	}
}

// The card tells every admin how many keys are still waiting, what failed and
// why, and which profiles they are on.
func TestPanelShowsPendingAndFailedKeys(t *testing.T) {
	a, store, token, stub := newSyncTestAdmin(t, "alsync-pending")
	ctx := context.Background()
	for _, sub := range []string{"a", "b"} {
		u, err := store.GetOrCreateUser(ctx, sub, sub+"@uni-osnabrueck.de", sub)
		if err != nil {
			t.Fatal(err)
		}
		k := &database.APIKey{UserID: u.ID, LiteLLMKey: "sk-" + sub, KeyPrefix: "sk-" + sub,
			ExpiresAt: time.Now().Add(time.Hour)}
		if err := store.ReplaceAPIKey(ctx, k); err != nil {
			t.Fatal(err)
		}
		if sub == "b" {
			if err := store.MarkLimitSyncFailed(ctx, k.ID, "gateway said 500", time.Now()); err != nil {
				t.Fatal(err)
			}
		}
	}
	stub.running = true

	body, lang := renderPanel(t, a, token)
	for _, want := range []string{
		fmt.Sprintf(i18n.T(lang, "admin.sync.pending"), 2),
		i18n.T(lang, "admin.sync.running"),
		fmt.Sprintf(i18n.T(lang, "admin.sync.failed.one"), 1),
		"b@uni-osnabrueck.de",
		"gateway said 500",
		"2 " + i18n.T(lang, "admin.sync.badge"),
	} {
		if !strings.Contains(body, want) {
			t.Errorf("panel lacks %q", want)
		}
	}
}

func TestFormatInterval(t *testing.T) {
	for d, want := range map[time.Duration]string{
		5 * time.Minute:         "5 min",
		2 * time.Hour:           "2 h",
		90 * time.Second:        "90 s",
		1500 * time.Millisecond: "1.5s",
	} {
		if got := formatInterval(d); got != want {
			t.Errorf("formatInterval(%v) = %q, want %q", d, got, want)
		}
	}
}

// One pending key gets the singular sentence, with the count filled in.
func TestPanelWordsASinglePendingKey(t *testing.T) {
	a, store, token, _ := newSyncTestAdmin(t, "alsync-one")
	ctx := context.Background()
	u, err := store.GetOrCreateUser(ctx, "a", "a@uni-osnabrueck.de", "a")
	if err != nil {
		t.Fatal(err)
	}
	k := &database.APIKey{UserID: u.ID, LiteLLMKey: "sk-a", KeyPrefix: "sk-a",
		ExpiresAt: time.Now().Add(time.Hour)}
	if err := store.ReplaceAPIKey(ctx, k); err != nil {
		t.Fatal(err)
	}
	if err := store.MarkLimitSyncFailed(ctx, k.ID, "boom", time.Now()); err != nil {
		t.Fatal(err)
	}

	body, lang := renderPanel(t, a, token)
	for _, want := range []string{
		fmt.Sprintf(i18n.T(lang, "admin.sync.pending.one"), 1),
		fmt.Sprintf(i18n.T(lang, "admin.sync.failed.one"), 1),
	} {
		if !strings.Contains(body, want) {
			t.Errorf("panel lacks %q", want)
		}
	}
	// A message whose placeholders do not match its arguments renders Go's
	// formatting error instead of text.
	if strings.Contains(body, "%!") {
		t.Error("the panel shows a formatting error")
	}
}
