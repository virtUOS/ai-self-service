package oidc

import (
	"context"
	"database/sql"
	"slices"
	"strings"
	"testing"

	"github.com/uptrace/bun"
	"github.com/uptrace/bun/dialect/sqlitedialect"
	"github.com/uptrace/bun/driver/sqliteshim"

	"github.com/virtuos/keyosk/internal/config"
	"github.com/virtuos/keyosk/internal/database"
)

// newFlagProvider gives a provider backed by a migrated in-memory store, with
// admin rights coming from group if it is set.
func newFlagProvider(t *testing.T, name, group string) (*Provider, *database.Store) {
	t.Helper()
	sqldb, err := sql.Open(sqliteshim.ShimName, "file:"+name+"?mode=memory&cache=shared")
	if err != nil {
		t.Fatal(err)
	}
	db := bun.NewDB(sqldb, sqlitedialect.New())
	t.Cleanup(func() { db.Close() })
	store := database.NewStore(db)
	if err := store.RunMigrations(context.Background()); err != nil {
		t.Fatal(err)
	}
	cfg := &config.Config{AdminGroup: group, AdminGroupClaim: "groups"}
	return &Provider{cfg: cfg, store: store}, store
}

func login(t *testing.T, p *Provider, isAdmin *bool) *database.User {
	t.Helper()
	u, err := p.GetOrCreateUser(context.Background(),
		&UserInfo{Subject: "sub-1", Email: "a@uni-osnabrueck.de", Name: "A", IsAdmin: isAdmin})
	if err != nil {
		t.Fatal(err)
	}
	return u
}

// Each login sets the flag to what the token says: joining the group makes
// the account an admin, leaving it takes that away at the next login.
func TestLoginFollowsTheAdminGroup(t *testing.T) {
	p, store := newFlagProvider(t, "flag1", "ai-admin")

	if u := login(t, p, ptr(true)); !u.IsAdmin {
		t.Fatal("a member of the group is not an admin after logging in")
	}
	if u := login(t, p, ptr(false)); u.IsAdmin {
		t.Error("the returned account is still an admin after leaving the group")
	}
	// Logging in again without a change records nothing more.
	login(t, p, ptr(false))

	events, err := store.ListAuditEvents(context.Background(), 10)
	if err != nil {
		t.Fatal(err)
	}
	var actions []string
	for _, e := range events {
		actions = append(actions, e.Action)
	}
	if !slices.Equal(actions, []string{database.AuditAdminRevoked, database.AuditAdminGranted}) {
		t.Errorf("audit actions = %v, want a grant then a revoke", actions)
	}

	admins, err := store.ListAdmins(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if len(admins) != 0 {
		t.Errorf("admins = %+v after leaving the group, want none", admins)
	}
}

// An admin whose token carries no group claim at all is still demoted, since
// a token that says nothing grants nothing, but the audit row must say that
// the claim was absent rather than that they left the group: the usual cause
// is a missing mapper in the IdP, and "left group" would send whoever reads
// the log to the group's membership instead.
func TestLoginWithoutTheClaimSaysSoInTheAudit(t *testing.T) {
	p, store := newFlagProvider(t, "flag3", "ai-admin")
	login(t, p, ptr(true))

	u, err := p.GetOrCreateUser(context.Background(),
		&UserInfo{Subject: "sub-1", Email: "a@uni-osnabrueck.de", Name: "A", IsAdmin: ptr(false), AdminClaimAbsent: true})
	if err != nil {
		t.Fatal(err)
	}
	if u.IsAdmin {
		t.Error("an account stayed admin on a token without the claim")
	}

	events, err := store.ListAuditEvents(context.Background(), 10)
	if err != nil {
		t.Fatal(err)
	}
	if len(events) == 0 || events[0].Action != database.AuditAdminRevoked {
		t.Fatalf("audit = %+v, want a revoke first", events)
	}
	detail := events[0].Detail
	if !strings.Contains(detail, "claim groups absent") || strings.Contains(detail, "left group") {
		t.Errorf("detail = %q, want it to say the groups claim was absent, not that they left the group", detail)
	}
}

// When the panel manages admin rights, a login leaves the flag alone, so a
// promotion made there survives the person's next login.
func TestLoginLeavesThePanelsFlagAlone(t *testing.T) {
	p, store := newFlagProvider(t, "flag2", "")
	u := login(t, p, nil)
	if err := store.SetUserAdmin(context.Background(), u.ID, true); err != nil {
		t.Fatal(err)
	}
	if u := login(t, p, nil); !u.IsAdmin {
		t.Error("a login cleared the flag the panel set")
	}
}
