//go:build e2e

package litellm

// End-to-end tests against a real gateway: by default the one in
// dev/docker-compose.yml, whose models are defined in dev/litellm-config.yaml
// and answer with a canned response, so usage costs nothing. Built only with
// the e2e tag, so the normal suite still needs nothing external:
//
//	docker compose -f dev/docker-compose.yml up -d --wait litellm
//	go test -tags e2e ./internal/litellm/
//
// LITELLM_BASE_URL and LITELLM_MASTER_KEY point them at another gateway, which
// needs the same models. Every test works under an owner of its own, so
// reruns against a long-lived stack never see each other's keys or spend.

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math"
	"net/http"
	"net/url"
	"os"
	"slices"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/virtuos/ai-self-service/internal/keyprovider"
)

// What a request costs is read back from the gateway rather than fixed here:
// LiteLLM's token count for the same mock request differs between releases.
const (
	spendLogPatience   = 60 * time.Second
	spendLogPollPeriod = time.Second
)

type e2eGateway struct {
	*Provider
	baseURL string
}

func newE2EGateway(t *testing.T) e2eGateway {
	t.Helper()
	base := envOr("LITELLM_BASE_URL", "http://localhost:4000")
	key := envOr("LITELLM_MASTER_KEY", "sk-local-dev")
	return e2eGateway{Provider: NewProvider(NewClient(base, key)), baseURL: base}
}

func envOr(name, fallback string) string {
	if v := os.Getenv(name); v != "" {
		return v
	}
	return fallback
}

// e2eOwner is an owner no earlier run has used.
func e2eOwner(t *testing.T) string {
	return fmt.Sprintf("zz-e2e-%s-%s",
		strings.ToLower(t.Name()), strconv.FormatInt(time.Now().UnixNano(), 36))
}

// createKey creates a key for owner and deletes it when the test ends.
func (g e2eGateway) createKey(t *testing.T, owner string, limits keyprovider.Limits) keyprovider.KeyResult {
	t.Helper()
	// Aliases are unique across the gateway, so a rotation needs a new one.
	alias := owner + "-" + strconv.FormatInt(time.Now().UnixNano(), 36)
	k, err := g.CreateKey(context.Background(), keyprovider.KeyRequest{
		Alias: alias, User: keyprovider.User{ID: owner}, Limits: limits,
		ExpiresAt: time.Now().AddDate(0, 0, 1),
	})
	if err != nil {
		t.Fatalf("create key: %v", err)
	}
	// A test that deletes the key itself makes this fail, which is fine.
	t.Cleanup(func() { _ = g.DeleteKey(context.Background(), k.Ref) })
	return k
}

// complete sends one chat request with a user's key, as a client of the
// gateway would, and returns the status. The portal itself never does this.
func (g e2eGateway) complete(t *testing.T, secret, model string) int {
	status, _ := g.completeBody(t, secret, model)
	return status
}

// completeBody is complete that also returns the response body, for errors.
func (g e2eGateway) completeBody(t *testing.T, secret, model string) (int, string) {
	t.Helper()
	body, _ := json.Marshal(map[string]any{
		"model":    model,
		"messages": []map[string]string{{"role": "user", "content": "hello"}},
		// LiteLLM reserves a request's worst-case cost against the budget
		// until the response is settled, and without max_tokens that is the
		// model's whole output limit — more than the small test budgets
		// hold, which would turn the next request into a 422 for a moment.
		"max_tokens": 20,
	})
	req, err := http.NewRequest(http.MethodPost, g.baseURL+"/chat/completions", bytes.NewReader(body))
	if err != nil {
		t.Fatal(err)
	}
	req.Header.Set("Authorization", "Bearer "+secret)
	req.Header.Set("Content-Type", "application/json")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("chat request: %v", err)
	}
	defer resp.Body.Close()
	b, _ := io.ReadAll(resp.Body)
	return resp.StatusCode, string(b)
}

// mustComplete is complete for requests that have to succeed.
func (g e2eGateway) mustComplete(t *testing.T, secret, model string) {
	t.Helper()
	if status, body := g.completeBody(t, secret, model); status != http.StatusOK {
		t.Fatalf("chat request on %s returned %d: %s", model, status, body)
	}
}

