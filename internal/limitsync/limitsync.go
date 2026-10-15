// Package limitsync brings the limits of issued keys in line with their
// owners' profiles.
//
// An admin change can affect thousands of keys, and each push is several
// gateway calls, so it cannot happen inside the admin's request. The database
// records which keys are out of date (see database.PendingLimitSyncs). The
// Syncer works through them in the background: right away when Kick is
// called after a change, and on an interval to retry what failed.
//
// There is no queue of changes. A key is pushed whatever its owner's profile
// says at the moment it is pushed, so when an admin saves a profile twice in
// quick succession, keys not reached yet only ever get the second version.
//
// The sync also looks after keys the gateway no longer has. A key whose push
// is answered "not found" is marked, not dropped: a gateway that was wrong or
// restored would otherwise make the portal forget every key at once. Each run
// checks the marked keys first. One that is back is unmarked and pushed like
// any out-of-date key; one that stays missing past the grace period is removed
// (issue #108).
package limitsync

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"sync"
	"time"

	"github.com/virtuos/keyosk/internal/database"
	"github.com/virtuos/keyosk/internal/keyprovider"
	"github.com/virtuos/keyosk/internal/metrics"
)

// gateway is the slice of the key provider the sync needs.
type gateway interface {
	UpdateLimits(ctx context.Context, ref string, user keyprovider.User, limits keyprovider.Limits) error
	UpdateAlias(ctx context.Context, ref, alias string) error
	CheckKey(ctx context.Context, ref string) error
}

// DefaultMissingGrace is how long a key stays marked as missing before its
// record is removed, unless WithMissingGrace says otherwise.
const DefaultMissingGrace = 24 * time.Hour

// maxErrorLen caps the stored failure reason. Gateway errors carry the
// response body, which can be long; the admin panel needs the gist.
const maxErrorLen = 500

// Syncer pushes profile limits to keys that are out of date.
type Syncer struct {
	store   *database.Store
	keys    gateway
	user    func(sub, name, email string) keyprovider.User
	workers int
	grace   time.Duration
	kick    chan struct{}

	mu       sync.Mutex
	running  bool
	finished time.Time
}

// New returns a Syncer that pushes with the given number of parallel workers.
//
// user describes the gateway user that holds a key owner's allowance, given
// their OIDC subject, name and email (config.Config.LiteLLMUser). Every push
// binds the key to that user and labels it, so the key, the dashboard and the
// sync always agree on it.
func New(store *database.Store, keys gateway, user func(sub, name, email string) keyprovider.User, workers int) *Syncer {
	if workers < 1 {
		workers = 1
	}
	return &Syncer{store: store, keys: keys, user: user, workers: workers,
		grace: DefaultMissingGrace, kick: make(chan struct{}, 1)}
}

// WithMissingGrace sets how long a key the gateway no longer has stays marked
// before the sync removes its record. It returns s, to chain onto New.
func (s *Syncer) WithMissingGrace(d time.Duration) *Syncer {
	s.grace = d
	return s
}

// Kick asks for a run as soon as possible. It never blocks: kicks that arrive
// while one is already waiting collapse into it, and a run already under way
// picks up keys that go out of date while it works.
func (s *Syncer) Kick() {
	select {
	case s.kick <- struct{}{}:
	default:
	}
}

// Status reports whether a run is in progress and when the last one finished.
// The finish time is zero until the first run completes.
func (s *Syncer) Status() (running bool, lastFinished time.Time) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.running, s.finished
}

// Result counts what a run did.
type Result struct {
	Synced int
	Failed int
	// Marked counts keys the gateway answered "not found" for, which the run
	// marked as missing (see database.Store.MarkKeyMissing).
	Marked int
	// Found counts marked keys the gateway has again, which the run unmarked.
	Found int
	// Removed counts marked keys that stayed missing past the grace period,
	// whose records the run dropped (see database.Store.RemoveMissingKey).
	Removed int
}

// outcome is what became of one key in a run.
type outcome int

