package litellm

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/virtuos/ai-self-service/internal/keyprovider"
)

// The gateway has no upsert route: /user/new answers 409 once the user exists.
// That is the normal path for every key after a user's first, so it must fall
// through to /user/update rather than failing the key generation.
func TestUpsertUserFallsBackToUpdateOnConflict(t *testing.T) {
	var paths []string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		paths = append(paths, r.URL.Path)
		if r.URL.Path == "/user/new" {
			w.WriteHeader(http.StatusConflict)
			w.Write([]byte(`{"error":{"message":"User already exists"}}`))
			return
		}
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()

	err := NewClient(srv.URL, "mk").UpsertUser(context.Background(), keyprovider.User{ID: "u1"}, &UserBudget{Budget: 0.0001, Period: "30d"})
	if err != nil {
		t.Fatalf("upsert failed on an existing user: %v", err)
	}
	if len(paths) != 2 || paths[0] != "/user/new" || paths[1] != "/user/update" {
		t.Errorf("call sequence = %v, want [/user/new /user/update]", paths)
	}
}

// A user that does not exist yet is created outright, with no second call.
func TestUpsertUserCreatesWhenAbsent(t *testing.T) {
	var paths []string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		paths = append(paths, r.URL.Path)
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()

	if err := NewClient(srv.URL, "mk").UpsertUser(context.Background(), keyprovider.User{ID: "u1"}, &UserBudget{Budget: 0.0001, Period: "30d"}); err != nil {
		t.Fatal(err)
	}
	if len(paths) != 1 || paths[0] != "/user/new" {
		t.Errorf("call sequence = %v, want [/user/new] only", paths)
	}
}

// The budget is sent as a spend cap, and as a single window. Stacked windows
// are silently dropped by the gateway, so nothing here may send them.
func TestUpsertUserSendsSingleWindowAsSpend(t *testing.T) {
	var body map[string]any
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		b, _ := io.ReadAll(r.Body)
		json.Unmarshal(b, &body)
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()

	if err := NewClient(srv.URL, "mk").UpsertUser(context.Background(), keyprovider.User{ID: "u1"}, &UserBudget{Budget: 0.1, Period: "30d"}); err != nil {
		t.Fatal(err)
	}
	if got := body["max_budget"]; got != 0.1 {
		t.Errorf("max_budget = %v, want 0.1", got)
	}
	if got := body["budget_duration"]; got != "30d" {
		t.Errorf("budget_duration = %v, want 30d", got)
	}
	if _, ok := body["budget_limits"]; ok {
		t.Error("budget_limits sent to a user, which the gateway accepts but does not store")
	}
}

// Dropping a profile's quota has to clear the allowance upstream. An omitted
// field leaves the old budget in force, so the nulls must be explicit.
func TestUpsertUserClearsBudgetWhenNil(t *testing.T) {
	var body map[string]any
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		b, _ := io.ReadAll(r.Body)
		json.Unmarshal(b, &body)
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()

	if err := NewClient(srv.URL, "mk").UpsertUser(context.Background(), keyprovider.User{ID: "u1"}, nil); err != nil {
		t.Fatal(err)
	}
	for _, f := range []string{"max_budget", "budget_duration"} {
		v, ok := body[f]
		if !ok {
			t.Errorf("%s omitted, so the old budget stays in force", f)
		}
		if v != nil {
			t.Errorf("%s = %v, want null", f, v)
		}
	}
}

// The quota shown to a user is the one enforced on them across every key.
func TestUserQuotaReportsSpendAgainstBudget(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if got := r.URL.Query().Get("user_id"); got != "u1" {
			t.Errorf("user_id = %q, want u1", got)
		}
		w.Write([]byte(`{"user_info":{"spend":0.02,"max_budget":0.1,"budget_reset_at":"2026-09-01T00:00:00Z"}}`))
	}))
	defer srv.Close()

	q, err := NewClient(srv.URL, "mk").UserQuota(context.Background(), "u1")
	if err != nil {
		t.Fatal(err)
	}
	if q.Used != 0.02 {
		t.Errorf("used = %v, want 0.02", q.Used)
	}
	if q.Limit != 0.1 {
		t.Errorf("limit = %v, want 0.1", q.Limit)
	}
	if q.ResetsAt.IsZero() {
		t.Error("reset time not reported")
	}
}

