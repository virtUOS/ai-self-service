package limitsync

import (
	"context"
	"errors"
	"fmt"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/virtuos/keyosk/internal/database"
	"github.com/virtuos/keyosk/internal/keyprovider"
	"github.com/virtuos/keyosk/internal/metrics"
	"github.com/virtuos/keyosk/internal/metrics/metricstest"
)

// gatewayStub records pushes. Refs in failing are rejected, and beforePush,
// when set, runs ahead of every push.
type gatewayStub struct {
	mu      sync.Mutex
	pushes  []string
	owners  map[string]string
	users   map[string]keyprovider.User
	aliases map[string][]string

	aliasFailing bool
	// missing holds refs the gateway no longer has.
	missing    map[string]bool
	limits     map[string]keyprovider.Limits
	failing    map[string]bool
	beforePush func(ref string)

	// checks are the refs CheckKey was asked about; checkFailing makes it
	// fail for a ref without saying the key is gone.
	checks       []string
	checkFailing map[string]bool
}

func newGatewayStub() *gatewayStub {
	return &gatewayStub{owners: map[string]string{}, users: map[string]keyprovider.User{},
		aliases: map[string][]string{}, missing: map[string]bool{},
		limits:  map[string]keyprovider.Limits{},
		failing: map[string]bool{}, checkFailing: map[string]bool{}}
}

func (g *gatewayStub) UpdateLimits(_ context.Context, ref string, user keyprovider.User, l keyprovider.Limits) error {
	g.mu.Lock()
	hook := g.beforePush
	g.mu.Unlock()
	if hook != nil {
		hook(ref)
	}

	g.mu.Lock()
	defer g.mu.Unlock()
	g.pushes = append(g.pushes, ref)
	if g.missing[ref] {
		return fmt.Errorf("LiteLLM /key/update returned 404: %w", keyprovider.ErrKeyNotFound)
	}
	if g.failing[ref] {
		return errors.New("gateway says no")
	}
	g.owners[ref] = user.ID
	g.users[ref] = user
	g.limits[ref] = l
	return nil
}

func (g *gatewayStub) UpdateAlias(_ context.Context, ref, alias string) error {
	g.mu.Lock()
	defer g.mu.Unlock()
	if g.aliasFailing {
		return errors.New("alias taken")
	}
	g.aliases[ref] = append(g.aliases[ref], alias)
	return nil
}

// CheckKey answers as the gateway does: a ref in missing is gone, one in
// checkFailing cannot be answered for.
func (g *gatewayStub) CheckKey(_ context.Context, ref string) error {
	g.mu.Lock()
	defer g.mu.Unlock()
	g.checks = append(g.checks, ref)
	if g.checkFailing[ref] {
		return errors.New("gateway unreachable")
	}
	if g.missing[ref] {
		return fmt.Errorf("LiteLLM /key/info returned 404: %w", keyprovider.ErrKeyNotFound)
	}
	return nil
}