// eventually retries check until it passes. The spend log is written in
// batches, so usage shows up a few seconds after the request that caused it.
func eventually(t *testing.T, what string, check func() error) {
	t.Helper()
	deadline := time.Now().Add(spendLogPatience)
	for {
		err := check()
		if err == nil {
			return
		}
		if time.Now().After(deadline) {
			t.Fatalf("%s: %v", what, err)
		}
		time.Sleep(spendLogPollPeriod)
	}
}

func near(a, b float64) bool { return math.Abs(a-b) < 1e-9 }

func TestE2EModels(t *testing.T) {
	g := newE2EGateway(t)
	ctx := context.Background()

	models, err := g.ListModels(ctx)
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"mock-embed", "mock-large", "mock-small"} {
		if !slices.Contains(models, want) {
			t.Errorf("models = %v, missing %s", models, want)
		}
	}

	embedding, err := g.EmbeddingModels(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if !embedding["mock-embed"] || embedding["mock-small"] || embedding["mock-large"] {
		t.Errorf("embedding models = %v, want mock-embed only", embedding)
	}
}

func TestE2EKeyLifecycle(t *testing.T) {
	g := newE2EGateway(t)
	ctx := context.Background()
	owner := e2eOwner(t)

	k := g.createKey(t, owner, keyprovider.Limits{
		Models: []string{"mock-small"},
		Quotas: []keyprovider.QuotaWindow{{Budget: 1, Period: "1h"}, {Budget: 5, Period: "30d"}},
	})
	if k.Secret == "" || k.Ref == "" || k.Ref == k.Secret {
		t.Fatalf("key result = %+v, want a secret and a distinct ref", k)
	}

	// The model list on the key is enforced.
	g.mustComplete(t, k.Secret, "mock-small")
	if status := g.complete(t, k.Secret, "mock-large"); status == http.StatusOK {
		t.Error("key restricted to mock-small could use mock-large")
	}

	// New limits reach both halves: the key's window and the owner's.
	err := g.UpdateLimits(ctx, k.Ref, keyprovider.User{ID: owner}, keyprovider.Limits{
		Quotas: []keyprovider.QuotaWindow{{Budget: 2, Period: "1h"}, {Budget: 7, Period: "30d"}},
	})
	if err != nil {
		t.Fatalf("update limits: %v", err)
	}
	windows, err := g.Windows(ctx, k.Ref, owner)
	if err != nil {
		t.Fatal(err)
	}
	limits := map[string]float64{}
	for _, w := range windows {
		limits[w.Period] = w.Limit
	}
	if limits["1h"] != 2 || limits["30d"] != 7 {
		t.Errorf("limits after update = %v, want 1h:2 30d:7", limits)
	}
	// Dropping the model list opens the key to every model.
	g.mustComplete(t, k.Secret, "mock-large")

	// Extending moves the expiry the gateway enforces.
	want := time.Now().AddDate(0, 0, 10)
	if err := g.UpdateExpiry(ctx, k.Ref, want); err != nil {
		t.Fatalf("update expiry: %v", err)
	}
	var info struct {
		Info struct {
			Expires string `json:"expires"`
		} `json:"info"`
	}
	resp, err := g.client.do(ctx, http.MethodGet, keyInfoPath(k.Ref), nil)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if err := json.NewDecoder(resp.Body).Decode(&info); err != nil {
		t.Fatal(err)
	}
	expires, err := time.Parse(time.RFC3339, info.Info.Expires)
	if err != nil {
		t.Fatalf("parse expiry %q: %v", info.Info.Expires, err)
	}
	if d := expires.Sub(want); d < -36*time.Hour || d > 36*time.Hour {
		t.Errorf("expires = %s, want about %s", expires, want)
	}

	// A deleted key is gone, both for the portal and for the user. (Its
	// /key/info still answers, with status "deleted".)
	if err := g.DeleteKey(ctx, k.Ref); err != nil {
		t.Fatalf("delete key: %v", err)
	}
	if err := g.UpdateExpiry(ctx, k.Ref, want); err == nil {
		t.Error("a deleted key could still be extended")
	}
	if status := g.complete(t, k.Secret, "mock-small"); status == http.StatusOK {
		t.Error("deleted key still works")
	}
}

