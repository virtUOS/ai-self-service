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

// A grant authorises by email before the subject is known, and by subject
// afterwards. The subject is the durable identifier, so once it is recorded a
// change of address must not revoke the rights.
func TestIsAdminGrantedMatchesEmailThenSubject(t *testing.T) {
	store := migratedStore(t, "admingrants3")
	ctx := context.Background()

	if err := store.GrantAdmin(ctx, "grantee@uni-osnabrueck.de", "boss@uni-osnabrueck.de"); err != nil {
		t.Fatal(err)
	}

	ok, err := store.IsAdminGranted(ctx, "sub-unknown", "grantee@uni-osnabrueck.de")
	if err != nil {
		t.Fatal(err)
	}
	if !ok {
		t.Error("a grant made by email did not authorise before first login")
	}

	if err := store.LinkAdminGrantSubject(ctx, "grantee@uni-osnabrueck.de", "sub-42"); err != nil {
		t.Fatal(err)
	}

	ok, err = store.IsAdminGranted(ctx, "sub-42", "someone.else@uni-osnabrueck.de")
	if err != nil {
		t.Fatal(err)
	}
	if !ok {
		t.Error("a linked subject did not authorise after the address changed")
	}
}

// Email matching folds case: IdPs vary in how they present an address, and an
// admin typing it by hand should not have to match that exactly.
func TestIsAdminGrantedFoldsEmailCase(t *testing.T) {
	store := migratedStore(t, "admingrants4")
	ctx := context.Background()

	if err := store.GrantAdmin(ctx, "Mixed.Case@uni-osnabrueck.de", "boss@uni-osnabrueck.de"); err != nil {
		t.Fatal(err)
	}
	ok, err := store.IsAdminGranted(ctx, "", "mixed.case@uni-osnabrueck.de")
	if err != nil {
		t.Fatal(err)
	}
	if !ok {
		t.Error("email match was case-sensitive")
	}
}

// An empty subject must never match a row whose subject is also unset, or a
// user whose IdP omits the claim would take someone else's grant.
func TestIsAdminGrantedIgnoresAnEmptySubject(t *testing.T) {
	store := migratedStore(t, "admingrants5")
	ctx := context.Background()

	if err := store.GrantAdmin(ctx, "someone@uni-osnabrueck.de", "boss@uni-osnabrueck.de"); err != nil {
		t.Fatal(err)
	}
	ok, err := store.IsAdminGranted(ctx, "", "unrelated@uni-osnabrueck.de")
	if err != nil {
		t.Fatal(err)
	}
	if ok {
		t.Error("an empty subject matched a grant with no subject recorded")
	}
}

// Granting an address that is already granted is not an error: the admin
// pressed the button twice, and the outcome they asked for already holds.
func TestGrantAdminIsIdempotent(t *testing.T) {
	store := migratedStore(t, "admingrants6")
	ctx := context.Background()

	if err := store.GrantAdmin(ctx, "twice@uni-osnabrueck.de", "boss@uni-osnabrueck.de"); err != nil {
		t.Fatal(err)
	}
	if err := store.GrantAdmin(ctx, "twice@uni-osnabrueck.de", "boss@uni-osnabrueck.de"); err != nil {
		t.Fatalf("second grant of the same address: %v", err)
	}

	grants, err := store.ListAdminGrants(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if len(grants) != 1 {
		t.Errorf("got %d grants, want 1", len(grants))
	}
}

// Revoking removes the row, so the resolver stops authorising.
func TestRevokeAdminRemovesTheGrant(t *testing.T) {
	store := migratedStore(t, "admingrants7")
	ctx := context.Background()

	if err := store.GrantAdmin(ctx, "gone@uni-osnabrueck.de", "boss@uni-osnabrueck.de"); err != nil {
		t.Fatal(err)
	}
	if err := store.RevokeAdmin(ctx, "gone@uni-osnabrueck.de"); err != nil {
		t.Fatal(err)
	}
	ok, err := store.IsAdminGranted(ctx, "", "gone@uni-osnabrueck.de")
	if err != nil {
		t.Fatal(err)
	}
	if ok {
		t.Error("a revoked grant still authorises")
	}
}

// A grant made before the person ever logged in is anchored to their subject
// the moment they do, so it stops depending on the address.
func TestLoginLinksAPendingGrantToTheSubject(t *testing.T) {
	store := migratedStore(t, "admingrants8")
	ctx := context.Background()

	if err := store.GrantAdmin(ctx, "future@uni-osnabrueck.de", "boss@uni-osnabrueck.de"); err != nil {
		t.Fatal(err)
	}
	if _, err := store.GetOrCreateUser(ctx, "sub-99", "future@uni-osnabrueck.de", "Future Admin"); err != nil {
		t.Fatal(err)
	}

	grants, err := store.ListAdminGrants(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if len(grants) != 1 {
		t.Fatalf("got %d grants, want 1", len(grants))
	}
	if grants[0].OIDCSub == nil || *grants[0].OIDCSub != "sub-99" {
		t.Errorf("OIDCSub = %v, want sub-99 recorded on first login", grants[0].OIDCSub)
	}
}

// Logging in must not attach a subject to somebody else's grant.
func TestLoginLeavesOtherGrantsAlone(t *testing.T) {
	store := migratedStore(t, "admingrants9")
	ctx := context.Background()

	if err := store.GrantAdmin(ctx, "other@uni-osnabrueck.de", "boss@uni-osnabrueck.de"); err != nil {
		t.Fatal(err)
	}
	if _, err := store.GetOrCreateUser(ctx, "sub-100", "unrelated@uni-osnabrueck.de", "Someone"); err != nil {
		t.Fatal(err)
	}

	grants, err := store.ListAdminGrants(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if grants[0].OIDCSub != nil {
		t.Errorf("OIDCSub = %v, want an unrelated login to leave it unset", *grants[0].OIDCSub)
	}
}

// IdPs vary in how they present an address, and an admin typing it by hand
// should not create a duplicate row just because the case differs from a
// prior grant. The unique constraint must fold case the same way the
// lookups do, or ON CONFLICT never fires and the same person ends up
// granted twice.
func TestGrantAdminIsIdempotentAcrossEmailCase(t *testing.T) {
	store := migratedStore(t, "admingrants10")
	ctx := context.Background()

	if err := store.GrantAdmin(ctx, "Bob@uni-osnabrueck.de", "boss@uni-osnabrueck.de"); err != nil {
		t.Fatal(err)
	}
	if err := store.GrantAdmin(ctx, "bob@uni-osnabrueck.de", "boss@uni-osnabrueck.de"); err != nil {
		t.Fatalf("second grant of the same address in a different case: %v", err)
	}

	grants, err := store.ListAdminGrants(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if len(grants) != 1 {
		t.Errorf("got %d grants, want 1", len(grants))
	}
}
