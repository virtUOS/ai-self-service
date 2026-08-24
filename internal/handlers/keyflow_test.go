package handlers

import (
	"context"
	"database/sql"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/uptrace/bun"
	"github.com/uptrace/bun/dialect/sqlitedialect"
	"github.com/uptrace/bun/driver/sqliteshim"

	"github.com/virtuos/ai-self-service/internal/config"
	"github.com/virtuos/ai-self-service/internal/database"
	"github.com/virtuos/ai-self-service/internal/keyprovider"
	"github.com/virtuos/ai-self-service/internal/session"
)

// newTestUI builds a UI backed by an in-memory database and a fake provider,
// plus a logged-in user and the cookies needed to satisfy CSRF.
func newTestUI(t *testing.T, name string) (*UI, *keyprovider.Fake, *database.Store, *database.User) {
	t.Helper()
	sqldb, err := sql.Open(sqliteshim.ShimName, "file:"+name+"?mode=memory&cache=shared")
	if err != nil {
		t.Fatal(err)
	}
	db := bun.NewDB(sqldb, sqlitedialect.New())
	t.Cleanup(func() { db.Close() })

	store := database.NewStore(db)
	ctx := context.Background()
	if err := store.RunMigrations(ctx); err != nil {
		t.Fatal(err)
	}
	if err := store.SeedDefaultProfile(ctx); err != nil {
		t.Fatal(err)
	}
	user, err := store.GetOrCreateUser(ctx, "sub-1", "s@uni-osnabrueck.de", "S")
	if err != nil {
		t.Fatal(err)
	}

	fake := keyprovider.NewFake()
	csrf, err := session.NewCSRF(false, "test-seed")
	if err != nil {
		t.Fatal(err)
	}
	sessions := session.NewManager(store, time.Hour, false)
	token, err := sessions.Create(ctx, user.ID, "id-token")
	if err != nil {
		t.Fatal(err)
	}
	t.Setenv("SESSION_TOKEN", token)

	ui := NewUI(&config.Config{KeyDurationDays: 90, FrontendURL: "http://x"},
		store, sessions, nil, fake, csrf)
	return ui, fake, store, user
}

// post issues an authenticated, CSRF-valid POST to h.
func post(t *testing.T, ui *UI, h http.HandlerFunc, path string) *httptest.ResponseRecorder {
	t.Helper()
	// Mint a CSRF cookie/token pair the way a rendered page would.
	warm := httptest.NewRecorder()
	tok := ui.csrf.Token(warm, httptest.NewRequest(http.MethodGet, "/", nil))
	csrfCookie := warm.Result().Cookies()[0]

	req := httptest.NewRequest(http.MethodPost, path,
		strings.NewReader(session.CSRFFormField+"="+tok))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.AddCookie(csrfCookie)
	req.AddCookie(&http.Cookie{Name: "session_token", Value: os.Getenv("SESSION_TOKEN")})

	rec := httptest.NewRecorder()
	h(rec, req)
	return rec
}

func TestGenerateKeyStoresAndIssues(t *testing.T) {
	ui, fake, store, user := newTestUI(t, "kf1")
	rec := post(t, ui, ui.GenerateKey, "/key/generate")
	if rec.Code != http.StatusFound {
		t.Fatalf("generate returned %d: %s", rec.Code, rec.Body.String())
	}
	if fake.LiveCount() != 1 {
		t.Fatalf("provider holds %d keys, want 1", fake.LiveCount())
	}
	k, err := store.GetAPIKeyByUser(context.Background(), user.ID)
	if err != nil || k == nil {
		t.Fatalf("key not stored: %v", err)
	}
	// The redirect must carry an opaque token, never the secret.
	if loc := rec.Header().Get("Location"); strings.Contains(loc, k.LiteLLMKey) {
		t.Errorf("redirect leaks the key: %s", loc)
	}
}

// If the key cannot be stored, it must be revoked upstream rather than left
// live with no local record of it — an unrevokable orphan.
func TestGenerateKeyRevokesOrphanWhenStoreFails(t *testing.T) {
	ui, fake, store, _ := newTestUI(t, "kf2")
	ctx := context.Background()

	// Reject any insert into api_keys so the handler's write fails after the
	// provider has already issued the key.
	if err := store.ExecRaw(ctx, `CREATE TRIGGER block_insert BEFORE INSERT ON api_keys
		BEGIN SELECT RAISE(ABORT, 'blocked'); END`); err != nil {
		t.Fatal(err)
	}

	rec := post(t, ui, ui.GenerateKey, "/key/generate")
	if rec.Code != http.StatusInternalServerError {
		t.Fatalf("expected 500 when the store fails, got %d", rec.Code)
	}
	if len(fake.Created) != 1 {
		t.Fatalf("provider created %d keys, want 1", len(fake.Created))
	}
	// The key must have been taken back rather than left live and unrevokable.
	if fake.LiveCount() != 0 {
		t.Errorf("orphaned key left live upstream: %d remain", fake.LiveCount())
	}
}

