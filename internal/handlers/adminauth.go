package handlers

import (
	"context"
	"log/slog"

	"github.com/virtuos/ai-self-service/internal/config"
	"github.com/virtuos/ai-self-service/internal/database"
	oidcpkg "github.com/virtuos/ai-self-service/internal/oidc"
)

// adminSource is where a user's admin rights come from. The zero value is no
// rights, so a resolver that fails closed denies.
type adminSource int

const (
	adminSourceNone adminSource = iota
	adminSourceRole
	adminSourceEnv
	adminSourceGrant
)

func (s adminSource) isAdmin() bool { return s != adminSourceNone }

// resolveAdmin decides whether a user holds admin rights and on what basis.
//
// This is the only place the precedence lives. The admin middleware and the
// dashboard's nav link both call it: deriving the answer separately is how the
// panel and the link that leads to it drift apart.
//
// Order is role, then the configured list, then the grants table. The role
// wins so that a deployment managing admins in the IdP is not second-guessed
// by a row here, and the list outranks the table so an entry an operator put
// in the deployment cannot be removed by a click.
//
// The second return is whether an env or table match was made on the subject
// rather than the address; callers use it to warn about the weaker form.
func resolveAdmin(ctx context.Context, cfg *config.Config, store *database.Store,
	idToken, sub, email string) (adminSource, bool) {

	if cfg.HasAdminRole(oidcpkg.RealmRoles(idToken)) {
		return adminSourceRole, true
	}
	if admin, bySubject := cfg.IsAdmin(sub, email); admin {
		return adminSourceEnv, bySubject
	}
	granted, err := store.IsAdminGranted(ctx, sub, email)
	if err != nil {
		// Fail closed: a database blip must not hand out the panel.
		slog.Error("check admin grants", "email", email, "err", err)
		return adminSourceNone, false
	}
	if granted {
		return adminSourceGrant, sub != ""
	}
	return adminSourceNone, false
}
