package database

import (
	"context"
	"testing"
)

// The table must exist after migrations and hold a grant that has no subject
// yet: an admin can be named before they have ever logged in.
func TestAdminGrantsTableAcceptsAGrantWithoutASubject(t *testing.T) {
	store := migratedStore(t, "admingrants1")
	ctx := context.Background()

	g := &AdminGrant{Email: "new.admin@uni-osnabrueck.de", GrantedByEmail: "boss@uni-osnabrueck.de"}
	if _, err := store.db.NewInsert().Model(g).Exec(ctx); err != nil {
		t.Fatalf("insert grant without subject: %v", err)
	}
	if g.ID == 0 {
		t.Error("insert did not assign an id")
	}

	var got AdminGrant
	if err := store.db.NewSelect().Model(&got).Where("email = ?", g.Email).Scan(ctx); err != nil {
		t.Fatalf("read grant back: %v", err)
	}
	if got.OIDCSub != nil {
		t.Errorf("OIDCSub = %v, want nil for a grant made before first login", *got.OIDCSub)
	}
	if got.GrantedByEmail != "boss@uni-osnabrueck.de" {
		t.Errorf("GrantedByEmail = %q, want the granting admin", got.GrantedByEmail)
	}
}

// Email is the identity of a grant: granting the same address twice must not
// create a second row.
func TestAdminGrantsEmailIsUnique(t *testing.T) {
	store := migratedStore(t, "admingrants2")
	ctx := context.Background()

	first := &AdminGrant{Email: "dup@uni-osnabrueck.de", GrantedByEmail: "boss@uni-osnabrueck.de"}
	if _, err := store.db.NewInsert().Model(first).Exec(ctx); err != nil {
		t.Fatalf("insert first grant: %v", err)
	}
	second := &AdminGrant{Email: "dup@uni-osnabrueck.de", GrantedByEmail: "boss@uni-osnabrueck.de"}
	if _, err := store.db.NewInsert().Model(second).Exec(ctx); err == nil {
		t.Error("inserting a duplicate email succeeded, want a unique-constraint error")
	}
}
