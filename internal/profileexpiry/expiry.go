// Package profileexpiry reverts profile assignments whose deadline has passed.
//
// It lives beside the portal rather than inside a request handler because the
// whole point is that it happens without the user doing anything: a user who
// stops visiting the dashboard would otherwise keep elevated limits on a live
// key indefinitely.
package profileexpiry

import (
	"context"
	"fmt"
	"log/slog"
	"time"

	"github.com/virtuos/ai-self-service/internal/database"
	"github.com/virtuos/ai-self-service/internal/keyprovider"
)

// gateway is the slice of the key provider this job needs. Narrow on purpose:
// it keeps the test fake to two methods.
type gateway interface {
	UpdateLimits(ctx context.Context, ref, ownerID string, limits keyprovider.Limits) error
	DeleteKey(ctx context.Context, ref string) error
}

// Runner reverts expired profile assignments.
type Runner struct {
	store *database.Store
	keys  gateway
}

func NewRunner(store *database.Store, keys gateway) *Runner {
	return &Runner{store: store, keys: keys}
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
	for i := range users {
		if err := r.expire(ctx, &users[i]); err != nil {
			slog.Error("revert expired profile", "user_id", users[i].ID, "err", err)
		}
	}
	return nil
}

// expire reverts one user.
func (r *Runner) expire(ctx context.Context, u *database.User) error {
	key, err := r.store.GetAPIKeyByUser(ctx, u.ID)
	if err != nil {
		return fmt.Errorf("load key: %w", err)
	}

	if u.RevokeKeyAtExpiry {
		return r.revoke(ctx, u, key)
	}

	// Resolve the destination now rather than when the deadline was set: a null
	// destination means "the default profile", whatever that is today.
	var dest *database.Profile
	if u.ProfileAfterExpiry != nil {
		dest, err = r.store.GetProfile(ctx, *u.ProfileAfterExpiry)
		if err != nil {
			return fmt.Errorf("load destination profile: %w", err)
		}
	} else {
		dest, err = r.store.GetDefaultProfile(ctx)
		if err != nil {
			return fmt.Errorf("load default profile: %w", err)
		}
	}

	// Push first, clear the deadline second. The reverse would leave a user
	// with no deadline and a key still carrying the elevated limits, which no
	// later run would ever correct.
	if key != nil {
		if err := r.keys.UpdateLimits(ctx, key.LiteLLMKey, u.OIDCSub, dest.Limits()); err != nil {
			return fmt.Errorf("push reverted limits: %w", err)
		}
	}

	if err := r.store.ApplyProfileExpiry(ctx, u.ID, u.ProfileAfterExpiry); err != nil {
		return fmt.Errorf("apply expiry: %w", err)
	}
	r.audit(ctx, u, destName(dest))
	return nil
}

// revoke deletes the key instead of switching profiles.
func (r *Runner) revoke(ctx context.Context, u *database.User, key *database.APIKey) error {
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

func destName(p *database.Profile) string {
	if p == nil {
		return "default"
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