func (g *gatewayStub) setMissing(ref string, missing bool) {
	g.mu.Lock()
	defer g.mu.Unlock()
	g.missing[ref] = missing
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

// plainUser is the owner mapping of a portal with neither
// LITELLM_USER_ID_PREFIX nor LITELLM_USER_EMAIL.
func plainUser(sub, name, _ string) keyprovider.User {
	return keyprovider.User{ID: sub, Name: name}
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
	// The keys_missing gauge counts this test's keys, as it counts the
	// server's in production.
	metrics.CountMissingKeysWith(store.CountMissingKeys)
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
	if err := f.store.ReplaceAPIKey(f.ctx, &database.APIKey{UserID: u.ID, KeyRef: ref,
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
	if _, _, err := f.store.UpdateProfile(f.ctx, p, p.Quotas); err != nil {
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

	res, err := New(f.store, f.gw, plainUser, 1).Run(f.ctx)
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
	res, err = New(f.store, f.gw, plainUser, 1).Run(f.ctx)
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

	res, err := New(f.store, f.gw, plainUser, 1).Run(f.ctx)
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
	if _, err := New(f.store, f.gw, plainUser, 1).Run(f.ctx); err != nil {
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

	if _, err := New(f.store, f.gw, plainUser, 1).Run(f.ctx); err != nil {
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

	res, err := New(f.store, f.gw, plainUser, 1).Run(f.ctx)
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

	res, err := New(f.store, f.gw, plainUser, 4).Run(f.ctx)
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
	s := New(f.store, f.gw, plainUser, 1)
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

// An expired key is not pushed, so a portal whose keys have all expired
// writes nothing upstream, not even its owners' allowances (issue #82).
func TestExpiredKeyIsNotPushed(t *testing.T) {
	f := newFixture(t)
	ref := f.addKey(t, "a")
	u, err := f.store.GetUserByOIDCSub(f.ctx, "a")
	if err != nil {
		t.Fatal(err)
	}
	k, err := f.store.GetAPIKeyByUser(f.ctx, u.ID)
	if err != nil {
		t.Fatal(err)
	}
	if err := f.store.UpdateAPIKeyExpiry(f.ctx, k.ID, time.Now().Add(-time.Hour)); err != nil {
		t.Fatal(err)
	}

	res, err := New(f.store, f.gw, plainUser, 1).Run(f.ctx)
	if err != nil {
		t.Fatal(err)
	}
	if res.Synced != 0 || res.Failed != 0 || f.gw.pushCount(ref) != 0 {
		t.Errorf("result = %+v, pushes = %d; want nothing pushed", res, f.gw.pushCount(ref))
	}
}

// The sync binds each key to the gateway user the portal names its owner by,
// not to the bare subject, so a prefixed portal keeps its own allowances.
func TestRunPushesTheMappedOwner(t *testing.T) {
	f := newFixture(t)
	ref := f.addKey(t, "a")

	prefixed := func(sub, name, email string) keyprovider.User {
		return keyprovider.User{ID: "testing:" + sub, Name: name, Email: email}
	}
	if _, err := New(f.store, f.gw, prefixed, 1).Run(f.ctx); err != nil {
		t.Fatal(err)
	}
	if got := f.gw.owners[ref]; got != "testing:a" {
		t.Errorf("owner = %q, want testing:a", got)
	}
}

// The push labels the owner's gateway user with their name and, where the
// mapping allows, their email, and records both so the key is left alone
// until either changes.
func TestRunLabelsTheOwnerAndRecordsIt(t *testing.T) {
	f := newFixture(t)
	ref := f.addKey(t, "a")
	withEmail := func(sub, name, email string) keyprovider.User {
		return keyprovider.User{ID: sub, Name: name, Email: email}
	}
	if _, err := New(f.store, f.gw, withEmail, 1).Run(f.ctx); err != nil {
		t.Fatal(err)
	}
	if u := f.gw.users[ref]; u.Name != "a" || u.Email != "a@uni-osnabrueck.de" {
		t.Errorf("pushed user = %+v, want name a and its email", u)
	}

	// Nothing changed, so the next run pushes nothing.
	if _, err := New(f.store, f.gw, withEmail, 1).Run(f.ctx); err != nil {
		t.Fatal(err)
	}
	if n := f.gw.pushCount(ref); n != 1 {
		t.Errorf("pushes = %d, want 1", n)
	}

	// A login with a new name makes the next run push again.
	if _, err := f.store.GetOrCreateUser(f.ctx, "a", "a@uni-osnabrueck.de", "Alice"); err != nil {
		t.Fatal(err)
	}
	if _, err := New(f.store, f.gw, withEmail, 1).Run(f.ctx); err != nil {
		t.Fatal(err)
	}
	if u := f.gw.users[ref]; f.gw.pushCount(ref) != 2 || u.Name != "Alice" {
		t.Errorf("after the rename: pushes = %d, user = %+v; want a second push as Alice", f.gw.pushCount(ref), u)
	}
}

// The alias carries the username, so it is rebuilt when the name changes, and
// only then. A key whose name was never recorded is relabelled once.
func TestRunRelabelsTheKeyWhenTheNameChanges(t *testing.T) {
	f := newFixture(t)
	ref := f.addKey(t, "a")

	if _, err := New(f.store, f.gw, plainUser, 1).Run(f.ctx); err != nil {
		t.Fatal(err)
	}
	if got := f.gw.aliases[ref]; len(got) != 1 || !strings.HasPrefix(got[0], "a-a-") {
		t.Fatalf("aliases = %v, want one relabel as a-a-…", got)
	}

	// A profile change pushes the key again, but the name is the same.
	f.setTPM(t, 500)
	if _, err := New(f.store, f.gw, plainUser, 1).Run(f.ctx); err != nil {
		t.Fatal(err)
	}
	if got := f.gw.aliases[ref]; len(got) != 1 {
		t.Errorf("aliases = %v after a limits-only push, want no new relabel", got)
	}

	if _, err := f.store.GetOrCreateUser(f.ctx, "a", "a@uni-osnabrueck.de", "Alice Smith"); err != nil {
		t.Fatal(err)
	}
	if _, err := New(f.store, f.gw, plainUser, 1).Run(f.ctx); err != nil {
		t.Fatal(err)
	}
	if got := f.gw.aliases[ref]; len(got) != 2 || !strings.HasPrefix(got[1], "a-alice-smith-") {
		t.Errorf("aliases = %v after the rename, want a second one as a-alice-smith-…", got)
	}
}

// A relabel that fails, say because the alias is taken, fails the key like
// any push, so the next run tries again with a fresh suffix.
func TestFailedRelabelIsRetried(t *testing.T) {
	f := newFixture(t)
	ref := f.addKey(t, "a")
	f.gw.aliasFailing = true

	res, err := New(f.store, f.gw, plainUser, 1).Run(f.ctx)
	if err != nil {
		t.Fatal(err)
	}
	if res.Failed != 1 {
		t.Fatalf("result = %+v, want the key failed", res)
	}

	f.gw.aliasFailing = false
	res, err = New(f.store, f.gw, plainUser, 1).Run(f.ctx)
	if err != nil {
		t.Fatal(err)
	}
	if res.Synced != 1 || len(f.gw.aliases[ref]) != 1 {
		t.Errorf("retry: result = %+v, aliases = %v; want it synced and relabelled", res, f.gw.aliases[ref])
	}
}

// ageMissing moves the first sighting of every marked key back in time, so a
// test can put a key inside or past the grace period without sleeping.
func (f *fixture) ageMissing(t *testing.T, by time.Duration) {
	t.Helper()
	if err := f.store.ExecRaw(f.ctx, `UPDATE api_keys SET missing_since = ? WHERE missing_since IS NOT NULL`,
		time.Now().Add(-by)); err != nil {
		t.Fatal(err)
	}
}

// keyOf returns a user's key, or nil.
func (f *fixture) keyOf(t *testing.T, sub string) *database.APIKey {
	t.Helper()
	u, err := f.store.GetUserByOIDCSub(f.ctx, sub)
	if err != nil {
		t.Fatal(err)
	}
	k, err := f.store.GetAPIKeyByUser(f.ctx, u.ID)
	if err != nil {
		t.Fatal(err)
	}
	return k
}

func auditCount(t *testing.T, f *fixture, action string) int {
	t.Helper()
	events, err := f.store.ListAuditEvents(f.ctx, 50)
	if err != nil {
		t.Fatal(err)
	}
	n := 0
	for _, e := range events {
		if e.Action == action {
			n++
		}
	}
	return n
}

// A key the gateway no longer has is marked, not dropped (issue #108): a
// gateway that was wrong or restored would otherwise make the portal forget
// every key in one run. The marker leaves the key out of the sync, the other
// keys are untouched, and the sighting is counted once.
func TestRunMarksAKeyTheGatewayNoLongerHas(t *testing.T) {
	f := newFixture(t)
	gone, kept := f.addKey(t, "a"), f.addKey(t, "b")
	f.gw.missing[gone] = true
	before := metricstest.Counter(t, "keyosk_keys_marked_missing_total", `source="sync"`)

	res, err := New(f.store, f.gw, plainUser, 1).Run(f.ctx)
	if err != nil {
		t.Fatal(err)
	}
	if res.Marked != 1 || res.Synced != 1 || res.Failed != 0 || res.Removed != 0 {
		t.Errorf("result = %+v, want one marked and one synced", res)
	}
	if got := metricstest.Counter(t, "keyosk_keys_marked_missing_total", `source="sync"`) - before; got != 1 {
		t.Errorf("keys_marked_missing{source=sync} rose by %v, want 1", got)
	}
	if got := metricstest.Gauge(t, "keyosk_keys_missing", ""); got != 1 {
		t.Errorf("keys_missing gauge = %v, want 1", got)
	}
	k := f.keyOf(t, "a")
	if k == nil || k.MissingSince == nil {
		t.Fatalf("a's key = %+v, want it kept and marked", k)
	}
	if st := f.status(t); st.Pending != 0 || st.Failed != 0 {
		t.Errorf("status = %+v, want nothing left pending or failed", st)
	}
	if n := auditCount(t, f, database.AuditKeyMissing); n != 1 {
		t.Errorf("key.missing audit entries = %d, want 1", n)
	}

	// The next run does not push to the marked key, and does not mark it again.
	f.setTPM(t, 500)
	res, err = New(f.store, f.gw, plainUser, 1).Run(f.ctx)
	if err != nil {
		t.Fatal(err)
	}
	if res.Marked != 0 || res.Removed != 0 {
		t.Errorf("second run = %+v, want the marked key left alone within the grace period", res)
	}
	if n := f.gw.pushCount(gone); n != 1 {
		t.Errorf("pushes to the marked key = %d, want 1", n)
	}
	if n := f.gw.pushCount(kept); n != 2 {
		t.Errorf("pushes to the kept key = %d, want 2", n)
	}
	if got := metricstest.Counter(t, "keyosk_keys_marked_missing_total", `source="sync"`) - before; got != 1 {
		t.Errorf("keys_marked_missing{source=sync} rose by %v over two runs, want 1", got)
	}
}

// A key that stays missing within the grace period is checked and kept.
func TestRunKeepsAMarkedKeyWithinTheGracePeriod(t *testing.T) {
	f := newFixture(t)
	gone := f.addKey(t, "a")
	f.gw.missing[gone] = true
	s := New(f.store, f.gw, plainUser, 1).WithMissingGrace(time.Hour)
	if _, err := s.Run(f.ctx); err != nil {
		t.Fatal(err)
	}
	f.ageMissing(t, 30*time.Minute)

	res, err := s.Run(f.ctx)
	if err != nil {
		t.Fatal(err)
	}
	if res.Removed != 0 || res.Found != 0 {
		t.Errorf("result = %+v, want the key left alone", res)
	}
	if f.keyOf(t, "a") == nil {
		t.Error("key removed within the grace period")
	}
	if len(f.gw.checks) != 1 {
		t.Errorf("checks = %v, want the marked key asked about once", f.gw.checks)
	}
}

// Past the grace period a key that is still gone is removed, with an audit
// entry, and it is counted as the damage done.
func TestRunRemovesAKeyMissingPastTheGracePeriod(t *testing.T) {
	f := newFixture(t)
	gone := f.addKey(t, "a")
	f.gw.missing[gone] = true
	s := New(f.store, f.gw, plainUser, 1).WithMissingGrace(time.Hour)
	if _, err := s.Run(f.ctx); err != nil {
		t.Fatal(err)
	}
	f.ageMissing(t, 2*time.Hour)
	removedBefore := metricstest.Counter(t, "keyosk_keys_missing_removed_total", "")

	res, err := s.Run(f.ctx)
	if err != nil {
		t.Fatal(err)
	}
	if res.Removed != 1 {
		t.Errorf("result = %+v, want one removed", res)
	}
	if f.keyOf(t, "a") != nil {
		t.Error("key still stored past the grace period")
	}
	if got := metricstest.Counter(t, "keyosk_keys_missing_removed_total", "") - removedBefore; got != 1 {
		t.Errorf("keys_missing_removed_total rose by %v, want 1", got)
	}
	if got := metricstest.Gauge(t, "keyosk_keys_missing", ""); got != 0 {
		t.Errorf("keys_missing gauge = %v after the removal, want 0", got)
	}
	if n := auditCount(t, f, database.AuditKeyGone); n != 1 {
		t.Errorf("key.gone audit entries = %d, want 1", n)
	}
}

// A key that comes back is unmarked and pushed in the same run, so changes it
// missed while it was gone reach it.
func TestRunUnmarksAKeyThatReturns(t *testing.T) {
	f := newFixture(t)
	ref := f.addKey(t, "a")
	f.gw.missing[ref] = true
	s := New(f.store, f.gw, plainUser, 1)
	if _, err := s.Run(f.ctx); err != nil {
		t.Fatal(err)
	}
	f.setTPM(t, 777) // changes while the key is gone
	f.gw.setMissing(ref, false)

	res, err := s.Run(f.ctx)
	if err != nil {
		t.Fatal(err)
	}
	if res.Found != 1 || res.Synced != 1 || res.Removed != 0 {
		t.Errorf("result = %+v, want the key found and synced", res)
	}
	if k := f.keyOf(t, "a"); k == nil || k.MissingSince != nil {
		t.Errorf("key = %+v, want it kept and unmarked", k)
	}
	if l := f.gw.limits[ref]; l.TokensPerMinute == nil || *l.TokensPerMinute != 777 {
		t.Errorf("limits after the return = %+v, want the profile as changed meanwhile", l)
	}
	if n := auditCount(t, f, database.AuditKeyFound); n != 1 {
		t.Errorf("key.found audit entries = %d, want 1", n)
	}
	if got := metricstest.Gauge(t, "keyosk_keys_missing", ""); got != 0 {
		t.Errorf("keys_missing gauge = %v, want 0", got)
	}
}

// When the gateway cannot say whether it has the key, nothing happens: not a
// removal, even past the grace period, and the clock does not restart.
func TestRunLeavesAMarkedKeyAloneWhenTheCheckFails(t *testing.T) {
	f := newFixture(t)
	ref := f.addKey(t, "a")
	f.gw.missing[ref] = true
	s := New(f.store, f.gw, plainUser, 1).WithMissingGrace(time.Hour)
	if _, err := s.Run(f.ctx); err != nil {
		t.Fatal(err)
	}
	f.ageMissing(t, 2*time.Hour)
	since := f.keyOf(t, "a").MissingSince
	f.gw.checkFailing[ref] = true

	res, err := s.Run(f.ctx)
	if err != nil {
		t.Fatal(err)
	}
	if res.Removed != 0 || res.Found != 0 || res.Failed != 0 {
		t.Errorf("result = %+v, want nothing done", res)
	}
	k := f.keyOf(t, "a")
	if k == nil || k.MissingSince == nil || !k.MissingSince.Equal(*since) {
		t.Errorf("key = %+v, want it kept with its first sighting %v", k, since)
	}
}

// An expired marked key is checked but never pushed to: a push would write the
// owner's allowance upstream (issue #82).
func TestRunChecksAnExpiredMarkedKeyWithoutPushing(t *testing.T) {
	f := newFixture(t)
	ref := f.addKey(t, "a")
	f.gw.missing[ref] = true
	s := New(f.store, f.gw, plainUser, 1)
	if _, err := s.Run(f.ctx); err != nil {
		t.Fatal(err)
	}
	k := f.keyOf(t, "a")
	if err := f.store.UpdateAPIKeyExpiry(f.ctx, k.ID, time.Now().Add(-time.Hour)); err != nil {
		t.Fatal(err)
	}
	f.gw.setMissing(ref, false)
	pushes := f.gw.pushCount(ref)

	res, err := s.Run(f.ctx)
	if err != nil {
		t.Fatal(err)
	}
	if res.Found != 1 {
		t.Errorf("result = %+v, want the expired key found", res)
	}
	if n := f.gw.pushCount(ref); n != pushes {
		t.Errorf("pushes to the expired key rose from %d to %d, want none", pushes, n)
	}
}
