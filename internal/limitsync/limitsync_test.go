package limitsync

import (
	"context"
	"errors"
	"fmt"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"github.com/virtuos/ai-self-service/internal/database"
	"github.com/virtuos/ai-self-service/internal/keyprovider"
)

// gatewayStub records pushes. Refs in failing are rejected, and beforePush,
// when set, runs ahead of every push.
type gatewayStub struct {
	mu         sync.Mutex
	pushes     []string
	owners     map[string]string
	limits     map[string]keyprovider.Limits
	failing    map[string]bool
	beforePush func(ref string)
}

func newGatewayStub() *gatewayStub {
	return &gatewayStub{owners: map[string]string{}, limits: map[string]keyprovider.Limits{},
		failing: map[string]bool{}}
}

func (g *gatewayStub) UpdateLimits(_ context.Context, ref, ownerID string, l keyprovider.Limits) error {
	g.mu.Lock()
	hook := g.beforePush
	g.mu.Unlock()
	if hook != nil {
		hook(ref)
	}

	g.mu.Lock()
	defer g.mu.Unlock()
	g.pushes = append(g.pushes, ref)
	if g.failing[ref] {
		return errors.New("gateway says no")
	}
	g.owners[ref] = ownerID
	g.limits[ref] = l
	return nil
}

func (g *gatewayStub) pushCount(ref string) int {
	g.mu.Lock()
	defer g.mu.Unlock()
	n := 0
	for _, r := range g.pushes {
		if r == ref {
			n++
		}
	}
	return n
}

func (g *gatewayStub) setFailing(ref string, fail bool) {
	g.mu.Lock()
	defer g.mu.Unlock()
	g.failing[ref] = fail
}

type fixture struct {
	store *database.Store
	ctx   context.Context
	def   *database.Profile
	gw    *gatewayStub
}

