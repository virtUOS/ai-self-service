package database

import (
	"context"
	"testing"
)

func TestAuditRecordAndList(t *testing.T) {
	s := migratedStore(t, "au1")
	ctx := context.Background()

	uid := int64(7)
	events := []AuditEvent{
		{Action: AuditKeyGenerated, ActorEmail: "s@uni.de", SubjectEmail: "s@uni.de", SubjectID: &uid, Detail: "key sk-abc"},
		{Action: AuditKeyExtended, ActorEmail: "s@uni.de", SubjectEmail: "s@uni.de", SubjectID: &uid},
		{Action: AuditKeyRevoked, ActorEmail: "admin@uni.de", SubjectEmail: "s@uni.de", SubjectID: &uid, Detail: "key sk-abc"},
	}
	for i := range events {
		if err := s.RecordAudit(ctx, &events[i]); err != nil {
			t.Fatalf("record %d: %v", i, err)
		}
	}

	got, err := s.ListAuditEvents(ctx, 10)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 3 {
		t.Fatalf("got %d events, want 3", len(got))
	}
	// Newest first, so the admin revocation leads.
	if got[0].Action != AuditKeyRevoked {
		t.Errorf("first event = %s, want %s (newest first)", got[0].Action, AuditKeyRevoked)
	}
	if got[0].ActorEmail != "admin@uni.de" || got[0].SubjectEmail != "s@uni.de" {
		t.Errorf("actor/subject = %s/%s, want admin@uni.de/s@uni.de",
			got[0].ActorEmail, got[0].SubjectEmail)
	}
	if got[0].CreatedAt.IsZero() {
		t.Error("CreatedAt not set")
	}
}

func TestAuditListLimit(t *testing.T) {
	s := migratedStore(t, "au2")
	ctx := context.Background()
	for i := 0; i < 10; i++ {
		if err := s.RecordAudit(ctx, &AuditEvent{
			Action: AuditKeyGenerated, ActorEmail: "a@b.c", SubjectEmail: "a@b.c",
		}); err != nil {
			t.Fatal(err)
		}
	}
	got, err := s.ListAuditEvents(ctx, 4)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 4 {
		t.Fatalf("limit 4 returned %d", len(got))
	}
}

// History must outlive the key it describes: revoking must not erase the trail.
func TestAuditSurvivesKeyDeletion(t *testing.T) {
	s := migratedStore(t, "au3")
	ctx := context.Background()
	u, err := s.GetOrCreateUser(ctx, "sub", "s@uni.de", "S")
	if err != nil {
		t.Fatal(err)
	}
	if err := s.RecordAudit(ctx, &AuditEvent{
		Action: AuditKeyRevoked, ActorEmail: "admin@uni.de",
		SubjectEmail: u.Email, SubjectID: &u.ID, Detail: "key sk-gone",
	}); err != nil {
		t.Fatal(err)
	}
	events, _ := s.ListAuditEvents(ctx, 10)
	if len(events) != 1 || events[0].SubjectEmail != "s@uni.de" {
		t.Fatalf("audit row lost: %#v", events)
	}
}

func TestListAPIKeys(t *testing.T) {
	s := migratedStore(t, "au4")
	ctx := context.Background()
	u1, _ := s.GetOrCreateUser(ctx, "s1", "a@b.c", "A")
	u2, _ := s.GetOrCreateUser(ctx, "s2", "c@d.e", "B")
	for _, u := range []int64{u1.ID, u2.ID} {
		if err := s.ReplaceAPIKey(ctx, &APIKey{
			UserID: u, LiteLLMKey: "sk-x", KeyPrefix: "sk-x",
		}); err != nil {
			t.Fatal(err)
		}
	}
	keys, err := s.ListAPIKeys(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if len(keys) != 2 {
		t.Fatalf("got %d keys, want 2", len(keys))
	}
}