// Keys issued before the portal tracked users have no user upstream. That is
// expected during the transition, not a failure that should break a dashboard.
func TestUserQuotaTreatsMissingUserAsNoUsage(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusNotFound)
		w.Write([]byte(`{"error":{"message":"User not found"}}`))
	}))
	defer srv.Close()

	q, err := NewClient(srv.URL, "mk").UserQuota(context.Background(), "nobody")
	if err != nil {
		t.Fatalf("missing user reported as an error: %v", err)
	}
	if q.Limit != 0 || q.Used != 0 {
		t.Errorf("got %+v, want an empty quota", q)
	}
}

// recordBodies is a gateway that records each request body by path, and
// answers /user/new with the given status.
func recordBodies(t *testing.T, newStatus int, newBody string) (*httptest.Server, map[string]map[string]any) {
	t.Helper()
	bodies := map[string]map[string]any{}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var body map[string]any
		b, _ := io.ReadAll(r.Body)
		json.Unmarshal(b, &body)
		bodies[r.URL.Path] = body
		if r.URL.Path == "/user/new" {
			w.WriteHeader(newStatus)
			w.Write([]byte(newBody))
			return
		}
		w.WriteHeader(http.StatusOK)
	}))
	t.Cleanup(srv.Close)
	return srv, bodies
}

// Without auto_create_key, /user/new also mints a key: one with no alias and
// no expiry, which the portal never records and so could never revoke.
// /user/update has no such field, so it is not sent there.
func TestUpsertUserAsksForNoKey(t *testing.T) {
	srv, bodies := recordBodies(t, http.StatusConflict, `{}`)
	if err := NewClient(srv.URL, "mk").UpsertUser(context.Background(), keyprovider.User{ID: "u1"}, nil); err != nil {
		t.Fatal(err)
	}
	if v, ok := bodies["/user/new"]["auto_create_key"]; !ok || v != false {
		t.Errorf("/user/new auto_create_key = %v, want false", v)
	}
	if _, ok := bodies["/user/update"]["auto_create_key"]; ok {
		t.Errorf("/user/update was sent auto_create_key: %v", bodies["/user/update"])
	}
}

// The alias and email label the user in LiteLLM's UI, and go to both routes so
// an existing user picks up a changed name or address.
func TestUpsertUserSendsAliasAndEmail(t *testing.T) {
	srv, bodies := recordBodies(t, http.StatusConflict, `{}`)
	u := keyprovider.User{ID: "u1", Name: "alice", Email: "a@uni-osnabrueck.de"}
	if err := NewClient(srv.URL, "mk").UpsertUser(context.Background(), u, nil); err != nil {
		t.Fatal(err)
	}
	for _, path := range []string{"/user/new", "/user/update"} {
		b := bodies[path]
		if b["user_alias"] != "alice" || b["user_email"] != "a@uni-osnabrueck.de" {
			t.Errorf("%s alias/email = %v/%v, want alice and the address", path, b["user_alias"], b["user_email"])
		}
	}
}

// Empty fields are left out rather than sent empty, so the email option being
// off never clears an address set some other way.
func TestUpsertUserOmitsEmptyAliasAndEmail(t *testing.T) {
	srv, bodies := recordBodies(t, http.StatusConflict, `{}`)
	if err := NewClient(srv.URL, "mk").UpsertUser(context.Background(), keyprovider.User{ID: "u1"}, nil); err != nil {
		t.Fatal(err)
	}
	for _, path := range []string{"/user/new", "/user/update"} {
		for _, field := range []string{"user_alias", "user_email"} {
			if _, ok := bodies[path][field]; ok {
				t.Errorf("%s sent %s for an empty value", path, field)
			}
		}
	}
}

// /user/new also answers 409 when another user holds the email. The update
// that follows then fails, and the error must carry the 409's reason, or it
// reads as if the portal's own user were missing.
func TestUpsertUserReportsTheConflictWhenTheUpdateFails(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/user/new" {
			w.WriteHeader(http.StatusConflict)
			w.Write([]byte(`{"error":"User with email a@uni-osnabrueck.de already exists"}`))
			return
		}
		w.WriteHeader(http.StatusNotFound)
		w.Write([]byte(`{"error":"user not found"}`))
	}))
	defer srv.Close()

	u := keyprovider.User{ID: "u1", Email: "a@uni-osnabrueck.de"}
	err := NewClient(srv.URL, "mk").UpsertUser(context.Background(), u, nil)
	if err == nil {
		t.Fatal("upsert succeeded though the update failed")
	}
	if !strings.Contains(err.Error(), "already exists") || !strings.Contains(err.Error(), "user not found") {
		t.Errorf("error %q does not carry both answers", err)
	}
}
