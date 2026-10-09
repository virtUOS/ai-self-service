// Package profileexpiry reverts profile assignments whose deadline has passed.
//
// It lives beside the portal rather than inside a request handler because the
// whole point is that it happens without the user doing anything: a user who
// stops visiting the dashboard would otherwise keep elevated limits on a live
// key indefinitely.
//
// The job only changes the assignment in the database. Moving a user to
// another profile makes their key out of date, and the limit sync pushes the
// new limits, the same way it does for a change an admin makes.
package profileexpiry

import (
	"context"
	"fmt"
	"log/slog"
	"time"

	"github.com/virtuos/ai-self-service/internal/database"
)

// gateway is the slice of the key provider this job needs: deleting a key,
// for assignments that end that way.
type gateway interface {
	DeleteKey(ctx context.Context, ref string) error
}

// Runner reverts expired profile assignments.
type Runner struct {
	store *database.Store
	keys  gateway
	// kick starts a limit sync run, so a reverted user's key gets its new
	// limits right away rather than at the sync's next interval.
	kick func()
}

// NewRunner returns a Runner. kick may be nil, in which case the limit sync
// picks reverted keys up on its own interval.
func NewRunner(store *database.Store, keys gateway, kick func()) *Runner {
	return &Runner{store: store, keys: keys, kick: kick}
}

// Run reverts every assignment whose deadline has passed.
//
// Safe to call repeatedly: applying an expiry clears the deadline, so a second
// run finds nothing. One user's failure does not stop the others — a gateway
// blip must not leave the rest of the batch un-reverted.
func (r *Runner) Run(ctx context.Context) error {
	users, err := r.store.UsersWithPassedDeadline(ctx, time.Now())
	if err != nil {
		return fmt.Errorf("find expired assignments: %w", err)
	}
	switched := false
	for i := range users {
		u := &users[i]
		var err error
		if u.RevokeKeyAtExpiry {
			err = r.revoke(ctx, u)
		} else {
			err = r.switchProfile(ctx, u)
			switched = switched || err == nil
		}
		if err != nil {
			slog.Error("revert expired profile", "user_id", u.ID, "err", err)
		}
	}
	if switched && r.kick != nil {
		r.kick()
	}
	return nil
}

// switchProfile moves a user to their post-deadline profile. The limit sync
// then finds their key out of date and pushes the new limits; the deadline can
// be cleared at once, because the pending state is recorded in the database
// and survives a failed push or a restart.
func (r *Runner) switchProfile(ctx context.Context, u *database.User) error {
	if err := r.store.ApplyProfileExpiry(ctx, u.ID, u.ProfileAfterExpiry); err != nil {
		return fmt.Errorf("apply expiry: %w", err)
	}
	r.audit(ctx, u, r.destName(ctx, u.ProfileAfterExpiry))
	return nil
}

// revoke deletes the key instead of switching profiles.
func (r *Runner) revoke(ctx context.Context, u *database.User) error {
	key, err := r.store.GetAPIKeyByUser(ctx, u.ID)
	if err != nil {
		return fmt.Errorf("load key: %w", err)
	}
	if key != nil {
		// Upstream first: if that fails the key is still live, so the local row
		// must stay to keep it revocable.
		if err := r.keys.DeleteKey(ctx, key.LiteLLMKey); err != nil {
			return fmt.Errorf("delete key upstream: %w", err)
		}
		if err := r.store.DeleteAPIKey(ctx, key.ID); err != nil {
			return fmt.Errorf("delete local key row: %w", err)
		}
	}
	if err := r.store.ApplyProfileExpiry(ctx, u.ID, u.ProfileAfterExpiry); err != nil {
		return fmt.Errorf("apply expiry: %w", err)
	}
	r.audit(ctx, u, "key deleted")
	return nil
}

func (r *Runner) audit(ctx context.Context, u *database.User, detail string) {
	if err := r.store.RecordAudit(ctx, &database.AuditEvent{
		Action:       database.AuditProfileExpired,
		ActorEmail:   "system", // no admin pressed anything; the deadline fired
		SubjectEmail: u.Email,
		SubjectID:    &u.ID,
		Detail:       detail,
	}); err != nil {
		slog.Error("record profile expiry", "user_id", u.ID, "err", err)
	}
}

// destName names the profile a user moved to, for the audit log. A null
// destination means the default profile, resolved by the limit sync when it
// pushes rather than here.
func (r *Runner) destName(ctx context.Context, id *int64) string {
	if id == nil {
		return "default"
	}
	p, err := r.store.GetProfile(ctx, *id)
	if err != nil {
		return fmt.Sprintf("profile #%d", *id)
	}
	return p.Name
}

// Start runs the job on an interval until ctx is cancelled.
func (r *Runner) Start(ctx context.Context, every time.Duration) {
	// Run once at startup so a restart does not delay an overdue reversion.
	if err := r.Run(ctx); err != nil {
		slog.Error("profile expiry run", "err", err)
	}
	t := time.NewTicker(every)
	defer t.Stop()
	for {
		select {
		case <-t.C:
			if err := r.Run(ctx); err != nil {
				slog.Error("profile expiry run", "err", err)
			}
		case <-ctx.Done():
			return
		}
	}
}
