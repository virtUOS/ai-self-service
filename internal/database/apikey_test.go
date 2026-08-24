package database

import (
	"context"
	"testing"
	"time"
)

// ReplaceAPIKey must leave exactly one row, even called repeatedly.
func TestReplaceAPIKeyRotates(t *testing.T) {
	s := testStore(t, "rk1")
	ctx := context.Background()
	if err := s.RunMigrations(ctx); err != nil {
		t.Fatal(err)
	}
	u, err := s.GetOrCreateUser(ctx, "sub", "a@b.c", "A")
	if err != nil {
		t.Fatal(err)
	}

	for i, want := range []string{"sk-1", "sk-2", "sk-3"} {
		err := s.ReplaceAPIKey(ctx, &APIKey{
			UserID: u.ID, LiteLLMKey: want, KeyPrefix: want,
			ExpiresAt: time.Now().Add(time.Hour),
		})
		if err != nil {
			t.Fatalf("rotation %d: %v", i, err)
		}
		got, err := s.GetAPIKeyByUser(ctx, u.ID)
		if err != nil {
			t.Fatal(err)
		}
		if got == nil || got.LiteLLMKey != want {
			t.Fatalf("after rotation %d: got %v, want %s", i, got, want)
		}
		var n int
		if err := s.db.QueryRowContext(ctx,
			`SELECT COUNT(*) FROM api_keys WHERE user_id = ?`, u.ID).Scan(&n); err != nil {
			t.Fatal(err)
		}
		if n != 1 {
			t.Fatalf("after rotation %d: %d rows, want 1", i, n)
		}
	}
}

// A user with no key is an ordinary state, not an error.
func TestGetAPIKeyByUserReturnsNilWhenAbsent(t *testing.T) {
	s := testStore(t, "rk2")
	ctx := context.Background()
	if err := s.RunMigrations(ctx); err != nil {
		t.Fatal(err)
	}
	k, err := s.GetAPIKeyByUser(ctx, 4242)
	if err != nil {
		t.Fatalf("absent key reported as error: %v", err)
	}
	if k != nil {
		t.Fatalf("got %v, want nil", k)
	}
}

// Two users must each keep their own key.
func TestReplaceAPIKeyScopedToUser(t *testing.T) {
	s := testStore(t, "rk3")
	ctx := context.Background()
	if err := s.RunMigrations(ctx); err != nil {
		t.Fatal(err)
	}
	u1, _ := s.GetOrCreateUser(ctx, "s1", "a@b.c", "A")
	u2, _ := s.GetOrCreateUser(ctx, "s2", "c@d.e", "B")

	exp := time.Now().Add(time.Hour)
	if err := s.ReplaceAPIKey(ctx, &APIKey{UserID: u1.ID, LiteLLMKey: "sk-u1", KeyPrefix: "sk-u1", ExpiresAt: exp}); err != nil {
		t.Fatal(err)
	}
	if err := s.ReplaceAPIKey(ctx, &APIKey{UserID: u2.ID, LiteLLMKey: "sk-u2", KeyPrefix: "sk-u2", ExpiresAt: exp}); err != nil {
		t.Fatal(err)
	}
	// Rotating u2 must not disturb u1.
	if err := s.ReplaceAPIKey(ctx, &APIKey{UserID: u2.ID, LiteLLMKey: "sk-u2b", KeyPrefix: "sk-u2b", ExpiresAt: exp}); err != nil {
		t.Fatal(err)
	}
	k1, _ := s.GetAPIKeyByUser(ctx, u1.ID)
	if k1 == nil || k1.LiteLLMKey != "sk-u1" {
		t.Errorf("user1 key = %v, want sk-u1", k1)
	}
	k2, _ := s.GetAPIKeyByUser(ctx, u2.ID)
	if k2 == nil || k2.LiteLLMKey != "sk-u2b" {
		t.Errorf("user2 key = %v, want sk-u2b", k2)
	}
}
