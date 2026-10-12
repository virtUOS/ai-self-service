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
package limitsync

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"sync"
	"time"

	"github.com/virtuos/ai-self-service/internal/database"
	"github.com/virtuos/ai-self-service/internal/keyprovider"
)

// gateway is the slice of the key provider the sync needs.
type gateway interface {
	UpdateLimits(ctx context.Context, ref string, user keyprovider.User, limits keyprovider.Limits) error
	UpdateAlias(ctx context.Context, ref, alias string) error
}

// maxErrorLen caps the stored failure reason. Gateway errors carry the
// response body, which can be long; the admin panel needs the gist.
const maxErrorLen = 500

// Syncer pushes profile limits to keys that are out of date.
type Syncer struct {
	store   *database.Store
	keys    gateway
	user    func(sub, name, email string) keyprovider.User
	workers int
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
		kick: make(chan struct{}, 1)}
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
	// Removed counts keys the gateway no longer has, whose records the run
	// dropped (see database.Store.RemoveMissingKey).
	Removed int
}

// outcome is what became of one key in a run.
type outcome int

const (
	synced outcome = iota
	failed
	removed
)

// Run pushes limits to every out-of-date key once.
//
// A key that fails is recorded and left for the next run. A key that goes out
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

	start := time.Now()
	batchSize := max(50, s.workers*10)
	failedThisRun := map[int64]bool{}
	var res Result

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
			case removed:
				res.Removed++
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
		firstErr error
		wg       sync.WaitGroup
		slots    = make(chan struct{}, s.workers)
	)
	for _, t := range batch {
		if ctx.Err() != nil {
			break
		}
		slots <- struct{}{}
		wg.Add(1)
		go func(t database.LimitSyncTarget) {
			defer func() { <-slots; wg.Done() }()
			o, attempted, err := s.syncOne(ctx, t)
			mu.Lock()
			defer mu.Unlock()
			if attempted {
				outcomes[t.KeyID] = o
			}
			if err != nil && firstErr == nil {
				firstErr = err
			}
		}(t)
	}
	wg.Wait()
	return outcomes, firstErr
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
// A key the gateway no longer has was deleted outside the portal. No retry can
// bring it back, and the portal cannot recreate it, so its record is dropped
// instead of failing forever (issue #82); the owner can generate a new key.
// Anything else is recorded as a failure and retried on the next run.
func (s *Syncer) pushFailed(ctx context.Context, t database.LimitSyncTarget, err error) (outcome, bool, error) {
	if ctx.Err() != nil {
		return failed, false, nil
	}
	if !errors.Is(err, keyprovider.ErrKeyNotFound) {
		return failed, true, s.fail(ctx, t, err.Error())
	}
	slog.Warn("key no longer exists in the gateway; removing it", "key_prefix", t.KeyPrefix, "user", t.Email, "err", err)
	if _, rerr := s.store.RemoveMissingKey(ctx, t.KeyID, t.UserID, t.Email,
		"key "+t.KeyPrefix+" no longer exists in the gateway (limit sync)"); rerr != nil {
		if ctx.Err() != nil {
			return failed, false, nil
		}
		return failed, true, fmt.Errorf("remove missing key %s: %w", t.KeyPrefix, rerr)
	}
	return removed, true, nil
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
		slog.Error("limit sync run", "err", err, "synced", res.Synced, "failed", res.Failed, "removed", res.Removed)
		return
	}
	if res.Synced > 0 || res.Failed > 0 || res.Removed > 0 {
		slog.Info("limit sync run", "synced", res.Synced, "failed", res.Failed, "removed", res.Removed)
	}
}