// Issue #26: the widest window lives on the owner, so regenerating a key must
// neither reset its spend nor change its allowance.
func TestE2EOwnerQuotaSurvivesRotation(t *testing.T) {
	g := newE2EGateway(t)
	ctx := context.Background()
	owner := e2eOwner(t)
	limits := keyprovider.Limits{Quotas: []keyprovider.QuotaWindow{
		{Budget: 1, Period: "1h"},
		{Budget: 10, Period: "30d"},
	}}

	k1 := g.createKey(t, owner, limits)
	g.mustComplete(t, k1.Secret, "mock-small")

	var q1 keyprovider.Quota
	eventually(t, "spend via first key", func() error {
		var err error
		if q1, err = g.Quota(ctx, k1.Ref, owner); err != nil {
			return err
		}
		if q1.Used <= 0 {
			return fmt.Errorf("used = %v", q1.Used)
		}
		return nil
	})
	if q1.Limit != 10 {
		t.Errorf("limit = %v, want the 30d allowance on the owner", q1.Limit)
	}

	k2 := g.createKey(t, owner, limits)
	q2, err := g.Quota(ctx, k2.Ref, owner)
	if err != nil {
		t.Fatalf("quota via replacement key: %v", err)
	}
	if q2.Limit != q1.Limit {
		t.Errorf("allowance changed across rotation: %v then %v", q1.Limit, q2.Limit)
	}
	if !near(q2.Used, q1.Used) {
		t.Errorf("spend reset across rotation: %v then %v", q1.Used, q2.Used)
	}

	// Spend through the replacement adds to the same allowance.
	g.mustComplete(t, k2.Secret, "mock-small")
	eventually(t, "spend accumulates across keys", func() error {
		spent2, err := g.TotalSpend(ctx, k2.Ref)
		if err != nil {
			return err
		}
		q3, err := g.Quota(ctx, k2.Ref, owner)
		if err != nil {
			return err
		}
		if spent2 <= 0 || !near(q3.Used, q1.Used+spent2) {
			return fmt.Errorf("used = %v, want %v + %v", q3.Used, q1.Used, spent2)
		}
		return nil
	})
}

// Every shape a profile's windows can take upstream reports the gateway's own
// figures: the widest window on the owner, and nothing, one flat window or
// several in budget_limits on the key.
func TestE2EWindowsByShape(t *testing.T) {
	cases := []struct {
		name    string
		quotas  []keyprovider.QuotaWindow
		periods []string
	}{
		{"no limit", nil, nil},
		{"owner only", []keyprovider.QuotaWindow{{Budget: 20, Period: "7d"}}, []string{"7d"}},
		{"owner and one key window", []keyprovider.QuotaWindow{
			{Budget: 1, Period: "1h"}, {Budget: 20, Period: "7d"},
		}, []string{"1h", "7d"}},
		{"owner and two key windows", []keyprovider.QuotaWindow{
			{Budget: 1, Period: "1h"}, {Budget: 5, Period: "24h"}, {Budget: 20, Period: "7d"},
		}, []string{"1h", "24h", "7d"}},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			g := newE2EGateway(t)
			ctx := context.Background()
			owner := e2eOwner(t)

			k := g.createKey(t, owner, keyprovider.Limits{Quotas: tc.quotas})
			g.mustComplete(t, k.Secret, "mock-small")
			g.mustComplete(t, k.Secret, "mock-small")

			eventually(t, "windows report both requests", func() error {
				windows, err := g.Windows(ctx, k.Ref, owner)
				if err != nil {
					return err
				}
				periods := make([]string, 0, len(windows))
				for _, w := range windows {
					periods = append(periods, w.Period)
				}
				if !slices.Equal(periods, tc.periods) {
					return fmt.Errorf("windows = %v, want %v", periods, tc.periods)
				}
				if len(windows) == 0 {
					return nil
				}

				// A key this fresh has spent the same inside every window it
				// holds, and so has its owner.
				keySpend, err := g.TotalSpend(ctx, k.Ref)
				if err != nil {
					return err
				}
				ownerQuota, err := g.client.UserQuota(ctx, owner)
				if err != nil {
					return err
				}
				widest := tc.periods[len(tc.periods)-1]
				for _, w := range windows {
					want := keySpend
					if w.Period == widest {
						want = ownerQuota.Used
					}
					if !w.UsedKnown || want <= 0 || !near(w.Used, want) {
						return fmt.Errorf("%s window: used=%v known=%v, want %v",
							w.Period, w.Used, w.UsedKnown, want)
					}
					if w.ResetsAt.IsZero() {
						return fmt.Errorf("%s window has no reset time", w.Period)
					}
				}
				return nil
			})
		})
	}
}

