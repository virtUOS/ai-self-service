package profileexpiry

import (
	"context"
	"database/sql"
	"errors"
	"testing"
	"time"

	"github.com/uptrace/bun"
	"github.com/uptrace/bun/dialect/sqlitedialect"
	"github.com/uptrace/bun/driver/sqliteshim"

	"github.com/virtuos/ai-self-service/internal/database"
	"github.com/virtuos/ai-self-service/internal/keyprovider"
)

func testStore(t *testing.T, name string) *database.Store {
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
	if err := store.SeedDefaultProfile(context.Background()); err != nil {
		t.Fatal(err)
	}
	return store
}

// A deadline in the future must be left alone.
func TestRunLeavesFutureDeadlines(t *testing.T) {
	store := testStore(t, "job1")
	ctx := context.Background()
	u, err := store.GetOrCreateUser(ctx, "sub-1", "a@uni-osnabrueck.de", "A")
	if err != nil {
		t.Fatal(err)
	}
	p := &database.Profile{Name: "raised"}
	if err := store.CreateProfile(ctx, p); err != nil {
		t.Fatal(err)
	}
	tomorrow := time.Now().Add(24 * time.Hour)
	if err := store.SetUserProfileUntil(ctx, u.ID, &p.ID, &tomorrow, nil, false); err != nil {
		t.Fatal(err)
	}

	if err := NewRunner(store, keyprovider.NewFake()).Run(ctx); err != nil {
		t.Fatal(err)
	}

	got, err := store.GetUserByID(ctx, u.ID)
	if err != nil {
		t.Fatal(err)
	}
	if got.ProfileID == nil || *got.ProfileID != p.ID {
		t.Errorf("ProfileID = %v, want the assignment untouched (%d)", got.ProfileID, p.ID)
	}
}

// The plain case: the deadline passed and no destination was set, so the user
// falls back to the default profile.
func TestRunRevertsToTheDefaultProfile(t *testing.T) {
	store := testStore(t, "job2")
	ctx := context.Background()
	u, err := store.GetOrCreateUser(ctx, "sub-2", "b@uni-osnabrueck.de", "B")
	if err != nil {
		t.Fatal(err)
	}
	p := &database.Profile{Name: "raised"}
	if err := store.CreateProfile(ctx, p); err != nil {
		t.Fatal(err)
	}
	yesterday := time.Now().Add(-time.Hour)
	if err := store.SetUserProfileUntil(ctx, u.ID, &p.ID, &yesterday, nil, false); err != nil {
		t.Fatal(err)
	}

	if err := NewRunner(store, keyprovider.NewFake()).Run(ctx); err != nil {
		t.Fatal(err)
	}

	got, err := store.GetUserByID(ctx, u.ID)
	if err != nil {
		t.Fatal(err)
	}
	if got.ProfileID != nil {
		t.Errorf("ProfileID = %v, want nil so the default applies", got.ProfileID)
	}
	if got.ProfileExpiresAt != nil {
		t.Error("the deadline was not cleared")
	}
}

// A destination the admin picked wins over the default.
func TestRunRevertsToTheChosenProfile(t *testing.T) {
	store := testStore(t, "job3")
	ctx := context.Background()
	u, err := store.GetOrCreateUser(ctx, "sub-3", "c@uni-osnabrueck.de", "C")
	if err != nil {
		t.Fatal(err)
	}
	from := &database.Profile{Name: "raised"}
	to := &database.Profile{Name: "restricted"}
	if err := store.CreateProfile(ctx, from); err != nil {
		t.Fatal(err)
	}
	if err := store.CreateProfile(ctx, to); err != nil {
		t.Fatal(err)
	}
	yesterday := time.Now().Add(-time.Hour)
	if err := store.SetUserProfileUntil(ctx, u.ID, &from.ID, &yesterday, &to.ID, false); err != nil {
		t.Fatal(err)
	}

	if err := NewRunner(store, keyprovider.NewFake()).Run(ctx); err != nil {
		t.Fatal(err)
	}

	got, err := store.GetUserByID(ctx, u.ID)
	if err != nil {
		t.Fatal(err)
	}
	if got.ProfileID == nil || *got.ProfileID != to.ID {
		t.Errorf("ProfileID = %v, want the chosen destination %d", got.ProfileID, to.ID)
	}
}

// The new limits must reach the gateway without waiting for a dashboard load,
// or a user who stops visiting keeps the elevated limits on a live key.
func TestRunPushesNewLimitsUpstream(t *testing.T) {
	store := testStore(t, "job4")
	ctx := context.Background()
	u, err := store.GetOrCreateUser(ctx, "sub-4", "d@uni-osnabrueck.de", "D")
	if err != nil {
		t.Fatal(err)
	}
	p := &database.Profile{Name: "raised"}
	if err := store.CreateProfile(ctx, p); err != nil {
		t.Fatal(err)
	}
	if err := store.CreateAPIKey(ctx, &database.APIKey{
		UserID: u.ID, LiteLLMKey: "sk-live", KeyPrefix: "sk-li",
		ExpiresAt: time.Now().Add(90 * 24 * time.Hour), CreatedAt: time.Now(),
	}); err != nil {
		t.Fatal(err)
	}
	yesterday := time.Now().Add(-time.Hour)
	if err := store.SetUserProfileUntil(ctx, u.ID, &p.ID, &yesterday, nil, false); err != nil {
		t.Fatal(err)
	}

	fake := keyprovider.NewFake()
	if err := NewRunner(store, fake).Run(ctx); err != nil {
		t.Fatal(err)
	}

	if _, ok := fake.LimitsByRef["sk-live"]; !ok {
		t.Error("the job did not push the reverted limits to the gateway")
	}
}

