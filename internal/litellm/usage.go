package litellm

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"sort"
	"strconv"
	"time"

	"github.com/virtuos/keyosk/internal/keyprovider"
)

// keyHash is how LiteLLM identifies a key: the SHA-256 of the key itself. It
// is the ref the portal stores, normally taken from /key/generate's token.
func keyHash(key string) string {
	sum := sha256.Sum256([]byte(key))
	return hex.EncodeToString(sum[:])
}

// activityMetrics is the part of a /user/daily/activity total the portal
// reads.
type activityMetrics struct {
	PromptTokens     int64 `json:"prompt_tokens"`
	CompletionTokens int64 `json:"completion_tokens"`
	TotalTokens      int64 `json:"total_tokens"`
	APIRequests      int64 `json:"api_requests"`
}

// dailyActivityPage is one page of /user/daily/activity.
//
// The breakdown holds the same totals several times over (by model, model
// group, provider, endpoint, key); only the model groups are read. They carry
// the public name a request was made with — users know "Qwen/…", not the
// deployment "openai/Qwen/…" it was routed to.
type dailyActivityPage struct {
	Results []struct {
		Date      string          `json:"date"`
		Metrics   activityMetrics `json:"metrics"`
		Breakdown struct {
			ModelGroups map[string]struct {
				Metrics activityMetrics `json:"metrics"`
			} `json:"model_groups"`
		} `json:"breakdown"`
	} `json:"results"`
	Metadata struct {
		HasMore bool `json:"has_more"`
	} `json:"metadata"`
}

// activityPageSize is the most rows /user/daily/activity returns per page.
const activityPageSize = 1000

// activityMaxPages bounds how many pages one History call follows. A page holds
// up to activityPageSize rows of one day, key, model and endpoint each, so a
// month of ordinary use fits on one. The bound only stops a gateway that keeps
// answering has_more from holding the dashboard forever.
const activityMaxPages = 20

// History returns what ownerID consumed over the last days days, across every
// key they have held: per-day token totals and a per-model breakdown.
//
// Both come from the daily totals LiteLLM keeps per user, key and model. They
// are written whether or not per-request logging is on, and are not removed
// with the spend log when it expires. The days are UTC days, as LiteLLM
// buckets them.
func (c *Client) History(ctx context.Context, ownerID string, days int) (keyprovider.History, error) {
	// Without a user the route reports every user's usage.
	if ownerID == "" {
		return keyprovider.History{}, nil
	}

	now := time.Now().UTC()
	q := url.Values{}
	q.Set("user_id", ownerID)
	q.Set("start_date", now.AddDate(0, 0, -days).Format("2006-01-02"))
	q.Set("end_date", now.Format("2006-01-02"))
	q.Set("page_size", strconv.Itoa(activityPageSize))

	// A page ends after a fixed number of rows, not at a day's end, so one day
	// can arrive split over two pages. Totals are summed, never replaced.
	tokensByDay := make(map[string]int64)
	byModel := make(map[string]*keyprovider.ModelUsage)
	for page := 1; ; page++ {
		if page > activityMaxPages {
			return keyprovider.History{}, fmt.Errorf("LiteLLM /user/daily/activity has more than %d pages", activityMaxPages)
		}
		q.Set("page", strconv.Itoa(page))
		body, err := c.dailyActivity(ctx, q)
		if err != nil {
			return keyprovider.History{}, err
		}
		for _, r := range body.Results {
			tokensByDay[r.Date] += r.Metrics.TotalTokens
			for name, g := range r.Breakdown.ModelGroups {
				m, ok := byModel[name]
				if !ok {
					m = &keyprovider.ModelUsage{Model: name}
					byModel[name] = m
				}
				m.Requests += g.Metrics.APIRequests
				m.PromptTokens += g.Metrics.PromptTokens
				m.CompletionTokens += g.Metrics.CompletionTokens
				m.TotalTokens += g.Metrics.TotalTokens
			}
		}
		if !body.Metadata.HasMore {
			break
		}
	}

	var h keyprovider.History
	for day, tokens := range tokensByDay {
		if tokens > 0 {
			h.Days = append(h.Days, keyprovider.DailyUsage{Day: day, Tokens: tokens})
		}
	}
	sort.Slice(h.Days, func(i, j int) bool { return h.Days[i].Day < h.Days[j].Day })

	for _, m := range byModel {
		h.Models = append(h.Models, *m)
	}
	sort.Slice(h.Models, func(i, j int) bool {
		if h.Models[i].TotalTokens != h.Models[j].TotalTokens {
			return h.Models[i].TotalTokens > h.Models[j].TotalTokens
		}
		return h.Models[i].Model < h.Models[j].Model
	})
	return h, nil
}

// dailyActivity fetches one page of /user/daily/activity.
func (c *Client) dailyActivity(ctx context.Context, q url.Values) (dailyActivityPage, error) {
	var body dailyActivityPage

	resp, err := c.do(ctx, http.MethodGet, "/user/daily/activity?"+q.Encode(), nil)
	if err != nil {
		return body, err
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		b, _ := io.ReadAll(resp.Body)
		return body, fmt.Errorf("LiteLLM /user/daily/activity returned %d: %s", resp.StatusCode, b)
	}
	if err := json.NewDecoder(resp.Body).Decode(&body); err != nil {
		return body, fmt.Errorf("decode daily activity: %w", err)
	}
	return body, nil
}