// The per-model table labels requests by the public model name and counts
// their tokens.
func TestE2EHistoryByModel(t *testing.T) {
	g := newE2EGateway(t)
	ctx := context.Background()
	k := g.createKey(t, e2eOwner(t), keyprovider.Limits{})

	g.mustComplete(t, k.Secret, "mock-small")
	g.mustComplete(t, k.Secret, "mock-small")
	g.mustComplete(t, k.Secret, "mock-large")

	eventually(t, "history shows both models", func() error {
		h, err := g.History(ctx, k.Ref, 30)
		if err != nil {
			return err
		}
		got := map[string]keyprovider.ModelUsage{}
		for _, m := range h.Models {
			got[m.Model] = m
		}
		if got["mock-small"].Requests != 2 || got["mock-large"].Requests != 1 {
			return fmt.Errorf("models = %+v, want mock-small×2 and mock-large×1", h.Models)
		}
		var total int64
		for _, m := range h.Models {
			if m.TotalTokens <= 0 || m.TotalTokens != m.PromptTokens+m.CompletionTokens {
				return fmt.Errorf("%s tokens don't add up: %+v", m.Model, m)
			}
			total += m.TotalTokens
		}
		if len(h.Days) != 1 || h.Days[0].Tokens != total {
			return fmt.Errorf("days = %+v, want one day of %d tokens", h.Days, total)
		}
		return nil
	})
}

// A quota the portal pushes is one the gateway enforces: on the owner for the
// widest window, which is the one users actually run into.
func TestE2EOwnerQuotaIsEnforced(t *testing.T) {
	g := newE2EGateway(t)
	owner := e2eOwner(t)
	// Room for about two mock-small requests.
	k := g.createKey(t, owner, keyprovider.Limits{Quotas: []keyprovider.QuotaWindow{
		{Budget: 0.1, Period: "30d"},
	}})

	// The last request that starts within the budget may take it over, so
	// allow a request or two past the line before calling it unenforced.
	for i := 1; i <= 6; i++ {
		status, body := g.completeBody(t, k.Secret, "mock-small")
		if status == http.StatusOK {
			time.Sleep(time.Second)
			continue
		}
		if i == 1 {
			t.Fatalf("first request returned %d: %s", status, body)
		}
		return
	}
	t.Fatal("six requests went through on a budget for about two")
}

// A push binds the key to the owner it names, so when a portal starts naming
// its users with LITELLM_USER_ID_PREFIX, the next sync moves each key to the
// prefixed user. Spend from then on counts against the new owner only, and the
// ':' the prefix adds is accepted in a user ID.
func TestE2EUpdateLimitsMovesKeyToNewOwner(t *testing.T) {
	g := newE2EGateway(t)
	ctx := context.Background()
	oldOwner := e2eOwner(t)
	newOwner := "zz-e2e-prefix:" + oldOwner
	limits := keyprovider.Limits{Quotas: []keyprovider.QuotaWindow{{Budget: 10, Period: "30d"}}}

	k := g.createKey(t, oldOwner, limits)
	g.mustComplete(t, k.Secret, "mock-small")
	var before keyprovider.Quota
	eventually(t, "spend on the old owner", func() error {
		var err error
		if before, err = g.Quota(ctx, k.Ref, oldOwner); err != nil {
			return err
		}
		if before.Used <= 0 {
			return fmt.Errorf("used = %v", before.Used)
		}
		return nil
	})

	if err := g.UpdateLimits(ctx, k.Ref, keyprovider.User{ID: newOwner}, limits); err != nil {
		t.Fatalf("update limits with the new owner: %v", err)
	}
	g.mustComplete(t, k.Secret, "mock-small")

	eventually(t, "spend on the new owner", func() error {
		q, err := g.Quota(ctx, k.Ref, newOwner)
		if err != nil {
			return err
		}
		if q.Limit != 10 || q.Used <= 0 {
			return fmt.Errorf("new owner: used %v of %v, want spend against 10", q.Used, q.Limit)
		}
		return nil
	})
	after, err := g.Quota(ctx, k.Ref, oldOwner)
	if err != nil {
		t.Fatal(err)
	}
	if !near(after.Used, before.Used) {
		t.Errorf("old owner's spend moved from %v to %v after the key left it", before.Used, after.Used)
	}
}

