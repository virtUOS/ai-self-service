# Budget-Denominated Quotas Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Store and enforce profile quotas as spend budgets instead of tokens, show each quota window as a percentage with the cost beside it, drop the remaining-tokens estimate, and add a per-model token breakdown to the usage history.

**Architecture:** The `keyprovider` vocabulary switches its quota fields from `int64` tokens to `float64` budget; the LiteLLM adapter passes budgets through untouched instead of converting; the database gains a migration that converts `profile_quotas.tokens` to `budget`; the admin form takes an amount; the dashboard view model carries percentages and formatted amounts. A new `BUDGET_UNIT` config value labels amounts. The per-day token chart is unchanged; a per-model table is added from the same spend log.

**Tech Stack:** Go 1.26, bun (SQLite), html/template, Go tests (`go test -race ./...`).

**Spec:** `docs/superpowers/specs/2026-09-07-budget-quotas-design.md`

## Global Constraints

- Commit messages: no `Co-Authored-By` lines (user's global CLAUDE.md).
- Every i18n key needs both `DE` and `EN` (`TestAllKeysTranslated`).
- German dashboard prose must not contain the whole words: the, and, once, your, with, reset, requests (`TestDashboardHasNoUntranslatedProse`).
- Quota periods stay `"1h" | "24h" | "7d" | "30d"`.
- `go build ./...` is red from Task 2 until Task 6 finishes, because the `keyprovider` types change first and their consumers are updated package by package. Run the package's own tests at each task; run `go vet ./... && go test -race ./...` at the end of Task 6 and every task after.
- Format amounts with `FormatBudget(amount, unit)` from Task 1 everywhere; never `%f` inline.

## File map

| File | Responsibility after this plan |
|---|---|
| `internal/config/config.go` | reads `BUDGET_UNIT` (default `$`) into `Config.BudgetUnit` |
| `internal/handlers/budgetfmt.go` (new) | `FormatBudget(amount float64, unit string) string` |
| `internal/keyprovider/keyprovider.go` | `QuotaWindow.Budget`, `Quota.Used/Limit`, `WindowUsage.Used/Limit`, `ModelUsage`, `UsageReporter.TotalSpend` + `ModelUsage` |
| `internal/keyprovider/fake.go` | fake reporter updated to the new fields |
| `internal/litellm/quota.go` | periods, ranking, `WidestWindow` only; token conversion and `FormatTokens` removed |
| `internal/litellm/pricing.go` | `Pricing{Unpriced []string}` — models that accrue no spend |
| `internal/litellm/client.go` | no pricing cache; budgets sent as given |
| `internal/litellm/usage.go` | spend rows carry model/prompt/completion/spend; `KeyQuota`, `KeySpend`, `ModelUsage` |
| `internal/litellm/windows.go` | window usage summed as spend |
| `internal/litellm/user.go`, `provider.go` | `UserBudget.Budget`; budgets passed through |
| `internal/database/models.go` | `ProfileQuota.Budget float64` |
| `internal/database/migrations/20240007_budget_quotas.go` (new) | `tokens` → `budget` at the nominal rate |
| `internal/handlers/admin.go` | `parseQuotaWindows` reads `quota_budget`; `fmtBudget` template func |
| `internal/handlers/ui.go`, `usagecache.go` | view model in percent + budget; per-model rows |
| `internal/handlers/lang.go` | `fmtBudget` template helper |
| `web/templates/dashboard.html`, `admin.html`, `web/static/style.css` | rendering |
| `internal/i18n/messages.go` | copy |
| `cmd/server/main.go` | warns about unpriced models only |
| `README.md`, `PLAN.md` | docs |

---

### Task 1: Budget unit config and formatter

**Files:**
- Modify: `internal/config/config.go`
- Create: `internal/handlers/budgetfmt.go`
- Test: `internal/handlers/budgetfmt_test.go`
- Modify: `internal/handlers/lang.go` (add `fmtBudget` to `langFuncs`)

**Interfaces:**
- Produces: `config.Config.BudgetUnit string`; `handlers.FormatBudget(amount float64, unit string) string`; template func `fmtBudget` with the same signature.

- [ ] **Step 1: Write the failing test**

```go
// internal/handlers/budgetfmt_test.go
package handlers

import "testing"

// Amounts are small (a day's allowance can be a few cents), so two decimals
// must not collapse a real figure to 0.00. A one-character symbol goes in
// front like a currency; a word goes after, so "credits" reads naturally.
func TestFormatBudget(t *testing.T) {
	cases := []struct {
		amount float64
		unit   string
		want   string
	}{
		{0.1, "$", "$0.10"},
		{1.5, "€", "€1.50"},
		{0.004, "$", "$0.0040"},
		{0, "$", "$0.00"},
		{2, "credits", "2.00 credits"},
		{0.25, "", "0.25"},
	}
	for _, c := range cases {
		if got := FormatBudget(c.amount, c.unit); got != c.want {
			t.Errorf("FormatBudget(%v, %q) = %q, want %q", c.amount, c.unit, got, c.want)
		}
	}
}
```

- [ ] **Step 2: Run it to verify it fails**

Run: `go test ./internal/handlers/ -run TestFormatBudget`
Expected: FAIL, `undefined: FormatBudget`.

- [ ] **Step 3: Implement**

```go
// internal/handlers/budgetfmt.go
package handlers

import (
	"strconv"
	"unicode"
)

// FormatBudget renders a spend amount with the deployment's unit label.
//
// Two decimals normally. Allowances can be a few cents a day, so an amount
// that would round to 0.00 is shown with four decimals instead of reading as
// nothing. A single non-letter character is a currency symbol and goes in
// front; anything else ("credits") goes after with a space.
func FormatBudget(amount float64, unit string) string {
	s := strconv.FormatFloat(amount, 'f', 2, 64)
	if amount > 0 && s == "0.00" {
		s = strconv.FormatFloat(amount, 'f', 4, 64)
	}
	if unit == "" {
		return s
	}
	if r := []rune(unit); len(r) == 1 && !unicode.IsLetter(r[0]) {
		return unit + s
	}
	return s + " " + unit
}
```

In `internal/config/config.go`: add the field and read it.

```go
	// BudgetUnit labels quota amounts on the dashboard and admin page. It is
	// the currency LiteLLM prices models in, or a word like "credits" when the
	// prices are nominal and should not read as money.
	BudgetUnit string
```
and in `Load()` next to `ListenAddr`:
```go
		BudgetUnit: envOr("BUDGET_UNIT", "$"),
```

In `internal/handlers/lang.go`, inside the `template.FuncMap` returned by `langFuncs()`, add:
```go
		// fmtBudget renders a quota amount with the deployment's unit label.
		"fmtBudget": FormatBudget,
```

- [ ] **Step 4: Run tests**

Run: `go test ./internal/handlers/ -run TestFormatBudget && go test ./internal/config/`
Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add internal/config/config.go internal/handlers/budgetfmt.go internal/handlers/budgetfmt_test.go internal/handlers/lang.go
git commit -m "Add a configurable unit label and formatter for quota amounts"
```

---

### Task 2: Provider vocabulary in budget terms

**Files:**
- Modify: `internal/keyprovider/keyprovider.go`
- Modify: `internal/keyprovider/fake.go`

**Interfaces:**
- Produces (used by every later task):

```go
type QuotaWindow struct {
	Budget float64 // spend allowed per period, in the gateway's pricing unit
	Period string  // "1h" | "24h" | "7d" | "30d"
}

type Quota struct {
	Used     float64   // spend in the current window
	Limit    float64   // allowance; zero means unlimited
	ResetsAt time.Time
}

type WindowUsage struct {
	Period    string
	Used      float64
	Limit     float64
	ResetsAt  time.Time
	UsedKnown bool
}

// ModelUsage is one model's share of a key's consumption.
type ModelUsage struct {
	Model            string
	Requests         int64
	PromptTokens     int64
	CompletionTokens int64
	TotalTokens      int64
}

type UsageReporter interface {
	Usage(ctx context.Context, ref string, days int) ([]DailyUsage, error)
	// ModelUsage returns per-model totals over the given number of days,
	// largest TotalTokens first. Empty when there is no per-request log.
	ModelUsage(ctx context.Context, ref string, days int) ([]ModelUsage, error)
	// TotalSpend is the key's cumulative spend counter, which the gateway
	// keeps whether or not per-request logging is on.
	TotalSpend(ctx context.Context, ref string) (float64, error)
	Windows(ctx context.Context, ref, ownerID string) ([]WindowUsage, error)
	Quota(ctx context.Context, ref, ownerID string) (Quota, error)
}
```

- [ ] **Step 1: Edit `keyprovider.go`**

Replace `QuotaWindow`, `Quota`, `WindowUsage` and the `UsageReporter` interface with the definitions above. Keep every existing doc comment that still applies, and reword these:
- `QuotaWindow`: "One allowance and the period it resets on. Budget is spend, not tokens: LiteLLM enforces spend, and with models priced differently a token figure would not be exact."
- `Quota.Used`/`Limit`: same sentences as today's `UsedTokens`/`LimitTokens` with "tokens" replaced by "spend".
- `WindowUsage.Used`: "consumption since the window last reset, as spend summed from the log".
- Add `ModelUsage` after `DailyUsage`.
- Rename `TotalUsage` → `TotalSpend` and reword its comment: returns spend, and is the fallback when `Usage` is empty.

`DailyUsage` is unchanged.

- [ ] **Step 2: Edit `fake.go`**

- Change `TotalByRef map[string]int64` → `TotalByRef map[string]float64` and the method:
```go
// TotalSpend returns the canned cumulative spend for a key.
func (f *Fake) TotalSpend(_ context.Context, ref string) (float64, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.TotalErr != nil {
		return 0, f.TotalErr
	}
	return f.TotalByRef[ref], nil
}
```
- Add fields next to `UsageByRef`:
```go
	// ModelUsageByRef is what ModelUsage returns per key ref; ModelUsageErr
	// forces it to fail.
	ModelUsageByRef map[string][]ModelUsage
	ModelUsageErr   error
```
and the method:
```go
// ModelUsage returns the canned per-model totals for a key.
func (f *Fake) ModelUsage(_ context.Context, ref string, _ int) ([]ModelUsage, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.ModelUsageErr != nil {
		return nil, f.ModelUsageErr
	}
	return f.ModelUsageByRef[ref], nil
}
```

- [ ] **Step 3: Verify the package builds**

Run: `go build ./internal/keyprovider/ && go vet ./internal/keyprovider/`
Expected: no output. (`go build ./...` is now red in `litellm`, `database` consumers and `handlers`; that is expected until Task 6.)

- [ ] **Step 4: Commit**

```bash
git add internal/keyprovider
git commit -m "Express quota allowances as spend budgets in the provider vocabulary"
```

---

### Task 3: LiteLLM adapter passes budgets through

**Files:**
- Modify: `internal/litellm/quota.go`, `client.go`, `pricing.go`, `usage.go`, `windows.go`, `user.go`, `provider.go`
- Modify tests: `quota_test.go`, `pricing_test.go`, `usage_test.go`, `provider_test.go`, `user_test.go`, `ownerquota_test.go`, `ownerspend_test.go`, `spendfetch_test.go`, `widestwindow_test.go`, `e2e_manual_test.go`, `windows_manual_test.go`
- Test (new): `internal/litellm/modelusage_test.go`

**Interfaces:**
- Consumes: Task 2 types.
- Produces: `litellm.UserBudget{Budget float64; Period string}`; `(*Client).KeySpend(ctx, key) (float64, error)`; `(*Client).ModelUsage(ctx, key, days) ([]keyprovider.ModelUsage, error)`; `Pricing{Unpriced []string}`; `(*Client).Pricing(ctx) (Pricing, error)`. Removed: `NominalTokenPrice`, `TokensToBudget`, `BudgetToTokens` (package and method forms), `FormatTokens`, `RefreshPricing`, `CurrentPricing`, `KeySpendTokens`, `TotalUsage`.

- [ ] **Step 1: Write the failing tests for the new behaviour**

Create `internal/litellm/modelusage_test.go`:

```go
package litellm

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

// Research users want to know which model consumed what. The spend log
// carries the model and the prompt/completion split per request; the portal
// sums them per model over the charted window.
func TestModelUsageAggregatesPerModel(t *testing.T) {
	now := time.Now().UTC()
	rows := []spendRow{
		{Model: "qwen", PromptTokens: 100, CompletionTokens: 50, TotalTokens: 150, StartTime: now.Add(-time.Hour).Format(time.RFC3339)},
		{Model: "qwen", PromptTokens: 10, CompletionTokens: 5, TotalTokens: 15, StartTime: now.Add(-2 * time.Hour).Format(time.RFC3339)},
		{Model: "embed", PromptTokens: 400, CompletionTokens: 0, TotalTokens: 400, StartTime: now.Add(-3 * time.Hour).Format(time.RFC3339)},
		// Older than the window: not counted.
		{Model: "qwen", PromptTokens: 9_999, TotalTokens: 9_999, StartTime: now.AddDate(0, 0, -40).Format(time.RFC3339)},
	}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		json.NewEncoder(w).Encode(rows)
	}))
	defer srv.Close()

	got, err := NewClient(srv.URL, "mk").ModelUsage(context.Background(), "sk-x", 30)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 2 {
		t.Fatalf("got %d models, want 2: %+v", len(got), got)
	}
	// Largest first.
	if got[0].Model != "embed" || got[0].TotalTokens != 400 || got[0].Requests != 1 {
		t.Errorf("first = %+v, want embed/400/1", got[0])
	}
	if got[1].Model != "qwen" || got[1].PromptTokens != 110 || got[1].CompletionTokens != 55 || got[1].TotalTokens != 165 || got[1].Requests != 2 {
		t.Errorf("second = %+v, want qwen 110/55/165 over 2 requests", got[1])
	}
}

