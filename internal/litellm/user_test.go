package litellm

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"
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

	err := NewClient(srv.URL, "mk").UpsertUser(context.Background(), "u1", &UserBudget{Tokens: 1000, Period: "30d"})
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

	if err := NewClient(srv.URL, "mk").UpsertUser(context.Background(), "u1", &UserBudget{Tokens: 1000, Period: "30d"}); err != nil {
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

	if err := NewClient(srv.URL, "mk").UpsertUser(context.Background(), "u1", &UserBudget{Tokens: 1_000_000, Period: "30d"}); err != nil {
		t.Fatal(err)
	}
	if got := body["max_budget"]; got != TokensToBudget(1_000_000) {
		t.Errorf("max_budget = %v, want %v", got, TokensToBudget(1_000_000))
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

	if err := NewClient(srv.URL, "mk").UpsertUser(context.Background(), "u1", nil); err != nil {
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
	if q.UsedTokens != BudgetToTokens(0.02) {
		t.Errorf("used = %d, want %d", q.UsedTokens, BudgetToTokens(0.02))
	}
	if q.LimitTokens != BudgetToTokens(0.1) {
		t.Errorf("limit = %d, want %d", q.LimitTokens, BudgetToTokens(0.1))
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
	if q.LimitTokens != 0 || q.UsedTokens != 0 {
		t.Errorf("got %+v, want an empty quota", q)
	}
}
