package handlers

import (
	"context"
	"net/url"
	"strings"
	"testing"
)

// flashOf returns the flash message a redirect carries.
func flashOf(t *testing.T, location string) string {
	t.Helper()
	u, err := url.Parse(location)
	if err != nil {
		t.Fatal(err)
	}
	return u.Query().Get("flash")
}

// A refused change to the default profile names the reason and leaves the
// profile as it was.
func TestAdminCannotRemoveTheDefaultProfile(t *testing.T) {
	a, store, token, _ := newSyncTestAdmin(t, "pguard-default")
	ctx := context.Background()
	def, err := store.GetDefaultProfile(ctx)
	if err != nil {
		t.Fatal(err)
	}

	rec := postAdminForm(t, a.UpdateProfile, token, "/admin/profiles/1", def.ID,
		url.Values{"name": {"renamed"}})
	if f := flashOf(t, rec.Header().Get("Location")); !strings.Contains(f, "must stay the default") {
		t.Errorf("un-default flash = %q", f)
	}

	rec = postAdminForm(t, a.DeleteProfile, token, "/admin/profiles/1/delete", def.ID, nil)
	if f := flashOf(t, rec.Header().Get("Location")); !strings.Contains(f, "cannot be deleted") {
		t.Errorf("delete flash = %q", f)
	}

	got, err := store.GetDefaultProfile(ctx)
	if err != nil {
		t.Fatalf("default profile gone: %v", err)
	}
	if got.ID != def.ID || got.Name != def.Name {
		t.Errorf("default = %d %q, want %d %q unchanged", got.ID, got.Name, def.ID, def.Name)
	}
}