// The budget is what the gateway reports, in its own unit. Nothing converts.
func TestKeyQuotaReportsSpendAgainstBudget(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		json.NewEncoder(w).Encode(map[string]any{"info": map[string]any{
			"spend": 0.042, "max_budget": 0.15, "budget_reset_at": "2026-08-26T00:00:00Z",
		}})
	}))
	defer srv.Close()

	got, err := NewClient(srv.URL, "mk").KeyQuota(context.Background(), "sk-x")
	if err != nil {
		t.Fatal(err)
	}
	if got.Limit != 0.15 || got.Used != 0.042 {
		t.Errorf("quota = %+v, want used 0.042 of 0.15", got)
	}
}

// A model priced at zero accrues no spend, so no quota can ever bind on it.
// That is the one pricing fact still worth warning about.
func TestPricingListsUnpricedModels(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte(`{"data":[
		  {"model_name":"free","litellm_params":{"input_cost_per_token":0,"output_cost_per_token":0}},
		  {"model_name":"paid","litellm_params":{"input_cost_per_token":1e-7,"output_cost_per_token":4e-7}},
		  {"model_name":"half","litellm_params":{"input_cost_per_token":0,"output_cost_per_token":4e-7}}
		]}`))
	}))
	defer srv.Close()

	got, err := NewClient(srv.URL, "mk").Pricing(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if len(got.Unpriced) != 1 || got.Unpriced[0] != "free" {
		t.Errorf("Unpriced = %v, want [free]", got.Unpriced)
	}
}
```

- [ ] **Step 2: Run to verify they fail**

Run: `go test ./internal/litellm/ -run 'TestModelUsageAggregatesPerModel|TestKeyQuotaReportsSpendAgainstBudget|TestPricingListsUnpricedModels'`
Expected: build failure (the package no longer compiles against Task 2's types). That is the failing state.

- [ ] **Step 3: Rewrite `quota.go`**

Delete `NominalTokenPrice`, `TokensToBudget`, `BudgetToTokens`, `FormatTokens`, `trimZero`. Keep `ValidQuotaPeriods`, `IsValidQuotaPeriod`, `periodRank`, `WidestWindow` unchanged. Replace the package comment at the top of the file with:

```go
// Quota windows are budgets: the spend a key may accrue per period, in
// whatever unit the gateway prices its models. LiteLLM enforces spend, so
// passing the budget through unchanged is the only exact option once models
// are priced differently.
```

- [ ] **Step 4: Rewrite `pricing.go`**

```go
package litellm

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"sort"
)