func newFixture(t *testing.T) *fixture {
	t.Helper()
	db, err := database.Open(filepath.Join(t.TempDir(), "test.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { db.Close() })
	store := database.NewStore(db)
	ctx := context.Background()
	if err := store.RunMigrations(ctx); err != nil {
		t.Fatal(err)
	}
	if err := store.SeedDefaultProfile(ctx); err != nil {
		t.Fatal(err)
	}
	def, err := store.GetDefaultProfile(ctx)
	if err != nil {
		t.Fatal(err)
	}
	return &fixture{store: store, ctx: ctx, def: def, gw: newGatewayStub()}
}

// addKey creates a user with a key that has never been synced.
func (f *fixture) addKey(t *testing.T, sub string) string {
	t.Helper()
	u, err := f.store.GetOrCreateUser(f.ctx, sub, sub+"@uni-osnabrueck.de", sub)
	if err != nil {
		t.Fatal(err)
	}
	ref := "sk-" + sub
	if err := f.store.ReplaceAPIKey(f.ctx, &database.APIKey{UserID: u.ID, LiteLLMKey: ref,
		KeyPrefix: ref, ExpiresAt: time.Now().Add(24 * time.Hour)}); err != nil {
		t.Fatal(err)
	}
	return ref
}

func (f *fixture) setTPM(t *testing.T, tpm int64) {
	t.Helper()
	p, err := f.store.GetProfile(f.ctx, f.def.ID)
	if err != nil {
		t.Fatal(err)
	}
	p.TPMLimit = &tpm
	if err := f.store.UpdateProfile(f.ctx, p); err != nil {
		t.Fatal(err)
	}
}

func (f *fixture) status(t *testing.T) *database.LimitSyncStatus {
	t.Helper()
	st, err := f.store.GetLimitSyncStatus(f.ctx)
	if err != nil {
		t.Fatal(err)
	}
	return st
}

func TestRunPushesProfileLimitsToPendingKeys(t *testing.T) {
	f := newFixture(t)
	f.setTPM(t, 500)
	a, b := f.addKey(t, "a"), f.addKey(t, "b")

	res, err := New(f.store, f.gw, 1).Run(f.ctx)
	if err != nil {
		t.Fatal(err)
	}
	if res.Synced != 2 || res.Failed != 0 {
		t.Errorf("result = %+v, want 2 synced", res)
	}
	for _, ref := range []string{a, b} {
		l := f.gw.limits[ref]
		if l.TokensPerMinute == nil || *l.TokensPerMinute != 500 {
			t.Errorf("%s got TPM %v, want 500", ref, l.TokensPerMinute)
		}
	}
	if f.gw.owners[a] != "a" {
		t.Errorf("owner of %s = %q, want the OIDC subject a", a, f.gw.owners[a])
	}
	if st := f.status(t); st.Pending != 0 {
		t.Errorf("pending after the run = %d, want 0", st.Pending)
	}

	// Nothing changed, so a second run pushes nothing.
	res, err = New(f.store, f.gw, 1).Run(f.ctx)
	if err != nil {
		t.Fatal(err)
	}
	if res.Synced != 0 || f.gw.pushCount(a) != 1 {
		t.Errorf("second run: result = %+v, pushes to a = %d; want nothing new", res, f.gw.pushCount(a))
	}
}

// A failing key is tried once per run, keeps its error for the admin panel,
// and does not stop the others.
func TestFailedKeyIsRetriedOnTheNextRun(t *testing.T) {
	f := newFixture(t)
	bad, good := f.addKey(t, "bad"), f.addKey(t, "good")
	f.gw.setFailing(bad, true)

	res, err := New(f.store, f.gw, 1).Run(f.ctx)
	if err != nil {
		t.Fatal(err)
	}
	if res.Synced != 1 || res.Failed != 1 {
		t.Errorf("result = %+v, want 1 synced and 1 failed", res)
	}
	if f.gw.pushCount(bad) != 1 || f.gw.pushCount(good) != 1 {
		t.Errorf("pushes bad/good = %d/%d, want 1/1", f.gw.pushCount(bad), f.gw.pushCount(good))
	}
	st := f.status(t)
	if st.Pending != 1 || st.Failed != 1 || len(st.RecentFailures) != 1 ||
		st.RecentFailures[0].Error != "gateway says no" {
		t.Fatalf("status = %+v, want the failed key with its error", st)
	}

	f.gw.setFailing(bad, false)
	if _, err := New(f.store, f.gw, 1).Run(f.ctx); err != nil {
		t.Fatal(err)
	}
	if st := f.status(t); st.Pending != 0 {
		t.Errorf("pending after the retry = %d, want 0", st.Pending)
	}
}

// An admin who saves again while a run is under way must not have to wait for
// the next run: a key pushed with the old limits goes out of date again, and
// the same run pushes the new ones.
func TestEditDuringRunIsPickedUpBySameRun(t *testing.T) {
	f := newFixture(t)
	ref := f.addKey(t, "a")
	f.setTPM(t, 100)

	var once sync.Once
	f.gw.beforePush = func(string) {
		once.Do(func() { f.setTPM(t, 200) })
	}

	if _, err := New(f.store, f.gw, 1).Run(f.ctx); err != nil {
		t.Fatal(err)
	}
	if n := f.gw.pushCount(ref); n != 2 {
		t.Errorf("pushes = %d, want 2: the old limits, then the new ones", n)
	}
	if l := f.gw.limits[ref]; l.TokensPerMinute == nil || *l.TokensPerMinute != 200 {
		t.Errorf("final TPM = %v, want 200", l.TokensPerMinute)
	}
	if st := f.status(t); st.Pending != 0 {
		t.Errorf("pending = %d, want 0", st.Pending)
	}
}

// Without any profile to take limits from, the key must not be pushed: empty
// limits mean "unlimited" to the gateway. The store refuses to un-default the
// default, so the state is set up directly, as a hand-edited database would.
func TestKeyWithoutProfileIsNeverPushedEmptyLimits(t *testing.T) {
	f := newFixture(t)
	ref := f.addKey(t, "a")
	if err := f.store.ExecRaw(f.ctx, "UPDATE profiles SET is_default = 0"); err != nil {
		t.Fatal(err)
	}

	res, err := New(f.store, f.gw, 1).Run(f.ctx)
	if err != nil {
		t.Fatal(err)
	}
	if f.gw.pushCount(ref) != 0 {
		t.Fatal("pushed limits to a key whose owner has no profile")
	}
	if res.Failed != 1 {
		t.Errorf("result = %+v, want 1 failed", res)
	}
	if st := f.status(t); st.Failed != 1 || st.RecentFailures[0].Error == "" {
		t.Errorf("status = %+v, want the failure recorded with a reason", st)
	}
}

func TestParallelWorkersSyncEveryKey(t *testing.T) {
	f := newFixture(t)
	const n = 40
	for i := 0; i < n; i++ {
		f.addKey(t, fmt.Sprintf("u%02d", i))
	}

	res, err := New(f.store, f.gw, 4).Run(f.ctx)
	if err != nil {
		t.Fatal(err)
	}
	if res.Synced != n {
		t.Errorf("synced = %d, want %d", res.Synced, n)
	}
	if st := f.status(t); st.Pending != 0 {
		t.Errorf("pending = %d, want 0", st.Pending)
	}
}

// Start runs on a kick without waiting for the interval, and stops when its
// context ends.
func TestKickStartsARun(t *testing.T) {
	f := newFixture(t)
	s := New(f.store, f.gw, 1)
	ctx, cancel := context.WithCancel(f.ctx)
	done := make(chan struct{})
	go func() {
		s.Start(ctx, time.Hour)
		close(done)
	}()

	waitFor(t, func() bool { _, fin := s.Status(); return !fin.IsZero() })
	ref := f.addKey(t, "late")
	s.Kick()
	waitFor(t, func() bool { return f.gw.pushCount(ref) == 1 })

	cancel()
	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("Start did not return after its context was cancelled")
	}
}

func waitFor(t *testing.T, cond func() bool) {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for !cond() {
		if time.Now().After(deadline) {
			t.Fatal("condition not met within 5s")
		}
		time.Sleep(10 * time.Millisecond)
	}
}