const (
	synced outcome = iota
	failed
	marked
)

// Run pushes limits to every out-of-date key once.
//
// Keys marked as missing are checked first, so one that has returned is
// pushed in the same run. A key that fails is recorded and left for the next
// run. A key that goes out
// of date while the run is under way, because an admin saved again, is picked
// up before the run ends. Run returns when nothing is left that it has not
// tried, or when ctx is cancelled.
func (s *Syncer) Run(ctx context.Context) (Result, error) {
	s.mu.Lock()
	s.running = true
	s.mu.Unlock()
	defer func() {
		s.mu.Lock()
		s.running = false
		s.finished = time.Now()
		s.mu.Unlock()
	}()

	var res Result
	var err error
	if res.Found, res.Removed, err = s.checkMissing(ctx); err != nil {
		return res, err
	}

	start := time.Now()
	batchSize := max(50, s.workers*10)
	failedThisRun := map[int64]bool{}

	for ctx.Err() == nil {
		batch, err := s.store.PendingLimitSyncs(ctx, start, batchSize)
		if err != nil {
			return res, fmt.Errorf("find out-of-date keys: %w", err)
		}
		// The database filter already skips keys that failed in this run.
		// The in-memory set is a guard against the clock stepping backwards,
		// which would otherwise hand the same failing keys back forever.
		fresh := batch[:0]
		for _, t := range batch {
			if !failedThisRun[t.KeyID] {
				fresh = append(fresh, t)
			}
		}
		if len(fresh) == 0 {
			break
		}

		outcomes, err := s.syncBatch(ctx, fresh)
		for id, o := range outcomes {
			switch o {
			case synced:
				res.Synced++
			case marked:
				res.Marked++
			default:
				res.Failed++
				failedThisRun[id] = true
			}
		}
		if err != nil {
			return res, err
		}
	}
	return res, ctx.Err()
}

// syncBatch pushes a batch with up to s.workers pushes in flight. It returns
// the outcome of each attempted key. Keys skipped because ctx was cancelled
// are left out.
//
// An error means the outcome of a push could not be recorded. The run stops
// then: a key whose result was not written would come back as pending at
// once, and the run would push it again and again.
func (s *Syncer) syncBatch(ctx context.Context, batch []database.LimitSyncTarget) (map[int64]outcome, error) {
	var (
		mu       sync.Mutex
		outcomes = make(map[int64]outcome, len(batch))
	)
	err := forEach(ctx, s.workers, batch, func(t database.LimitSyncTarget) error {
		o, attempted, err := s.syncOne(ctx, t)
		if attempted {
			mu.Lock()
			outcomes[t.KeyID] = o
			mu.Unlock()
		}
		return err
	})
	return outcomes, err
}

// forEach calls do for each item, with up to workers calls in flight. It
// starts no further calls once ctx is cancelled, waits for those under way,
// and returns the first error any of them returned.
func forEach[T any](ctx context.Context, workers int, items []T, do func(T) error) error {
	var (
		mu       sync.Mutex
		firstErr error
		wg       sync.WaitGroup
		slots    = make(chan struct{}, workers)
	)
	for _, item := range items {
		if ctx.Err() != nil {
			break
		}
		slots <- struct{}{}
		wg.Add(1)
		go func() {
			defer func() { <-slots; wg.Done() }()
			if err := do(item); err != nil {
				mu.Lock()
				if firstErr == nil {
					firstErr = err
				}
				mu.Unlock()
			}
		}()
	}
	wg.Wait()
	return firstErr
}