// e2eUser reads a user back from the gateway, with the keys it owns.
func (g e2eGateway) e2eUser(t *testing.T, id string) (alias, email string, keys int) {
	t.Helper()
	req, err := http.NewRequest(http.MethodGet,
		g.baseURL+"/user/info?user_id="+url.QueryEscape(id), nil)
	if err != nil {
		t.Fatal(err)
	}
	req.Header.Set("Authorization", "Bearer "+envOr("LITELLM_MASTER_KEY", "sk-local-dev"))
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	var info struct {
		UserInfo struct {
			Alias *string `json:"user_alias"`
			Email *string `json:"user_email"`
		} `json:"user_info"`
		Keys []json.RawMessage `json:"keys"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&info); err != nil {
		t.Fatalf("decode /user/info: %v", err)
	}
	if info.UserInfo.Alias != nil {
		alias = *info.UserInfo.Alias
	}
	if info.UserInfo.Email != nil {
		email = *info.UserInfo.Email
	}
	return alias, email, len(info.Keys)
}

// A user is created labelled, owning no key of LiteLLM's making, and an
// existing user picks up a new name and address.
func TestE2EUpsertUserLabelsTheUser(t *testing.T) {
	g := newE2EGateway(t)
	ctx := context.Background()
	id := e2eOwner(t)
	email := id + "@e2e.invalid"

	if err := g.client.UpsertUser(ctx, keyprovider.User{ID: id, Name: "alice", Email: email}, nil); err != nil {
		t.Fatal(err)
	}
	alias, gotEmail, keys := g.e2eUser(t, id)
	if alias != "alice" || gotEmail != email || keys != 0 {
		t.Errorf("created user: alias %q, email %q, %d keys; want alice, %s, no keys", alias, gotEmail, keys, email)
	}

	renamed := "renamed-" + email
	if err := g.client.UpsertUser(ctx, keyprovider.User{ID: id, Name: "Alice", Email: renamed}, nil); err != nil {
		t.Fatal(err)
	}
	if alias, gotEmail, _ := g.e2eUser(t, id); alias != "Alice" || gotEmail != renamed {
		t.Errorf("updated user: alias %q, email %q; want Alice and %s", alias, gotEmail, renamed)
	}
}

// /user/new refuses a user whose address another one holds, but /user/update
// creates a user it does not find, so the upsert still succeeds and the second
// user carries the same address. A duplicate is therefore not an error here;
// LITELLM_USER_EMAIL is opt-in because LiteLLM's own SSO login matches users
// by address, and then picks one of them.
func TestE2EUpsertUserToleratesADuplicateEmail(t *testing.T) {
	g := newE2EGateway(t)
	ctx := context.Background()
	first := e2eOwner(t)
	email := first + "@e2e.invalid"
	if err := g.client.UpsertUser(ctx, keyprovider.User{ID: first, Email: email}, nil); err != nil {
		t.Fatal(err)
	}

	second := first + "-other"
	if err := g.client.UpsertUser(ctx, keyprovider.User{ID: second, Name: "bob", Email: email}, nil); err != nil {
		t.Fatalf("second user with the same email: %v", err)
	}
	if alias, gotEmail, keys := g.e2eUser(t, second); alias != "bob" || gotEmail != email || keys != 0 {
		t.Errorf("second user: alias %q, email %q, %d keys; want bob, %s, no keys", alias, gotEmail, keys, email)
	}
}

// e2eKeyInfo reads a key's alias and metadata back from the gateway.
func (g e2eGateway) e2eKeyInfo(t *testing.T, ref string) (alias string, metadata map[string]any) {
	t.Helper()
	req, err := http.NewRequest(http.MethodGet, g.baseURL+"/key/info?key="+url.QueryEscape(ref), nil)
	if err != nil {
		t.Fatal(err)
	}
	req.Header.Set("Authorization", "Bearer "+envOr("LITELLM_MASTER_KEY", "sk-local-dev"))
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	var info struct {
		Info struct {
			Alias    string         `json:"key_alias"`
			Metadata map[string]any `json:"metadata"`
		} `json:"info"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&info); err != nil {
		t.Fatalf("decode /key/info: %v", err)
	}
	return info.Info.Alias, info.Info.Metadata
}

