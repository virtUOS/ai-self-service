package handlers

import (
	"context"
	"log/slog"
	"strings"

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

// adminRow is one line of the Admins tab.
type adminRow struct {
	Email     string
	Source    string // "role", "config" or "granted"
	Removable bool
	IsSelf    bool
}

// adminRows lists every admin the panel can show, marking which ones it may
// remove.
//
// Entries from ADMIN_IDS are shown but never removable: the list outranks the
// table, so a remove button on one would silently do nothing. The acting
// admin's own row is not removable either — see Admin.RevokeAdmin.
//
// ADMIN_ROLE cannot be enumerated: role membership lives in the IdP and this
// process only ever sees the token of whoever is currently signed in. The
// template says so rather than implying the list is complete.
func adminRows(cfg *config.Config, grants []database.AdminGrant, actor string) []adminRow {
	rows := make([]adminRow, 0, len(cfg.AdminIDs)+len(grants))
	for _, id := range cfg.AdminIDs {
		rows = append(rows, adminRow{
			Email:  id,
			Source: "config",
			IsSelf: strings.EqualFold(id, actor),
		})
	}
	for _, g := range grants {
		rows = append(rows, adminRow{
			Email:     g.Email,
			Source:    "granted",
			Removable: !strings.EqualFold(g.Email, actor),
			IsSelf:    strings.EqualFold(g.Email, actor),
		})
	}
	return rows
}
