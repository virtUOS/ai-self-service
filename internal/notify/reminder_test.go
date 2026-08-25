package notify

import (
	"context"
	"database/sql"
	"errors"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/uptrace/bun"
	"github.com/uptrace/bun/dialect/sqlitedialect"
	"github.com/uptrace/bun/driver/sqliteshim"

	"github.com/virtuos/ai-self-service/internal/database"
)

// recorder captures messages instead of sending them.
type recorder struct {
	mu   sync.Mutex
	sent []Message
	err  error
}

func (r *recorder) Notify(_ context.Context, m Message) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.err != nil {
		return r.err
	}
	r.sent = append(r.sent, m)
	return nil
}

func (r *recorder) count() int {
	r.mu.Lock()
	defer r.mu.Unlock()
	return len(r.sent)
}

func setup(t *testing.T, name string) *database.Store {
	t.Helper()
	sqldb, err := sql.Open(sqliteshim.ShimName, "file:"+name+"?mode=memory&cache=shared")
	if err != nil {
		t.Fatal(err)
	}
	db := bun.NewDB(sqldb, sqlitedialect.New())
	t.Cleanup(func() { db.Close() })
	s := database.NewStore(db)
	if err := s.RunMigrations(context.Background()); err != nil {
		t.Fatal(err)
	}
	return s
}

// addKey creates a user with a key expiring in the given number of days.
func addKey(t *testing.T, s *database.Store, sub, email string, inDays int) *database.APIKey {
	t.Helper()
	ctx := context.Background()
	u, err := s.GetOrCreateUser(ctx, sub, email, "Test User")
	if err != nil {
		t.Fatal(err)
	}
	k := &database.APIKey{
		UserID: u.ID, LiteLLMKey: "sk-" + sub, KeyPrefix: "sk-" + sub,
		ExpiresAt: time.Now().AddDate(0, 0, inDays),
	}
	if err := s.ReplaceAPIKey(ctx, k); err != nil {
		t.Fatal(err)
	}
	return k
}

func TestReminderWarnsKeysInsideWindow(t *testing.T) {
	s := setup(t, "rem1")
	addKey(t, s, "soon", "soon@uni.de", 2)    // inside 3d and 14d
	addKey(t, s, "later", "later@uni.de", 40) // outside every window

	rec := &recorder{}
	r := NewReminder(s, rec, "https://portal", nil)
	if err := r.Run(context.Background()); err != nil {
		t.Fatal(err)
	}

	if rec.count() != 2 {
		t.Fatalf("sent %d messages, want 2 (14d and 3d for the soon key)", rec.count())
	}
	for _, m := range rec.sent {
		if m.To != "soon@uni.de" {
			t.Errorf("unexpected recipient %s", m.To)
		}
		if !strings.Contains(m.Body, "https://portal") {
			t.Error("message lacks the portal link")
		}
	}
}

// The whole point of the notices table: repeated runs must not re-mail.
func TestReminderDoesNotRepeat(t *testing.T) {
	s := setup(t, "rem2")
	addKey(t, s, "u1", "u1@uni.de", 2)

	rec := &recorder{}
	r := NewReminder(s, rec, "https://portal", nil)
	ctx := context.Background()

	if err := r.Run(ctx); err != nil {
		t.Fatal(err)
	}
	first := rec.count()
	for i := 0; i < 3; i++ {
		if err := r.Run(ctx); err != nil {
			t.Fatal(err)
		}
	}
	if rec.count() != first {
		t.Fatalf("re-sent notices: %d after repeated runs, want %d", rec.count(), first)
	}
}

// Already-expired keys are not worth a warning.
func TestReminderSkipsExpiredKeys(t *testing.T) {
	s := setup(t, "rem3")
	addKey(t, s, "gone", "gone@uni.de", -5)

	rec := &recorder{}
	if err := NewReminder(s, rec, "https://portal", nil).Run(context.Background()); err != nil {
		t.Fatal(err)
	}
	if rec.count() != 0 {
		t.Fatalf("warned about an expired key: %+v", rec.sent)
	}
}

// A delivery failure must not record the notice, so the next run retries.
func TestReminderRetriesAfterFailure(t *testing.T) {
	s := setup(t, "rem4")
	addKey(t, s, "u1", "u1@uni.de", 2)

	rec := &recorder{err: errors.New("relay down")}
	r := NewReminder(s, rec, "https://portal", []int{3})
	ctx := context.Background()

	if err := r.Run(ctx); err != nil {
		t.Fatal(err)
	}
	if rec.count() != 0 {
		t.Fatal("recorder should have captured nothing while failing")
	}

	// Relay recovers; the notice must still be pending.
	rec.err = nil
	if err := r.Run(ctx); err != nil {
		t.Fatal(err)
	}
	if rec.count() != 1 {
		t.Fatalf("notice not retried after failure: sent %d", rec.count())
	}
}

func TestReminderMessageWording(t *testing.T) {
	s := setup(t, "rem5")
	addKey(t, s, "u1", "u1@uni.de", 1)

	rec := &recorder{}
	if err := NewReminder(s, rec, "https://portal", []int{1}).Run(context.Background()); err != nil {
		t.Fatal(err)
	}
	if rec.count() != 1 {
		t.Fatalf("sent %d, want 1", rec.count())
	}
	m := rec.sent[0]
	if !strings.Contains(m.Subject, "tomorrow") {
		t.Errorf("subject = %q, want it to say tomorrow", m.Subject)
	}
	if !strings.Contains(m.Body, "Test User") {
		t.Error("body does not address the user by name")
	}
}

// Discard must not error, so an unconfigured deployment still runs.
func TestDiscardNotifier(t *testing.T) {
	if err := (Discard{}).Notify(context.Background(), Message{To: "a@b.c"}); err != nil {
		t.Fatalf("Discard returned %v", err)
	}
}

// The portal is German by default, so notices must not arrive English-only.
// There is no per-user language on record, so the mail carries both, German
// first to match the UI default. See issue #6.
func TestReminderMessageIsBilingual(t *testing.T) {
	s := setup(t, "rem6")
	addKey(t, s, "u1", "u1@uni.de", 1)

	rec := &recorder{}
	if err := NewReminder(s, rec, "https://portal", []int{1}).Run(context.Background()); err != nil {
		t.Fatal(err)
	}
	m := rec.sent[0]

	for _, want := range []string{"Ihr KI-API-Schlüssel", "läuft", "verlängern"} {
		if !strings.Contains(m.Body, want) {
			t.Errorf("body missing German text %q", want)
		}
	}
	for _, want := range []string{"AI API key", "expires", "extend"} {
		if !strings.Contains(m.Body, want) {
			t.Errorf("body missing English text %q", want)
		}
	}
	if !strings.Contains(m.Subject, "läuft") || !strings.Contains(m.Subject, "expires") {
		t.Errorf("subject = %q, want both languages", m.Subject)
	}
	// Both halves must still carry the facts the user needs to act.
	if strings.Count(m.Body, "https://portal") < 2 {
		t.Error("portal link should appear in both halves")
	}
}