// Rotation must revoke the previous key, leaving exactly one live.
func TestGenerateKeyRevokesOldKeyAfterSuccess(t *testing.T) {
	ui, fake, store, user := newTestUI(t, "kf3")

	post(t, ui, ui.GenerateKey, "/key/generate")
	first, _ := store.GetAPIKeyByUser(context.Background(), user.ID)

	post(t, ui, ui.GenerateKey, "/key/generate")
	second, _ := store.GetAPIKeyByUser(context.Background(), user.ID)

	if first.LiteLLMKey == second.LiteLLMKey {
		t.Fatal("rotation did not issue a new key")
	}
	if fake.LiveCount() != 1 {
		t.Errorf("provider holds %d keys after rotation, want 1 (old key leaked)", fake.LiveCount())
	}
	if len(fake.Deleted) != 1 || fake.Deleted[0] != first.LiteLLMKey {
		t.Errorf("old key not revoked; deleted=%v", fake.Deleted)
	}
}

// A creation failure must leave the existing key untouched, not strand the user.
func TestGenerateKeyKeepsOldKeyWhenCreateFails(t *testing.T) {
	ui, fake, store, user := newTestUI(t, "kf4")

	post(t, ui, ui.GenerateKey, "/key/generate")
	original, _ := store.GetAPIKeyByUser(context.Background(), user.ID)

	fake.CreateErr = errors.New("gateway down")
	rec := post(t, ui, ui.GenerateKey, "/key/generate")
	if rec.Code != http.StatusInternalServerError {
		t.Fatalf("expected 500 on create failure, got %d", rec.Code)
	}

	after, _ := store.GetAPIKeyByUser(context.Background(), user.ID)
	if after == nil || after.LiteLLMKey != original.LiteLLMKey {
		t.Fatal("existing key was lost when creation failed")
	}
	if fake.LiveCount() != 1 {
		t.Errorf("provider holds %d keys, want the original still live", fake.LiveCount())
	}
}

func TestExtendKeyUsesProfileDuration(t *testing.T) {
	ui, fake, store, user := newTestUI(t, "kf5")
	ctx := context.Background()

	p := &database.Profile{Name: "students", KeyDurationDays: 30}
	if err := store.CreateProfile(ctx, p); err != nil {
		t.Fatal(err)
	}
	if err := store.SetUserProfile(ctx, user.ID, &p.ID); err != nil {
		t.Fatal(err)
	}

	post(t, ui, ui.GenerateKey, "/key/generate")
	rec := post(t, ui, ui.ExtendKey, "/key/extend")
	if rec.Code != http.StatusFound {
		t.Fatalf("extend returned %d", rec.Code)
	}
	if len(fake.Extended) != 1 {
		t.Fatalf("provider not asked to extend: %v", fake.Extended)
	}
	k, _ := store.GetAPIKeyByUser(ctx, user.ID)
	days := int(time.Until(k.ExpiresAt).Hours() / 24)
	if days < 28 || days > 31 {
		t.Errorf("expiry %d days out, want ~30 from the profile", days)
	}
}

func TestDeleteKeyRevokesUpstream(t *testing.T) {
	ui, fake, store, user := newTestUI(t, "kf6")
	post(t, ui, ui.GenerateKey, "/key/generate")

	post(t, ui, ui.DeleteKey, "/key/delete")
	if fake.LiveCount() != 0 {
		t.Errorf("provider still holds %d keys after delete", fake.LiveCount())
	}
	k, _ := store.GetAPIKeyByUser(context.Background(), user.ID)
	if k != nil {
		t.Error("key row still present after delete")
	}
}

// The profile's quota must reach the provider as a token allowance.
func TestGenerateKeyPassesQuotaToProvider(t *testing.T) {
	ui, fake, store, user := newTestUI(t, "kf7")
	ctx := context.Background()

	p := &database.Profile{Name: "students", QuotaTokens: 1_000_000, QuotaPeriod: "24h"}
	if err := store.CreateProfile(ctx, p); err != nil {
		t.Fatal(err)
	}
	if err := store.SetUserProfile(ctx, user.ID, &p.ID); err != nil {
		t.Fatal(err)
	}

	post(t, ui, ui.GenerateKey, "/key/generate")
	if len(fake.Created) != 1 {
		t.Fatal("no key created")
	}
	got := fake.Created[0].Limits
	if got.QuotaTokens != 1_000_000 || got.QuotaPeriod != "24h" {
		t.Errorf("limits = %d/%s, want 1000000/24h", got.QuotaTokens, got.QuotaPeriod)
	}
}