// Pricing is the one thing about model prices the portal still checks: which
// models are unpriced. A model priced at zero for both input and output
// accrues no spend, so a budget over it never binds and the user is
// effectively unlimited on that model.
type Pricing struct {
	Unpriced []string
}

// pricedModel is the part of /model/info this needs.
type pricedModel struct {
	ModelName string `json:"model_name"`
	Params    struct {
		InputCost  float64 `json:"input_cost_per_token"`
		OutputCost float64 `json:"output_cost_per_token"`
	} `json:"litellm_params"`
}

// Pricing reads the gateway's model list and reports the unpriced ones.
func (c *Client) Pricing(ctx context.Context) (Pricing, error) {
	resp, err := c.do(ctx, http.MethodGet, "/model/info", nil)
	if err != nil {
		return Pricing{}, err
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		b, _ := io.ReadAll(resp.Body)
		return Pricing{}, fmt.Errorf("LiteLLM /model/info returned %d: %s", resp.StatusCode, b)
	}

	var body struct {
		Data []pricedModel `json:"data"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&body); err != nil {
		return Pricing{}, fmt.Errorf("decode model pricing: %w", err)
	}
	return summarisePricing(body.Data), nil
}

func summarisePricing(models []pricedModel) Pricing {
	var p Pricing
	for _, m := range models {
		if m.Params.InputCost <= 0 && m.Params.OutputCost <= 0 {
			p.Unpriced = append(p.Unpriced, m.ModelName)
		}
	}
	sort.Strings(p.Unpriced)
	return p
}
```

- [ ] **Step 5: Trim `client.go`**

Remove the `mu` and `pricing` fields from `Client` and the `sync` import; delete `RefreshPricing`, `CurrentPricing`, `tokenPrice`, `TokensToBudget`, `BudgetToTokens`. `BudgetWindow`, `KeyParams`, `CreateKey` and the rest stay.

- [ ] **Step 6: Rewrite the budget paths in `usage.go`**

`spendRow` becomes:
```go
type spendRow struct {
	APIKey           string  `json:"api_key"`
	Model            string  `json:"model"`
	PromptTokens     int64   `json:"prompt_tokens"`
	CompletionTokens int64   `json:"completion_tokens"`
	TotalTokens      int64   `json:"total_tokens"`
	Spend            float64 `json:"spend"`
	StartTime        string  `json:"startTime"`
}
```
`Usage` is unchanged. Replace `KeyQuota` and `KeySpendTokens`:
```go
// KeyQuota reports spend against the key's enforced budget, both read from
// the key itself: that is the counter LiteLLM enforces against, and it resets
// on the budget period rather than the 30-day window the log is charted over.
func (c *Client) KeyQuota(ctx context.Context, key string) (keyprovider.Quota, error) {
	info, err := c.keyInfo(ctx, key)
	if err != nil {
		return keyprovider.Quota{}, err
	}
	q := keyprovider.Quota{Used: info.Info.Spend}
	if info.Info.MaxBudget != nil {
		q.Limit = *info.Info.MaxBudget
	}
	if info.Info.BudgetResetAt != nil {
		if t, err := time.Parse(time.RFC3339, *info.Info.BudgetResetAt); err == nil {
			q.ResetsAt = t
		}
	}
	return q, nil
}

// KeySpend is the cumulative spend recorded on the key itself. It is the
// fallback for when per-request spend logging is switched off: the key's
// counter keeps working either way, at the cost of any per-day breakdown.
func (c *Client) KeySpend(ctx context.Context, key string) (float64, error) {
	info, err := c.keyInfo(ctx, key)
	if err != nil {
		return 0, err
	}
	return info.Info.Spend, nil
}

// ModelUsage sums the per-request log per model over the last days,
// largest total first. Same source and same window bound as Usage.
func (c *Client) ModelUsage(ctx context.Context, key string, days int) ([]keyprovider.ModelUsage, error) {
	rows, err := c.spendLog(ctx, key)
	if err != nil {
		return nil, err
	}
	cutoff := time.Now().UTC().AddDate(0, 0, -days).Format("2006-01-02")

	totals := make(map[string]*keyprovider.ModelUsage)
	for _, r := range rows {
		if r.TotalTokens <= 0 || len(r.StartTime) < 10 || r.StartTime[:10] < cutoff {
			continue
		}
		m, ok := totals[r.Model]
		if !ok {
			m = &keyprovider.ModelUsage{Model: r.Model}
			totals[r.Model] = m
		}
		m.Requests++
		m.PromptTokens += r.PromptTokens
		m.CompletionTokens += r.CompletionTokens
		m.TotalTokens += r.TotalTokens
	}

	out := make([]keyprovider.ModelUsage, 0, len(totals))
	for _, m := range totals {
		out = append(out, *m)
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].TotalTokens != out[j].TotalTokens {
			return out[i].TotalTokens > out[j].TotalTokens
		}
		return out[i].Model < out[j].Model
	})
	return out, nil
}
```
In `UpdateKeyLimits`, replace `c.TokensToBudget(w.Tokens)` with `w.Budget` and `c.TokensToBudget(windows[0].Tokens)` with `windows[0].Budget`.

- [ ] **Step 7: `windows.go`**

`spentSince` sums spend:
```go
// spentSince sums the spend recorded at or after start, from an already
// fetched log.
func spentSince(rows []spendRow, start time.Time) float64 {
	var spend float64
	for _, r := range rows {
		t, err := time.Parse(time.RFC3339, r.StartTime)
		if err != nil {
			continue
		}
		if !t.Before(start) {
			spend += r.Spend
		}
	}
	return spend
}
```
In `Windows`: `LimitTokens: p.client.BudgetToTokens(w.MaxBudget)` → `Limit: w.MaxBudget`; `u.UsedTokens = spentSince(rows, t.Add(-d))` → `u.Used = ...`; `u.UsedTokens = p.client.BudgetToTokens(w.Spend)` → `u.Used = w.Spend`. Reword the comment on the derived figure: "Consumption is therefore summed from the spend log" stays true.

- [ ] **Step 8: `user.go` and `provider.go`**

`UserBudget` becomes `{Budget float64; Period string}`; in `UpsertUser`, `payload["max_budget"] = budget.Budget`. In `UserQuota`, `UsedTokens: c.BudgetToTokens(info.UserInfo.Spend)` → `Used: info.UserInfo.Spend` and `quota.LimitTokens = c.BudgetToTokens(*info.UserInfo.MaxBudget)` → `quota.Limit = *info.UserInfo.MaxBudget`.

`provider.go`: `userBudget` returns `&UserBudget{Budget: w.Budget, Period: w.Period}` (keep its nil-for-zero-value check, testing `w.Budget <= 0 || w.Period == ""`). In `toKeyParams`: `budget := windows[0].Budget` and `MaxBudget: w.Budget`. Replace `TotalUsage` with:
```go
// TotalSpend reports the key's cumulative spend counter.
func (p *Provider) TotalSpend(ctx context.Context, ref string) (float64, error) {
	return p.client.KeySpend(ctx, ref)
}

// ModelUsage reports what a key has consumed, per model.
func (p *Provider) ModelUsage(ctx context.Context, ref string, days int) ([]keyprovider.ModelUsage, error) {
	return p.client.ModelUsage(ctx, ref, days)
}
```
`effectiveWindows` in `usage.go` (around line 235) keeps windows with `q.Tokens > 0`; change that to `q.Budget > 0`. Reword the "Priced identically for input and output" comment in `toKeyParams` to: "The allowance is already a spend cap, so it goes to the gateway as is."

- [ ] **Step 9: Update the existing tests**

Mechanical substitutions, file by file:
- `quota_test.go`: delete `TestTokensToBudget`, `TestBudgetToTokensRoundTrip`, `TestFormatTokens`; keep `TestIsValidQuotaPeriod`.
- `pricing_test.go`: delete every test; the new `TestPricingListsUnpricedModels` covers the file. Add one more: an empty `data` list yields `len(Unpriced) == 0`.
- `usage_test.go`: `TestUsageFallsBackToKeySpend` and `TestKeySpendZeroForUnusedKey` call `KeySpend` and compare the raw float the stub returns (`0.042` → `0.042`, zero → `0`). Delete `TestKeyQuotaReportsWindow` (replaced) and change `TestKeyQuotaUnlimited` to assert `got.Limit == 0 && got.Used == 1e-05`. In the `UpdateKeyLimits` tests replace `{Tokens: 10_000, Period: "1h"}` with `{Budget: 0.001, Period: "1h"}` and expected `10_000*NominalTokenPrice` with `0.001`; do the same for the stacked-window tests (`Tokens: N` → `Budget: N * 1e-7` written as a literal, and expected values likewise).
- `provider_test.go`: `{Tokens: 1_000_000, Period: "24h"}` → `{Budget: 0.1, Period: "24h"}`; the assertion already expects `0.1`. `{Tokens: 500_000}` → `{Budget: 0.05}`.
- `user_test.go`: `&UserBudget{Tokens: 1_000_000, Period: "30d"}` → `&UserBudget{Budget: 0.1, Period: "30d"}`; expected `TokensToBudget(1_000_000)` → `0.1`. In `TestUserQuotaReportsSpendAgainstBudget` assert `Used`/`Limit` equal the stub's floats.
- `ownerquota_test.go`: every `Tokens: N` → `Budget: N * 1e-7` as a literal (`1_000` → `0.0001`, `10_000` → `0.001`, `1_000_000` → `0.1`); `TokensToBudget(1_000_000)` → `0.1`. `TestQuotaPrefersTheOwnerAllowance` / `...FallsBack...`: assert on `Used`/`Limit`.
- `ownerspend_test.go`, `spendfetch_test.go`: `spendRow` literals gain `Spend:` (use `TotalTokens * 1e-7`, e.g. `TotalTokens: 100, Spend: 0.00001`); assertions on `UsedTokens` become `Used` against the summed spend (`0.00001` for the 1h window, `0.00006` for 24h in `TestWindowsCountPerWindowFromOneFetch`; compare with a `1e-12` tolerance).
- `widestwindow_test.go`: `Tokens:` → `Budget:` with any positive literals.
- `e2e_manual_test.go`, `windows_manual_test.go` (build-tagged manual tests): `Tokens:` → `Budget:`, `LimitTokens`/`UsedTokens` → `Limit`/`Used`, `%d` → `%v`.

- [ ] **Step 10: Run the package tests**

Run: `go vet ./internal/litellm/ && go test -race ./internal/litellm/`
Expected: PASS.

- [ ] **Step 11: Commit**

```bash
git add internal/litellm
git commit -m "Pass quota budgets through to LiteLLM instead of converting tokens"
```

---

### Task 4: Store budgets, migrate token quotas

**Files:**
- Modify: `internal/database/models.go`, `internal/database/db.go`
- Create: `internal/database/migrations/20240007_budget_quotas.go`
- Test: `internal/database/budgetquotas_test.go` (new); update `profilequotas_test.go`, `stackedquotas_test.go`, `stackedmigrate_test.go`, `profile_test.go`

**Interfaces:**
- Produces: `database.ProfileQuota{ID, ProfileID int64; Budget float64; Period string}`.

- [ ] **Step 1: Write the failing migration test**

```go
// internal/database/budgetquotas_test.go
package database

import (
	"context"
	"math"
	"testing"
	"time"

	"github.com/uptrace/bun/migrate"

	"github.com/virtuos/ai-self-service/internal/database/migrations"
)

// A profile written by the previous release holds its quota in tokens. The
// migration converts at the nominal per-token rate every model was priced at
// (0.0000001), so the enforced cap keeps its size across the upgrade.
func TestMigrationConvertsTokenQuotasToBudgets(t *testing.T) {
	s := testStore(t, "bq1")
	ctx := context.Background()

	// Everything up to, but not including, the budget migration.
	before := migrate.NewMigrations()
	for _, m := range migrations.Migrations.Sorted() {
		if m.Name < "20240007" {
			before.Add(m)
		}
	}
	old := migrate.NewMigrator(s.db, before)
	if err := old.Init(ctx); err != nil {
		t.Fatal(err)
	}
	if _, err := old.Migrate(ctx); err != nil {
		t.Fatal(err)
	}

	now := time.Now()
	if err := s.ExecRaw(ctx, `INSERT INTO profiles (name, is_default, created_at, updated_at) VALUES ('students', 0, ?, ?)`, now, now); err != nil {
		t.Fatal(err)
	}
	if err := s.ExecRaw(ctx, `INSERT INTO profile_quotas (profile_id, tokens, period) VALUES (1, 1500000, '24h')`); err != nil {
		t.Fatal(err)
	}

	// Now the rest, as a real upgrade would run it.
	if err := s.RunMigrations(ctx); err != nil {
		t.Fatal(err)
	}

	got, err := s.GetProfile(ctx, 1)
	if err != nil {
		t.Fatal(err)
	}
	if len(got.Quotas) != 1 {
		t.Fatalf("got %d windows, want 1", len(got.Quotas))
	}
	if math.Abs(got.Quotas[0].Budget-0.15) > 1e-9 || got.Quotas[0].Period != "24h" {
		t.Errorf("window = %+v, want 0.15/24h (1.5M tokens at the nominal rate)", got.Quotas[0])
	}
	if err := s.ExecRaw(ctx, `SELECT tokens FROM profile_quotas`); err == nil {
		t.Error("profile_quotas.tokens still exists after the migration")
	}
}
```

- [ ] **Step 2: Run it to verify it fails**

Run: `go test ./internal/database/ -run TestMigrationConvertsTokenQuotasToBudgets`
Expected: build failure (`Budget` undefined) or, once the model compiles, FAIL because no migration converts.

- [ ] **Step 3: Model and store**

`models.go`:
```go
// ProfileQuota is one allowance window on a profile.
//
// Budget is spend per period in the unit the gateway prices models in, not a
// token count: LiteLLM enforces spend, and with models priced differently a
// token figure could not be exact.
type ProfileQuota struct {
	bun.BaseModel `bun:"table:profile_quotas"`

	ID        int64   `bun:"id,pk,autoincrement"`
	ProfileID int64   `bun:"profile_id,notnull"`
	Budget    float64 `bun:"budget,notnull"`
	Period    string  `bun:"period,notnull"` // "1h" | "24h" | "7d" | "30d"
}
```
`db.go` `SetProfileQuotas`: `if q.Tokens <= 0 || q.Period == ""` → `if q.Budget <= 0 || q.Period == ""`.

- [ ] **Step 4: The migration**

```go
// internal/database/migrations/20240007_budget_quotas.go
package migrations

import (
	"context"
	"fmt"

	"github.com/uptrace/bun"
)

// Quotas move from tokens to spend budgets.
//
// LiteLLM enforces spend. Storing tokens and converting at one price per
// token was exact only while every model cost the same; with models priced
// differently, no single rate turns spend back into a true token count.
// Admins now configure the budget directly and the gateway gets it as is.
//
// Existing rows convert at the nominal rate every model on this deployment
// was priced at, 0.0000001 per token (README, "How usage limits work"), so
// each enforced cap keeps its size across the upgrade. A deployment that had
// changed that rate should check its profile budgets after upgrading.
func init() {
	Migrations.MustRegister(func(ctx context.Context, db *bun.DB) error {
		for _, stmt := range []string{
			`ALTER TABLE profile_quotas ADD COLUMN budget REAL NOT NULL DEFAULT 0`,
			`UPDATE profile_quotas SET budget = tokens * 0.0000001`,
			`ALTER TABLE profile_quotas DROP COLUMN tokens`,
		} {
			if _, err := db.ExecContext(ctx, stmt); err != nil {
				return fmt.Errorf("convert token quotas to budgets: %w", err)
			}
		}
		return nil
	}, func(ctx context.Context, db *bun.DB) error {
		for _, stmt := range []string{
			`ALTER TABLE profile_quotas ADD COLUMN tokens INTEGER NOT NULL DEFAULT 0`,
			`UPDATE profile_quotas SET tokens = CAST(budget / 0.0000001 AS INTEGER)`,
			`ALTER TABLE profile_quotas DROP COLUMN budget`,
		} {
			if _, err := db.ExecContext(ctx, stmt); err != nil {
				return err
			}
		}
		return nil
	})
}
```

- [ ] **Step 5: Update the existing tests**

Every `{Tokens: N, Period: ...}` in the database tests becomes `{Budget: <N × 1e-7 as a literal>, Period: ...}` and each `.Tokens != N` assertion becomes a `.Budget` comparison against the same literal. Concretely: `1_000` → `0.0001`, `50_000` → `0.005`, `100_000` → `0.01`, `1_000_000` → `0.1`, `1_500_000` → `0.15`, `5_000_000` → `0.5`, `1` → `0.0000001`. In `stackedmigrate_test.go`, the raw select becomes `SELECT profile_id, budget, period FROM profile_quotas`.

- [ ] **Step 6: Run the package tests**

Run: `go vet ./internal/database/... && go test -race ./internal/database/...`
Expected: PASS, including the new migration test.

- [ ] **Step 7: Commit**

```bash
git add internal/database
git commit -m "Store profile quotas as spend budgets and migrate token quotas"
```

---

### Task 5: Admin form takes a budget amount

**Files:**
- Modify: `internal/handlers/admin.go` (`parseQuotaWindows`, `checkWindowsBind`, `parseAdminTemplate`, `adminData`, the handler that fills it)
- Modify: `web/templates/admin.html`
- Modify: `internal/i18n/messages.go`
- Test: `internal/handlers/quotaform_test.go`, `render_test.go`

**Interfaces:**
- Consumes: `database.ProfileQuota.Budget`, `FormatBudget`, `fmtBudget`.
- Produces: form fields `quota_budget[]` + `quota_period[]`; `adminData.BudgetUnit string`.

- [ ] **Step 1: Update `quotaform_test.go` to the new field and write the new cases**

Rename every `quota_tokens` form key to `quota_budget`, every `Tokens:` expectation to `Budget:`, and use these values: `"1000"` → `"0.10"` expecting `0.1`, `"1000000"` → `"5"` expecting `5`. Add:

```go
// The amount is money-shaped, so it must accept decimals and reject text.
func TestParseQuotaWindowsReadsDecimals(t *testing.T) {
	got, err := parseQuotaWindows(url.Values{
		"quota_budget": {"0.25"}, "quota_period": {"24h"},
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 1 || got[0].Budget != 0.25 {
		t.Errorf("got %+v, want one 0.25 window", got)
	}
}

func TestParseQuotaWindowsRejectsNonNumericAmount(t *testing.T) {
	if _, err := parseQuotaWindows(url.Values{
		"quota_budget": {"ten"}, "quota_period": {"24h"},
	}); err == nil {
		t.Error("a non-numeric amount was accepted")
	}
}

func TestParseQuotaWindowsRejectsInfiniteAmount(t *testing.T) {
	if _, err := parseQuotaWindows(url.Values{
		"quota_budget": {"Inf"}, "quota_period": {"24h"},
	}); err == nil {
		t.Error("an infinite amount was accepted")
	}
}
```
`TestParseQuotaWindowsRejectsUnreachableWindow` keeps its shape with a shorter window carrying the larger budget (e.g. `1h: 5`, `30d: 1`).

- [ ] **Step 2: Run to verify they fail**

Run: `go test ./internal/handlers/ -run TestParseQuotaWindows`
Expected: build failure in the handlers package (expected at this stage; it turns green in Task 6). Proceed to implement and check compile errors are confined to `ui.go`/`usagecache.go`/`profile_test.go` by reading `go vet ./internal/handlers/ 2>&1 | grep admin.go` — it must print nothing after Step 3.

- [ ] **Step 3: Implement**

`parseQuotaWindows` in `admin.go`:
```go
// parseQuotaWindows reads the repeating quota rows the profile form posts.
//
// Rows are paired by position: quota_budget[i] with quota_period[i]. A blank
// amount is how an admin removes a window, so it is dropped rather than
// stored — a zero budget would read upstream as an allowance of nothing,
// blocking every request. Anything else that is not a positive number is a
// mistake and is rejected rather than silently dropped.
func parseQuotaWindows(form url.Values) ([]database.ProfileQuota, error) {
	amounts := form["quota_budget"]
	periods := form["quota_period"]

	out := make([]database.ProfileQuota, 0, len(amounts))
	seen := make(map[string]bool, len(amounts))

	for i, raw := range amounts {
		raw = strings.TrimSpace(raw)
		if raw == "" {
			continue
		}
		amount, err := strconv.ParseFloat(raw, 64)
		if err != nil || math.IsNaN(amount) || math.IsInf(amount, 0) {
			return nil, fmt.Errorf("invalid quota amount %q", raw)
		}
		if amount <= 0 {
			continue
		}
		period := ""
		if i < len(periods) {
			period = strings.TrimSpace(periods[i])
		}
		if !litellm.IsValidQuotaPeriod(period) || period == "" {
			return nil, fmt.Errorf("invalid quota period %q", period)
		}
		if seen[period] {
			return nil, fmt.Errorf("duplicate quota period %q", period)
		}
		seen[period] = true
		out = append(out, database.ProfileQuota{Budget: amount, Period: period})
	}

	if err := checkWindowsBind(out); err != nil {
		return nil, err
	}
	return out, nil
}
```
Add `"math"` and `"strconv"` to the imports if missing. In `checkWindowsBind`, compare `a.Budget > b.Budget` and format with `%v`.

`parseAdminTemplate`: drop the `fmtTokens` line (`fmtBudget` already comes from `langFuncs`). Add `BudgetUnit string` to `adminData` with the comment "labels quota amounts" and set it from `a.cfg.BudgetUnit` where `adminData` is built (grep `adminData{` in `admin.go`).

`admin.html`:
- Table cell: `{{fmtTokens $q.Tokens}} {{fmtPeriod $q.Period}}` → `{{fmtBudget $q.Budget $.BudgetUnit}} {{fmtPeriod $q.Period}}`.
- Form label: `{{T .Lang "admin.form.quota"}}` stays; the hint line becomes
  `<div class="form-hint">{{T .Lang "admin.form.quota.hint"}} {{T .Lang "admin.form.quota.unit"}} {{.BudgetUnit}}</div>`.
- JS: `quotaRow(budget, period)`: `n.name = 'quota_budget'; n.min = '0'; n.step = 'any'; n.placeholder = {{T .Lang "admin.form.quota.amount"}}; n.value = budget || '';`. `addQuotaRow(budget, period)` and `setQuotaRows` reads `q.Budget`. Update the comment above `QUOTA_PERIODS` to say `quota_budget`.

`messages.go`:
- `"admin.form.quota"`: `{DE: "Nutzungslimit (Kosten)", EN: "Usage limit (cost)"}`
- `"admin.form.quota.hint"`: `{DE: "Ausgabenkontingent pro Zeitraum. Leer bedeutet kein Limit.", EN: "Spend allowance per period. Blank means no limit."}`
- replace `"admin.form.quota.tokens"` with `"admin.form.quota.amount": {DE: "Betrag", EN: "Amount"}`
- add `"admin.form.quota.unit": {DE: "Einheit:", EN: "Unit:"}`
- `"help.usagelimit"`: `{DE: "Wie viel pro Zeitraum ausgegeben werden darf, in der Einheit, in der das Gateway Modelle bepreist. Ist das Kontingent verbraucht, schlagen Anfragen fehl, bis der Zeitraum zurückgesetzt wird. Mehrere Fenster gelten gleichzeitig — etwa 0,10 pro Tag und 2 pro Monat —, das jeweils engste greift. Ohne Fenster gilt kein Limit. Die Zurücksetzung erfolgt zu festen UTC-Zeitpunkten: täglich um Mitternacht UTC, wöchentlich montags, monatlich am 1.", EN: "How much may be spent per period, in the unit the gateway prices models in. Once spent, requests fail until the period resets. Several windows apply at once — say 0.10 per day and 2 per month — and the tightest one binds. No windows means no limit. Resets happen on fixed UTC boundaries: daily at midnight UTC, weekly on Monday, monthly on the 1st."}`

`render_test.go`:
- `TestAdminShowsProfileQuotaWindows`: quotas `{Budget: 0.001, Period: "1h"}, {Budget: 0.1, Period: "30d"}`, `BudgetUnit: "$"`, expect `"$0.0010"` and `"$0.10"`.
- `TestAdminPageFullyGerman`: replace `"Usage limit (tokens)"` with `"Usage limit (cost)"`.

- [ ] **Step 4: Confirm admin.go compiles cleanly**

Run: `go vet ./internal/handlers/ 2>&1 | grep -E 'admin\.go|admin\.html'`
Expected: no lines. (Other files still fail until Task 6.)

- [ ] **Step 5: Commit**

```bash
git add internal/handlers/admin.go internal/handlers/quotaform_test.go internal/handlers/render_test.go web/templates/admin.html internal/i18n/messages.go
git commit -m "Take profile quotas as spend amounts in the admin form"
```

---

### Task 6: Dashboard view model in percent and cost

**Files:**
- Modify: `internal/handlers/ui.go`, `internal/handlers/usagecache.go`
- Modify: `web/templates/dashboard.html`, `web/static/style.css`
- Modify: `internal/i18n/messages.go`
- Modify tests: `usage_test.go`, `quotawindows_test.go`, `profile_test.go`, `profilesync_test.go`, `keyflow_test.go`, `untranslated_test.go`, `render_test.go`

**Interfaces:**
- Consumes: Task 2 types, `usageCache`, `fmtBudget`.
- Produces:
```go
type quotaLine struct{ Budget, Period string }   // Budget pre-formatted

type usageReport struct {
	Days       []keyprovider.DailyUsage
	Models     []keyprovider.ModelUsage
	Total      int64
	Peak       int64
	TotalOnly  bool
	TotalSpend float64 // shown when TotalOnly

	HasQuota bool
	Used     float64
	Limit    float64
	QuotaPct int
	ResetsAt time.Time
	Windows  []quotaWindowView
}

type quotaWindowView struct {
	Period   string
	Label    string
	Used     float64
	Limit    float64
	Pct      int
	ResetsAt time.Time
}
```
and `dashboardData.BudgetUnit string`.

- [ ] **Step 1: Update the handler tests to the new shape**

- `quotawindows_test.go`: `UsedTokens`/`LimitTokens` → `Used`/`Limit` with float literals (`800/1_000` → `0.8/1.0`, `9_590/1_000_000` → `0.00959/1.0`). Drop every `Remaining` assertion; keep `Pct` ones (`0.8/1.0` is still 80%). `TestQuotaPctClampsAtFull` calls `quotaPct(2.0, 1.0)`.
- `usage_test.go`: `TestUserUsageReportsRemaining` becomes `TestUserUsageReportsPercentOfBudget`: quota `{Used: 0.042, Limit: 0.15, ResetsAt: ...}`; assert `HasQuota`, `QuotaPct == 28`, `Used == 0.042`, `Limit == 0.15`. `TestUserUsageUnlimitedHasNoRemaining` → asserts `!HasQuota && QuotaPct == 0`. `TestUserUsageFallsBackToTotal`: `TotalByRef["k"] = 0.0123`; assert `TotalOnly && TotalSpend == 0.0123`. `TestUserUsagePrefersPerDayRows`: `TotalByRef` float. `TestDashboardRendersUsage` unchanged except set `BudgetUnit: "$"` in `base`.
- `profile_test.go` `TestDashboardQuotaRendering`: profiles get `{Budget: 0.15, Period: "24h"}` expecting `quotaLine{Budget: "$0.15", Period: "per day"}`, and `{Budget: 0.01, "24h"}, {Budget: 0.1, "30d"}` expecting `"$0.01"`/`"$0.10"`. `profileQuotaLines` gains a `unit string` parameter (see Step 3); pass `"$"`. `TestProfileLimits`: `Budget: 0.1` and assert `got.Quotas[0].Budget == 0.1`.
- `profilesync_test.go`, `keyflow_test.go`: `Tokens: N` → `Budget:` literals as in Task 4; assertions likewise.
- `untranslated_test.go`: `Quotas: []quotaLine{{Budget: "$0.15", Period: "per day"}}`; add to the `Usage` literal `HasQuota: true, Used: 0.05, Limit: 0.15, QuotaPct: 33` and `Models: []keyprovider.ModelUsage{{Model: "gpt-4o", Requests: 3, PromptTokens: 7, CompletionTokens: 3, TotalTokens: 10}}`, and set `BudgetUnit: "$"`, so the new blocks are examined for English.
- `render_test.go` `TestDashboardDrawsABarPerWindow`: windows `{Period: "1h", Label: "per hour", Used: 0.00095, Limit: 0.001, Pct: 95}` and `{Period: "30d", Label: "per month", Used: 0.00959, Limit: 1, Pct: 1}`, `BudgetUnit: "$"`; expected strings become `"per hour", "per month", "width:95%", "width:1%", "95%", "$0.0010", "$1.00"`. `TestDashboardFallsBackToOneBar`: `Used: 0.00959, Limit: 1, QuotaPct: 1`.

Add a new test in `usage_test.go`:
```go
// The usage card lists what each model consumed, from the same log as the
// chart, so a researcher can see where their tokens went.
func TestUserUsageReportsModels(t *testing.T) {
	fake := keyprovider.NewFake()
	fake.UsageByRef = map[string][]keyprovider.DailyUsage{"k": {{Day: "2026-08-25", Tokens: 10}}}
	fake.ModelUsageByRef = map[string][]keyprovider.ModelUsage{
		"k": {{Model: "qwen", Requests: 2, PromptTokens: 7, CompletionTokens: 3, TotalTokens: 10}},
	}
	got := usageUI(t, fake).userUsage(context.Background(), &database.APIKey{LiteLLMKey: "k"}, "", i18n.EN)
	if len(got.Models) != 1 || got.Models[0].Model != "qwen" || got.Models[0].TotalTokens != 10 {
		t.Errorf("Models = %+v, want the qwen row", got.Models)
	}
}
```

- [ ] **Step 2: Run to verify failure**

Run: `go test ./internal/handlers/ 2>&1 | head -20`
Expected: build errors naming `ui.go` / `usagecache.go`.

- [ ] **Step 3: Implement `ui.go`**

- `quotaLine` → `{Budget, Period string}`. `profileQuotaLines(p *database.Profile, lang i18n.Lang, unit string)`: skip `q.Budget <= 0`, set `Budget: FormatBudget(q.Budget, unit)`. Its call site passes `u.cfg.BudgetUnit`. `dashboardData` gains `BudgetUnit string`, set from `u.cfg.BudgetUnit` in the dashboard handler.
- `usageReport` and `quotaWindowView` as in Interfaces. Update comments: "Used, Limit and QuotaPct describe the current quota window as spend".
- `profileLimits`: `keyprovider.QuotaWindow{Budget: q.Budget, Period: q.Period}`.
- `quotaPct`:
```go
// quotaPct is consumption as a percentage of an allowance, clamped to 100 so
// an over-spent window renders as full rather than overflowing its bar.
func quotaPct(used, limit float64) int {
	if limit <= 0 {
		return 0
	}
	pct := int(used / limit * 100)
	if pct > 100 {
		return 100
	}
	if pct < 0 {
		return 0
	}
	return pct
}
```
- `userUsage`: the quota branch becomes
```go
	if q, err := u.usage.Quota(ctx, k.LiteLLMKey, ownerID); err == nil && q.Limit > 0 {
		rep.HasQuota = true
		rep.Used, rep.Limit, rep.ResetsAt = q.Used, q.Limit, q.ResetsAt
		rep.QuotaPct = quotaPct(q.Used, q.Limit)
	}
```
the binding-window copy becomes `rep.Used, rep.Limit = b.Used, b.Limit; rep.QuotaPct, rep.ResetsAt = b.Pct, b.ResetsAt`, and after `rep.Days = days` add `rep.Models = u.usage.Models(ctx, k.LiteLLMKey)`. The fallback becomes
```go
	if total := u.usage.TotalSpend(ctx, k.LiteLLMKey); total > 0 {
		rep.TotalSpend, rep.TotalOnly = total, true
	}
```
- `quotaWindows`: skip `w.Limit <= 0`; build `quotaWindowView{Period, Label: periodLabel(...), Used: w.Used, Limit: w.Limit, Pct: quotaPct(w.Used, w.Limit), ResetsAt: w.ResetsAt}`. Remove the `remaining` computation and `LimitText`.
- `bindingWindow` unchanged.

- [ ] **Step 4: Implement `usagecache.go`**

`totalEntry.tokens int64` → `spend float64`; `Total` → `TotalSpend(ctx, ref) float64` calling `c.reporter.TotalSpend`. `usageEntry` gains `models []keyprovider.ModelUsage`; `Days` fetches both in one refresh:
```go
	days, err := c.reporter.Usage(ctx, ref, usageWindowDays)
	if err != nil {
		slog.Error("read key usage", "err", err)
		return nil
	}
	models, err := c.reporter.ModelUsage(ctx, ref, usageWindowDays)
	if err != nil {
		// The chart is still worth showing without its breakdown.
		slog.Error("read key model usage", "err", err)
		models = nil
	}
	c.entries[ref] = usageEntry{days: days, models: models, fetchedAt: time.Now()}
	return days
```
and add
```go
// Models returns the cached per-model totals, refreshed together with Days.
func (c *usageCache) Models(ctx context.Context, ref string) []keyprovider.ModelUsage {
	if c.reporter == nil || ref == "" {
		return nil
	}
	c.Days(ctx, ref) // refresh if stale
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.entries[ref].models
}
```
(`Days` takes the lock itself, so `Models` must call it before locking.)

- [ ] **Step 5: `dashboard.html`**

Account card quota lines (`{{$q.Tokens}} {{T $.Lang "dash.tokens"}} {{$q.Period}}`) → `{{$q.Budget}} {{$q.Period}}`. After the `dash.usagelimit.note` paragraph inside `{{if .Quotas}}`, add:
```html
    <p class="text-muted" style="margin-top:.35rem;font-size:.85rem">
      {{T .Lang "dash.quota.costnote"}}
    </p>
```
Usage card, per-window block:
```html
      {{range .Usage.Windows}}
        <p style="margin-bottom:.35rem">
          <strong>{{.Pct}}%</strong> {{T $.Lang "dash.quota.used"}}
          <span class="text-muted">· {{.Label}}</span>
        </p>
        <div class="quota-bar" role="img"
             aria-label="{{.Pct}}% {{T $.Lang "dash.quota.used"}}">
          <div class="quota-fill{{if ge .Pct 90}} quota-full{{end}}"
               style="width:{{.Pct}}%"></div>
        </div>
        <p class="text-muted" style="font-size:.85rem;margin-bottom:1rem">
          {{fmtBudget .Used $.BudgetUnit}} {{T $.Lang "dash.quota.of"}}
          {{fmtBudget .Limit $.BudgetUnit}}{{if not .ResetsAt.IsZero}} ·
          {{T $.Lang "dash.quota.resets"}}
          <span class="reset-at" data-reset="{{.ResetsAt.Format "2006-01-02T15:04:05Z"}}"></span>{{end}}
        </p>
      {{end}}
```
Fallback block (`{{else if .Usage.HasQuota}}`): same markup using `.Usage.QuotaPct`, `.Usage.Used`, `.Usage.Limit`, `.Usage.ResetsAt`, with `{{T .Lang ...}}` and `.BudgetUnit`. Update the template comment: "Both figures are spend, straight from the gateway."

TotalOnly block:
```html
    {{if .Usage.TotalOnly}}
      <p style="margin-bottom:.5rem">
        <strong>{{fmtBudget .Usage.TotalSpend .BudgetUnit}}</strong>
        <span class="text-muted">({{T .Lang "dash.usagestats.total"}})</span>
      </p>
      <p class="text-muted" style="font-size:.85rem">
        {{T .Lang "dash.usagestats.nobreakdown"}}
      </p>
```
After the `usage-chart` div (inside the `{{else if .Usage.Days}}` branch), add:
```html
      {{if .Usage.Models}}
      <h3 class="usage-models-title">{{T .Lang "dash.usagestats.bymodel"}}</h3>
      <div class="table-wrap">
        <table class="usage-models">
          <thead>
            <tr>
              <th>{{T .Lang "dash.col.model"}}</th>
              <th>{{T .Lang "dash.col.requests"}}</th>
              <th>{{T .Lang "dash.col.input"}}</th>
              <th>{{T .Lang "dash.col.output"}}</th>
              <th>{{T .Lang "dash.col.total"}}</th>
            </tr>
          </thead>
          <tbody>
            {{range .Usage.Models}}
            <tr>
              <td><code>{{.Model}}</code></td>
              <td>{{thousands .Requests}}</td>
              <td>{{thousands .PromptTokens}}</td>
              <td>{{thousands .CompletionTokens}}</td>
              <td>{{thousands .TotalTokens}}</td>
            </tr>
            {{end}}
          </tbody>
        </table>
      </div>
      {{end}}
```
Remove the now-unused `add` template function from `lang.go`.

`style.css`, after `.usage-label`:
```css
.usage-models-title {
  margin-top: 1rem;
  font-size: .95rem;
}

.usage-models td:not(:first-child),
.usage-models th:not(:first-child) {
  text-align: right;
  white-space: nowrap;
}
```

- [ ] **Step 6: `messages.go`**

Add:
```go
	"dash.quota.costnote":     {DE: "Das Kontingent wird in Kosten gemessen, nicht in Tokens: Teurere Modelle verbrauchen es schneller.", EN: "Your allowance is measured in cost, not tokens: pricier models use it up faster."},
	"dash.usagestats.bymodel": {DE: "Nach Modell", EN: "By model"},
	"dash.col.model":          {DE: "Modell", EN: "Model"},
	"dash.col.requests":       {DE: "Anfragen", EN: "Requests"},
	"dash.col.input":          {DE: "Eingabe-Tokens", EN: "Input tokens"},
	"dash.col.output":         {DE: "Ausgabe-Tokens", EN: "Output tokens"},
	"dash.col.total":          {DE: "Gesamt", EN: "Total"},
```
Change:
- `"dash.usagestats.nobreakdown"`: `{DE: "Gesamtausgaben dieses Schlüssels. Eine Aufschlüsselung nach Tagen ist derzeit nicht verfügbar.", EN: "Total spent by this key. A per-day breakdown is not available at the moment."}`
- `"dash.profile.help"`: replace "wie viele Token Sie pro Zeitraum verbrauchen dürfen" with "wie viel Sie pro Zeitraum verbrauchen dürfen", and "how many tokens you may use per period" with "how much you may use per period".
Remove `"dash.quota.remaining"` (no longer referenced; check with `grep -rn quota.remaining web internal`).

- [ ] **Step 7: Build, vet, test everything**

Run: `go build ./... && go vet ./... && go test -race ./...`
Expected: `cmd/server` fails to build on `RefreshPricing`/`CurrentPricing` — fix it now rather than in a separate task: in `cmd/server/main.go` replace the pricing block with
```go
	// A model priced at zero accrues no spend, so a budget over it never
	// binds. Say so at startup; nothing else about prices needs checking now
	// that quotas are budgets and differing prices are the expected state.
	if p, err := gateway.Pricing(ctx); err != nil {
		slog.Warn("could not read model pricing", "err", err)
	} else {
		for _, m := range p.Unpriced {
			slog.Warn("model is unpriced; quotas do not bind on it", "model", m)
		}
	}
```
Then re-run. Expected: all PASS.

- [ ] **Step 8: Commit**

```bash
git add internal/handlers internal/i18n web cmd/server/main.go
git commit -m "Show each quota window as a percentage with its cost, and usage per model"
```

---

### Task 7: Documentation

**Files:**
- Modify: `README.md` ("How usage limits work", "Usage reporting", configuration table if there is one)
- Modify: `PLAN.md` (add a dated entry)
- Modify: `internal/database/migrations/20240003_profile_quotas.go` comment (one line noting the later reversal)

- [ ] **Step 1: README**

Replace the first two paragraphs of "How usage limits work" with:

> Admins configure quotas as **spend budgets** per period, in the unit LiteLLM prices its models in (`BUDGET_UNIT` labels them on the page; default `$`). LiteLLM enforces spend directly, so the figure an admin enters is the figure the gateway enforces, whatever mix of models a key uses. A model priced at `0` or `null` accrues no spend, so a budget never binds on it; the server warns about such models at startup.
>
> Quotas used to be stored in tokens and converted at one nominal price. That was exact only while every model cost the same per token. Migration `20240007` converts existing token quotas at that nominal rate (0.0000001 per token) so enforced caps keep their size; deployments that priced models differently should review profile budgets after upgrading.

In "Usage reporting", change the "Against the quota" bullet to say the figure is spend and is shown as a percentage of the budget with the amounts beside it, and add a bullet: "**Per model, over 30 days** — the same log summed by model, with the prompt/completion split." Change the `disable_spend_logs` bullet: "falls back to the key's cumulative spend and hides the chart and the per-model table".

In the profile field table (README line ~113) change the "Usage limit" row to "Spend allowance per period, in `BUDGET_UNIT` (blank = unlimited); several windows may apply at once". Add a `BUDGET_UNIT` row to the environment variable table (README line ~46, next to `KEY_DURATION_DAYS`): "Unit label for quota amounts; default `$`. Use a word such as `credits` when model prices are nominal."

- [ ] **Step 2: PLAN.md**

Under the most recent dated section add:

> ### Quotas as budgets (2026-09-07)
>
> Quotas are stored and shown as spend, not tokens, because LiteLLM enforces spend and the token conversion stopped being exact once models could be priced differently. The dashboard leads with the percentage of each window used and shows the amounts beside it; the remaining-tokens figure is gone because it was only a guess. The token chart stays and gained a per-model table. Schema change: migration `20240007` (`profile_quotas.tokens` → `budget`), so the upgrade needs the image and a migration run; the down migration converts back at the nominal rate.

- [ ] **Step 3: Migration 20240003 comment**

Append to its doc comment: `// Superseded by 20240007, which stores budgets after all: see that file.`

- [ ] **Step 4: Verify and commit**

Run: `go vet ./... && go test -race ./...`
Expected: PASS.

```bash
git add README.md PLAN.md internal/database/migrations/20240003_profile_quotas.go
git commit -m "Document budget-denominated quotas"
```

---

## Self-review

**Spec coverage.** Decision 1 (budgets) → Tasks 2–5. Decision 2 (percentage headline) and 3 (cost beside it, `BUDGET_UNIT`) → Tasks 1, 6. Decision 4 (no remaining tokens) → Task 6 removes `Remaining` and the key. Decision 5 (token chart + per-model table) → Tasks 2, 3, 6. Decision 6 (total-only as spend) → Tasks 2, 3, 6. Decision 7 (migration at nominal rate) → Task 4. Decision 8 (unpriced warning) → Tasks 3, 6 step 7. Docs → Task 7.

**Type consistency.** `QuotaWindow.Budget`, `Quota.Used/Limit`, `WindowUsage.Used/Limit`, `ModelUsage{Model, Requests, PromptTokens, CompletionTokens, TotalTokens}`, `TotalSpend`, `UserBudget.Budget`, `ProfileQuota.Budget`, `quotaLine.Budget`, `usageReport.TotalSpend/Models`, `quotaWindowView{Used, Limit, Pct}`, `FormatBudget`/`fmtBudget`, `Config.BudgetUnit`, `adminData.BudgetUnit`, `dashboardData.BudgetUnit` are used with those exact names throughout.

**Known rough edge, stated on purpose.** The spend log is fetched for `Usage`, again for `ModelUsage`, and again uncached in `Windows`. The first two are cached for 60 s per key; the third already ran per page load before this plan. Folding them into one fetch is a follow-up, not part of this change.