// keyStatusDeleted is the status /key/info reports for a deleted key. Since
// LiteLLM 1.105 a delete is soft: the record stays readable, but the key no
// longer authenticates and every other route answers 404 for it. Earlier
// versions answer 404 here too.
const keyStatusDeleted = "deleted"

// keyInfoResponse is the part of /key/info the portal reads.
type keyInfoResponse struct {
	Info struct {
		Status            string                 `json:"status"`
		Spend             float64                `json:"spend"`
		BudgetLimits      []budgetWindow         `json:"budget_limits"`
		BudgetLimitsUsage map[string]windowSpend `json:"budget_limits_usage"`
		MaxBudget         *float64               `json:"max_budget"`
		BudgetDuration    *string                `json:"budget_duration"`
		BudgetResetAt     *string                `json:"budget_reset_at"`
	} `json:"info"`
}

// keyInfoPath addresses a key by its ref, which is its hash, never the key
// itself: query strings end up in access logs, and LiteLLM accepts either form
// here.
func keyInfoPath(ref string) string {
	q := url.Values{}
	q.Set("key", ref)
	return "/key/info?" + q.Encode()
}

// keyInfo fetches the gateway's own record of a key.
func (c *Client) keyInfo(ctx context.Context, ref string) (keyInfoResponse, error) {
	var info keyInfoResponse

	resp, err := c.do(ctx, http.MethodGet, keyInfoPath(ref), nil)
	if err != nil {
		return info, err
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return info, keyCallError("/key/info", resp)
	}
	if err := json.NewDecoder(resp.Body).Decode(&info); err != nil {
		return info, fmt.Errorf("decode key info: %w", err)
	}
	if info.Info.Status == keyStatusDeleted {
		return info, fmt.Errorf("LiteLLM /key/info reports the key deleted: %w", keyprovider.ErrKeyNotFound)
	}
	return info, nil
}

// UpdateKeyLimits pushes a profile's limits onto a key that already exists.
//
// Creating a key is not the only time its limits change: a user can be moved
// between profiles, and a profile's quota can be edited. Without this the
// portal would advertise a limit the gateway does not enforce.
//
// Fields are sent explicitly rather than omitted when empty. LiteLLM leaves an
// omitted field untouched, so clearing a quota has to send null — otherwise a
// profile that loses its allowance keeps enforcing the previous one.
//
// A non-empty ownerID also binds the key to that internal user. The binding
// is set on every push rather than only at creation, so a key follows its
// owner when the portal's user IDs change (LITELLM_USER_ID_PREFIX); the
// gateway enforces the new owner's allowance from the next request on.
func (c *Client) UpdateKeyLimits(ctx context.Context, ref, ownerID string, l keyprovider.Limits) error {
	payload := map[string]any{
		"key":       ref,
		"tpm_limit": l.TokensPerMinute,
		"rpm_limit": l.RequestsPerMinute,
	}
	if ownerID != "" {
		payload["user_id"] = ownerID
	}

	// An unrestricted profile sends an empty list, not null. /key/update
	// rejects null with "A value is required but not set" — unlike
	// /key/generate, where omitempty drops the field before it reaches the API.
	//
	// Empty must be sent rather than omitted, or a profile that drops its
	// restriction would never clear the old list. Verified against the live
	// gateway: [] clears a restriction, and a key with [] serves every model.
	models := l.Models
	if models == nil {
		models = []string{}
	}
	payload["models"] = models

	// Both shapes are always sent, one of them null: LiteLLM leaves an omitted
	// field untouched, so a key moving between shapes would otherwise keep
	// enforcing the one it no longer uses.
	windows := effectiveWindows(l)
	switch {
	case len(windows) > 1:
		// Several windows: budget_limits, enforced independently upstream.
		limits := make([]map[string]any, 0, len(windows))
		for _, w := range windows {
			limits = append(limits, map[string]any{
				"budget_duration": w.Period,
				"max_budget":      w.Budget,
			})
		}
		payload["budget_limits"] = limits
		payload["max_budget"] = nil
		payload["budget_duration"] = nil
	case len(windows) == 1:
		// One window: the plain pair already works, so leave it alone.
		payload["max_budget"] = windows[0].Budget
		payload["budget_duration"] = windows[0].Period
		payload["budget_limits"] = nil
	default:
		payload["max_budget"] = nil
		payload["budget_duration"] = nil
		payload["budget_limits"] = nil
	}

	body, err := json.Marshal(payload)
	if err != nil {
		return err
	}

	resp, err := c.do(ctx, http.MethodPost, "/key/update", body)
	if err != nil {
		return err
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return keyCallError("/key/update", resp)
	}
	return nil
}

// effectiveWindows drops windows that are not actually limits, so a blank row
// left in the admin form does not become a zero-budget quota upstream.
func effectiveWindows(l keyprovider.Limits) []keyprovider.QuotaWindow {
	out := make([]keyprovider.QuotaWindow, 0, len(l.Quotas))
	for _, q := range l.Quotas {
		if q.Budget > 0 && q.Period != "" {
			out = append(out, q)
		}
	}
	return out
}