// syncOne pushes one key's limits and records the outcome. attempted is false
// when ctx was cancelled mid-push: the key stays pending without a failure,
// since nothing about it went wrong.
func (s *Syncer) syncOne(ctx context.Context, t database.LimitSyncTarget) (o outcome, attempted bool, err error) {
	if t.ProfileID == nil {
		// Never push empty limits: to the gateway they mean "unlimited".
		return failed, true, s.fail(ctx, t, "the user has no profile and there is no default profile")
	}
	p, err := s.store.GetProfile(ctx, *t.ProfileID)
	if err != nil {
		if ctx.Err() != nil {
			return failed, false, nil
		}
		return failed, true, s.fail(ctx, t, fmt.Sprintf("load profile %d: %v", *t.ProfileID, err))
	}

	if err := s.keys.UpdateLimits(ctx, t.KeyRef, s.user(t.OIDCSub, t.Name, t.Email), p.Limits()); err != nil {
		return s.pushFailed(ctx, t, err)
	}

	// The alias carries the username (keyprovider.NewAlias), so a new name
	// gets a new alias. Keys from before the name was recorded get one once.
	if t.SyncedOwnerName == nil || *t.SyncedOwnerName != t.Name {
		alias, err := keyprovider.NewAlias(t.OIDCSub, t.Name)
		if err == nil {
			err = s.keys.UpdateAlias(ctx, t.KeyRef, alias)
		}
		if err != nil {
			return s.pushFailed(ctx, t, err)
		}
	}

	if err := s.store.MarkLimitsSynced(ctx, t.KeyID, p.ID, p.LimitsRev, t.Name, t.Email); err != nil {
		if ctx.Err() != nil {
			return synced, true, nil
		}
		return failed, true, fmt.Errorf("record limit sync of key %s: %w", t.KeyPrefix, err)
	}
	return synced, true, nil
}

// pushFailed settles a push the gateway did not accept.
//
// A key the gateway no longer has was deleted outside the portal, or the
// gateway lost it. No retry can bring it back and the portal cannot recreate
// it, so it is marked instead of failing forever (issue #82); the next runs
// check whether it returns, and remove it once the grace period has passed.
// Anything else is recorded as a failure and retried on the next run.
func (s *Syncer) pushFailed(ctx context.Context, t database.LimitSyncTarget, err error) (outcome, bool, error) {
	if ctx.Err() != nil {
		return failed, false, nil
	}
	if !errors.Is(err, keyprovider.ErrKeyNotFound) {
		return failed, true, s.fail(ctx, t, err.Error())
	}
	slog.Warn("key no longer exists in the gateway; marking it missing", "key_prefix", t.KeyPrefix, "user", t.Email, "err", err)
	newly, merr := s.store.MarkKeyMissing(ctx, t.KeyID, t.UserID, t.Email,
		"key "+t.KeyPrefix+" no longer exists in the gateway ("+metrics.SourceSync+")")
	if merr != nil {
		if ctx.Err() != nil {
			return failed, false, nil
		}
		return failed, true, fmt.Errorf("mark missing key %s: %w", t.KeyPrefix, merr)
	}
	// Counted only when this run marked the key: the dashboard may have
	// noticed first, and then it is its sighting.
	if newly {
		metrics.KeysMarkedMissing.WithLabelValues(metrics.SourceSync).Inc()
	}
	return marked, true, nil
}

// checkMissing looks at every key marked as missing: one the gateway has again
// is unmarked, one that is still gone past the grace period is removed, and
// anything else is left alone. It returns how many it unmarked and removed.
//
// A key whose check fails for another reason keeps its marker, and its grace
// period does not start over: it runs from the first sighting.
//
// An error means an outcome could not be recorded, and ends the run.
func (s *Syncer) checkMissing(ctx context.Context) (found, removed int, err error) {
	keys, err := s.store.ListMissingKeys(ctx)
	if err != nil {
		return 0, 0, fmt.Errorf("list missing keys: %w", err)
	}
	var mu sync.Mutex
	err = forEach(ctx, s.workers, keys, func(m database.MissingKey) error {
		o, err := s.checkOne(ctx, m)
		mu.Lock()
		defer mu.Unlock()
		switch o {
		case checkFound:
			found++
		case checkRemoved:
			removed++
		}
		return err
	})
	return found, removed, err
}

// checkResult is what became of one marked key.
type checkResult int

