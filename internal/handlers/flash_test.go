package handlers

import "testing"

func TestFlashRoundTrip(t *testing.T) {
	f := newKeyFlash()
	tok, err := f.Put(7, "sk-secret")
	if err != nil {
		t.Fatal(err)
	}
	if tok == "" {
		t.Fatal("empty token")
	}
	if got := f.Take(7, tok); got != "sk-secret" {
		t.Fatalf("first Take = %q, want the key", got)
	}
	// One-time: a reload must not re-reveal the secret.
	if got := f.Take(7, tok); got != "" {
		t.Fatalf("second Take = %q, want empty (single use)", got)
	}
}

func TestFlashRejectsOtherUser(t *testing.T) {
	f := newKeyFlash()
	tok, _ := f.Put(1, "sk-secret")
	if got := f.Take(2, tok); got != "" {
		t.Fatalf("user 2 redeemed user 1's token: %q", got)
	}
	// Owner can still redeem it.
	if got := f.Take(1, tok); got != "sk-secret" {
		t.Fatalf("owner Take = %q, want the key", got)
	}
}

func TestFlashUnknownAndEmptyToken(t *testing.T) {
	f := newKeyFlash()
	if got := f.Take(1, ""); got != "" {
		t.Fatalf("empty token returned %q", got)
	}
	if got := f.Take(1, "not-a-real-token"); got != "" {
		t.Fatalf("unknown token returned %q", got)
	}
}