// A relabel changes the alias and nothing else; in particular it leaves the
// key's metadata alone, where LiteLLM keeps settings an admin made.
func TestE2EUpdateAliasRelabelsTheKey(t *testing.T) {
	g := newE2EGateway(t)
	ctx := context.Background()
	owner := e2eOwner(t)
	k := g.createKey(t, owner, keyprovider.Limits{})

	if _, metadata := g.e2eKeyInfo(t, k.Ref); len(metadata) != 0 {
		t.Errorf("new key carries metadata %v, want none", metadata)
	}

	// What an admin might set on the key in LiteLLM's UI.
	body, _ := json.Marshal(map[string]any{"key": k.Ref, "metadata": map[string]any{"admin_note": "keep"}})
	req, err := http.NewRequest(http.MethodPost, g.baseURL+"/key/update", bytes.NewReader(body))
	if err != nil {
		t.Fatal(err)
	}
	req.Header.Set("Authorization", "Bearer "+envOr("LITELLM_MASTER_KEY", "sk-local-dev"))
	req.Header.Set("Content-Type", "application/json")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()

	alias := owner + "-relabelled"
	if err := g.UpdateAlias(ctx, k.Ref, alias); err != nil {
		t.Fatal(err)
	}
	got, metadata := g.e2eKeyInfo(t, k.Ref)
	if got != alias {
		t.Errorf("alias = %q, want %q", got, alias)
	}
	if metadata["admin_note"] != "keep" {
		t.Errorf("metadata = %v after the relabel, want the admin's note kept", metadata)
	}
}

// A key deleted in LiteLLM is soft-deleted since 1.105: /key/info still
// answers, with status "deleted", while every other route answers 404. The
// portal must read all of these as a missing key, and a second delete as done.
func TestE2EDeletedKeyIsReportedMissing(t *testing.T) {
	g := newE2EGateway(t)
	ctx := context.Background()
	owner := e2eOwner(t)
	k := g.createKey(t, owner, keyprovider.Limits{})

	if err := g.DeleteKey(ctx, k.Ref); err != nil {
		t.Fatal(err)
	}
	if err := g.DeleteKey(ctx, k.Ref); err != nil {
		t.Errorf("deleting the deleted key again: %v, want success", err)
	}

	for name, call := range map[string]func() error{
		"update limits": func() error { return g.UpdateLimits(ctx, k.Ref, keyprovider.User{ID: owner}, keyprovider.Limits{}) },
		"update alias":  func() error { return g.UpdateAlias(ctx, k.Ref, owner+"-gone") },
		"update expiry": func() error { return g.UpdateExpiry(ctx, k.Ref, time.Now().AddDate(0, 0, 30)) },
		"windows":       func() error { _, err := g.Windows(ctx, k.Ref, ""); return err },
		"key quota":     func() error { _, err := g.Quota(ctx, k.Ref, ""); return err },
	} {
		if err := call(); !errors.Is(err, keyprovider.ErrKeyNotFound) {
			t.Errorf("%s: err = %v, want ErrKeyNotFound", name, err)
		}
	}
}