const (
	checkKept checkResult = iota
	checkFound
	checkRemoved
)

func (s *Syncer) checkOne(ctx context.Context, m database.MissingKey) (checkResult, error) {
	err := s.keys.CheckKey(ctx, m.KeyRef)
	switch {
	case ctx.Err() != nil:
		return checkKept, nil

	case err == nil:
		cleared, cerr := s.store.ClearKeyMissing(ctx, m.KeyID, m.UserID, m.Email,
			"key "+m.KeyPrefix+" is in the gateway again")
		if cerr != nil {
			if ctx.Err() != nil {
				return checkKept, nil
			}
			return checkKept, fmt.Errorf("unmark missing key %s: %w", m.KeyPrefix, cerr)
		}
		if !cleared {
			return checkKept, nil
		}
		slog.Info("key is in the gateway again; unmarking it", "key_prefix", m.KeyPrefix, "user", m.Email,
			"missing_since", m.MissingSince)
		return checkFound, nil

	case errors.Is(err, keyprovider.ErrKeyNotFound):
		// The statement that removes the row checks the grace period too, so
		// a key still within it is simply kept.
		dropped, rerr := s.store.RemoveMissingKey(ctx, m.KeyID, m.UserID, m.Email,
			"key "+m.KeyPrefix+" stayed missing from the gateway since "+
				m.MissingSince.Format("2006-01-02 15:04")+", past the grace period of "+s.grace.String(),
			time.Now().Add(-s.grace))
		if rerr != nil {
			if ctx.Err() != nil {
				return checkKept, nil
			}
			return checkKept, fmt.Errorf("remove missing key %s: %w", m.KeyPrefix, rerr)
		}
		if !dropped {
			return checkKept, nil
		}
		slog.Warn("key stayed missing past the grace period; removed it", "key_prefix", m.KeyPrefix, "user", m.Email,
			"missing_since", m.MissingSince, "grace", s.grace)
		metrics.KeysMissingRemoved.Inc()
		return checkRemoved, nil

	default:
		// The gateway could not say. Leave the marker as it is.
		slog.Warn("could not check a key marked missing", "key_prefix", m.KeyPrefix, "user", m.Email, "err", err)
		return checkKept, nil
	}
}

// fail records why a key could not be synced. The returned error is only for
// a failure to record it.
func (s *Syncer) fail(ctx context.Context, t database.LimitSyncTarget, reason string) error {
	if len(reason) > maxErrorLen {
		reason = reason[:maxErrorLen] + "…"
	}
	slog.Warn("limit sync failed", "key_prefix", t.KeyPrefix, "user", t.Email, "err", reason)
	if err := s.store.MarkLimitSyncFailed(ctx, t.KeyID, reason, time.Now()); err != nil {
		if ctx.Err() != nil {
			return nil
		}
		return fmt.Errorf("record limit sync failure of key %s: %w", t.KeyPrefix, err)
	}
	return nil
}

// Start runs the sync at startup, whenever Kick is called, and every interval
// to retry what failed, until ctx is cancelled.
func (s *Syncer) Start(ctx context.Context, every time.Duration) {
	t := time.NewTicker(every)
	defer t.Stop()
	for {
		s.runAndLog(ctx)
		select {
		case <-ctx.Done():
			return
		case <-t.C:
		case <-s.kick:
		}
	}
}

func (s *Syncer) runAndLog(ctx context.Context) {
	res, err := s.Run(ctx)
	if err != nil && ctx.Err() == nil {
		slog.Error("limit sync run", "err", err, "synced", res.Synced, "failed", res.Failed,
			"marked", res.Marked, "found", res.Found, "removed", res.Removed)
		return
	}
	if res.Synced > 0 || res.Failed > 0 || res.Marked > 0 || res.Found > 0 || res.Removed > 0 {
		slog.Info("limit sync run", "synced", res.Synced, "failed", res.Failed,
			"marked", res.Marked, "found", res.Found, "removed", res.Removed)
	}
}
