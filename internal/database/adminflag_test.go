package database

import (
	"context"
	"testing"
)

// Admin rights live on the account. Nobody the portal has seen is an admin
// until another admin, or the operator's command, sets the flag.
func TestUserAdminFlagRoundTrips(t *testing.T) {
	s := migratedStore(t, "adminflag1")
	ctx := context.Background()

	u, err := s.GetOrCreateUser(ctx, "sub-1", "p@uni-osnabrueck.de", "P")
	if err != nil {
		t.Fatal(err)
	}
	if u.IsAdmin {
		t.Fatal("a new account is an admin")
	}

	if err := s.SetUserAdmin(ctx, u.ID, true); err != nil {
		t.Fatal(err)
	}
	got, err := s.GetUserByID(ctx, u.ID)
	if err != nil {
		t.Fatal(err)
	}
	if !got.IsAdmin {
		t.Error("the flag did not persist")
	}

	if err := s.SetUserAdmin(ctx, u.ID, false); err != nil {
		t.Fatal(err)
	}
	if got, _ = s.GetUserByID(ctx, u.ID); got.IsAdmin {
		t.Error("the flag was not cleared")
	}
}

// Logging in again updates the name and address from the IdP and nothing
// else, so it must not clear the flag.
func TestLoginKeepsTheAdminFlag(t *testing.T) {
	s := migratedStore(t, "adminflag2")
	ctx := context.Background()

	u, err := s.GetOrCreateUser(ctx, "sub-1", "p@uni-osnabrueck.de", "P")
	if err != nil {
		t.Fatal(err)
	}
	if err := s.SetUserAdmin(ctx, u.ID, true); err != nil {
		t.Fatal(err)
	}

	again, err := s.GetOrCreateUser(ctx, "sub-1", "new@uni-osnabrueck.de", "P Renamed")
	if err != nil {
		t.Fatal(err)
	}
	if !again.IsAdmin {
		t.Error("a login cleared the admin flag")
	}
	if again.Email != "new@uni-osnabrueck.de" {
		t.Errorf("email = %q, want it updated from the IdP", again.Email)
	}
}

// ListAdmins is the Admins tab, oldest account first; CountAdmins is the
// startup check that warns when nobody can reach the panel.
func TestListAndCountAdmins(t *testing.T) {
	s := migratedStore(t, "adminflag3")
	ctx := context.Background()

	n, err := s.CountAdmins(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if n != 0 {
		t.Fatalf("CountAdmins = %d on an empty store, want 0", n)
	}

	var ids []int64
	for _, sub := range []string{"a", "b", "c"} {
		u, err := s.GetOrCreateUser(ctx, "sub-"+sub, sub+"@uni-osnabrueck.de", sub)
		if err != nil {
			t.Fatal(err)
		}
		ids = append(ids, u.ID)
	}
	for _, id := range []int64{ids[2], ids[0]} {
		if err := s.SetUserAdmin(ctx, id, true); err != nil {
			t.Fatal(err)
		}
	}

	admins, err := s.ListAdmins(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if len(admins) != 2 || admins[0].ID != ids[0] || admins[1].ID != ids[2] {
		t.Errorf("ListAdmins = %+v, want accounts a then c", admins)
	}
	if n, _ = s.CountAdmins(ctx); n != 2 {
		t.Errorf("CountAdmins = %d, want 2", n)
	}
}