// The explicit third option: delete the key rather than switch profiles.
func TestRunRevokesTheKeyWhenAsked(t *testing.T) {
	store := testStore(t, "job5")
	ctx := context.Background()
	u, err := store.GetOrCreateUser(ctx, "sub-5", "e@uni-osnabrueck.de", "E")
	if err != nil {
		t.Fatal(err)
	}
	p := &database.Profile{Name: "raised"}
	if err := store.CreateProfile(ctx, p); err != nil {
		t.Fatal(err)
	}
	if err := store.CreateAPIKey(ctx, &database.APIKey{
		UserID: u.ID, LiteLLMKey: "sk-doomed", KeyPrefix: "sk-do",
		ExpiresAt: time.Now().Add(90 * 24 * time.Hour), CreatedAt: time.Now(),
	}); err != nil {
		t.Fatal(err)
	}
	yesterday := time.Now().Add(-time.Hour)
	if err := store.SetUserProfileUntil(ctx, u.ID, &p.ID, &yesterday, nil, true); err != nil {
		t.Fatal(err)
	}

	// The fake's DeleteKey requires the ref to already be known to it, so seed
	// it the way CreateKey would have.
	fake := keyprovider.NewFake()
	fake.Keys["sk-doomed"] = keyprovider.KeyRequest{}

	if err := NewRunner(store, fake).Run(ctx); err != nil {
		t.Fatal(err)
	}

	found := false
	for _, ref := range fake.Deleted {
		if ref == "sk-doomed" {
			found = true
		}
	}
	if !found {
		t.Error("the key was not deleted upstream")
	}
	key, err := store.GetAPIKeyByUser(ctx, u.ID)
	if err != nil {
		t.Fatal(err)
	}
	if key != nil {
		t.Error("the local key row survived the revoke")
	}
}

// Running twice must change nothing the second time.
func TestRunIsIdempotent(t *testing.T) {
	store := testStore(t, "job6")
	ctx := context.Background()
	u, err := store.GetOrCreateUser(ctx, "sub-6", "f@uni-osnabrueck.de", "F")
	if err != nil {
		t.Fatal(err)
	}
	p := &database.Profile{Name: "raised"}
	if err := store.CreateProfile(ctx, p); err != nil {
		t.Fatal(err)
	}
	yesterday := time.Now().Add(-time.Hour)
	if err := store.SetUserProfileUntil(ctx, u.ID, &p.ID, &yesterday, nil, false); err != nil {
		t.Fatal(err)
	}

	r := NewRunner(store, keyprovider.NewFake())
	if err := r.Run(ctx); err != nil {
		t.Fatal(err)
	}
	before, err := store.GetUserByID(ctx, u.ID)
	if err != nil {
		t.Fatal(err)
	}
	if err := r.Run(ctx); err != nil {
		t.Fatal(err)
	}
	after, err := store.GetUserByID(ctx, u.ID)
	if err != nil {
		t.Fatal(err)
	}

	if before.UpdatedAt != after.UpdatedAt {
		t.Error("the second run touched a user with no deadline left")
	}
}

// An upstream failure must leave the deadline in place so the next run retries,
// rather than silently dropping the user's limits on the floor.
func TestRunKeepsTheDeadlineWhenTheGatewayFails(t *testing.T) {
	store := testStore(t, "job7")
	ctx := context.Background()
	u, err := store.GetOrCreateUser(ctx, "sub-7", "g@uni-osnabrueck.de", "G")
	if err != nil {
		t.Fatal(err)
	}
	p := &database.Profile{Name: "raised"}
	if err := store.CreateProfile(ctx, p); err != nil {
		t.Fatal(err)
	}
	if err := store.CreateAPIKey(ctx, &database.APIKey{
		UserID: u.ID, LiteLLMKey: "sk-fail", KeyPrefix: "sk-fa",
		ExpiresAt: time.Now().Add(90 * 24 * time.Hour), CreatedAt: time.Now(),
	}); err != nil {
		t.Fatal(err)
	}
	yesterday := time.Now().Add(-time.Hour)
	if err := store.SetUserProfileUntil(ctx, u.ID, &p.ID, &yesterday, nil, true); err != nil {
		t.Fatal(err)
	}

	fake := keyprovider.NewFake()
	fake.DeleteErr = errors.New("gateway unavailable")
	// The run reports the failure but must not panic or abort the batch.
	_ = NewRunner(store, fake).Run(ctx)

	got, err := store.GetUserByID(ctx, u.ID)
	if err != nil {
		t.Fatal(err)
	}
	if got.ProfileExpiresAt == nil {
		t.Error("deadline cleared despite the upstream delete failing; the next run cannot retry")
	}
}
