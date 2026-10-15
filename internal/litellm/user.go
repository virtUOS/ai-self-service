package litellm

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"time"

	"github.com/virtuos/keyosk/internal/keyprovider"
)

// UserBudget is the allowance carried by a LiteLLM internal user.
//
// An internal user's budget applies to every key that user owns, which is what
// makes it survive a key rotation. A key's own budget does not: generating a
// replacement starts a fresh counter, which is the hole issue #26 reported.
//
// It holds exactly one window. /user/new accepts a budget_limits array and
// echoes it back in its response, but does not store it — reading the user
// back shows only max_budget and budget_duration. Sending stacked windows here
// would enforce one of them while the portal promised several.
type UserBudget struct {
	Budget float64
	Period string
}

// UpsertUser applies a budget to an internal user, creating the user if it
// does not exist yet. The user's alias and email are set from u where given;
// empty fields leave what the user has.
//
// LiteLLM has no documented upsert route: /user/new returns 409 once the user
// exists. Create-then-update on 409 is the order that works for both a first
// key and every later one, and it does not race — a concurrent create simply
// loses and falls through to the update.
//
// /user/new also returns 409 when another user already holds the email. LiteLLM
// 1.105's /user/update then creates the user it does not find, so this still
// succeeds, and the two users share the address. Should a gateway's update
// refuse instead, the error reports both answers so the cause can be told
// apart.
func (c *Client) UpsertUser(ctx context.Context, u keyprovider.User, budget *UserBudget) error {
	payload := map[string]any{"user_id": u.ID}
	if u.Name != "" {
		payload["user_alias"] = u.Name
	}
	if u.Email != "" {
		payload["user_email"] = u.Email
	}

	// Both fields are always sent, null when there is no budget: LiteLLM
	// leaves an omitted field untouched, so a profile that drops its quota
	// would otherwise keep enforcing the allowance it no longer has.
	if budget != nil {
		payload["max_budget"] = budget.Budget
		payload["budget_duration"] = budget.Period
	} else {
		payload["max_budget"] = nil
		payload["budget_duration"] = nil
	}

	updateBody, err := json.Marshal(payload)
	if err != nil {
		return err
	}
	// Without auto_create_key, /user/new also mints a key for the user: one
	// with no alias and no expiry, which the portal never records and so could
	// never revoke.
	payload["auto_create_key"] = false
	createBody, err := json.Marshal(payload)
	if err != nil {
		return err
	}

	resp, err := c.do(ctx, http.MethodPost, "/user/new", createBody)
	if err != nil {
		return err
	}
	defer resp.Body.Close()

	if resp.StatusCode == http.StatusOK {
		return nil
	}
	if resp.StatusCode != http.StatusConflict {
		b, _ := io.ReadAll(resp.Body)
		return fmt.Errorf("LiteLLM /user/new returned %d: %s", resp.StatusCode, b)
	}
	conflict, _ := io.ReadAll(resp.Body)

	// The user already exists, which is the normal path for every key after
	// the first. Re-apply the budget so a profile edit takes effect.
	updateResp, err := c.do(ctx, http.MethodPost, "/user/update", updateBody)
	if err != nil {
		return err
	}
	defer updateResp.Body.Close()

	if updateResp.StatusCode != http.StatusOK {
		b, _ := io.ReadAll(updateResp.Body)
		return fmt.Errorf("LiteLLM /user/update returned %d: %s (after /user/new returned 409: %s)",
			updateResp.StatusCode, b, conflict)
	}
	return nil
}

// userInfoResponse is the part of /user/info the portal reads.
//
// The endpoint also returns every key the user owns; the portal tracks its own
// key, so that list is ignored.
type userInfoResponse struct {
	UserInfo struct {
		Spend          float64  `json:"spend"`
		MaxBudget      *float64 `json:"max_budget"`
		BudgetDuration *string  `json:"budget_duration"`
		BudgetResetAt  *string  `json:"budget_reset_at"`
	} `json:"user_info"`
}

// userWindow is the owner's allowance expressed as a budget window, or nil
// when they have none. The owner holds the widest window since issue #26, so
// it belongs alongside the key's own windows when reporting what binds.
func (c *Client) userWindow(ctx context.Context, userID string) (*budgetWindow, error) {
	q := url.Values{}
	q.Set("user_id", userID)

	resp, err := c.do(ctx, http.MethodGet, "/user/info?"+q.Encode(), nil)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	// A user that was never created has no allowance, which is the normal
	// state for keys issued before owners were tracked.
	if resp.StatusCode == http.StatusNotFound {
		return nil, nil
	}
	if resp.StatusCode != http.StatusOK {
		b, _ := io.ReadAll(resp.Body)
		return nil, fmt.Errorf("LiteLLM /user/info returned %d: %s", resp.StatusCode, b)
	}

	var info userInfoResponse
	if err := json.NewDecoder(resp.Body).Decode(&info); err != nil {
		return nil, fmt.Errorf("decode user info: %w", err)
	}
	if info.UserInfo.MaxBudget == nil || info.UserInfo.BudgetDuration == nil {
		return nil, nil
	}
	return &budgetWindow{
		BudgetDuration: *info.UserInfo.BudgetDuration,
		MaxBudget:      *info.UserInfo.MaxBudget,
		ResetAt:        info.UserInfo.BudgetResetAt,
		Spend:          info.UserInfo.Spend,
		HasSpend:       true,
	}, nil
}

// UserQuota reports a user's consumption against the allowance enforced on
// them, across every key they own.
//
// This replaces the key's own quota as the figure shown to users: it is the
// one that binds after a rotation, and the one a user cannot reset.
func (c *Client) UserQuota(ctx context.Context, userID string) (keyprovider.Quota, error) {
	q := url.Values{}
	q.Set("user_id", userID)

	resp, err := c.do(ctx, http.MethodGet, "/user/info?"+q.Encode(), nil)
	if err != nil {
		return keyprovider.Quota{}, err
	}
	defer resp.Body.Close()

	// A user that has never been created has no usage to report. That is the
	// normal state for a key issued before this became the enforcement point,
	// so it is not an error.
	if resp.StatusCode == http.StatusNotFound {
		return keyprovider.Quota{}, nil
	}
	if resp.StatusCode != http.StatusOK {
		b, _ := io.ReadAll(resp.Body)
		return keyprovider.Quota{}, fmt.Errorf("LiteLLM /user/info returned %d: %s", resp.StatusCode, b)
	}

	var info userInfoResponse
	if err := json.NewDecoder(resp.Body).Decode(&info); err != nil {
		return keyprovider.Quota{}, fmt.Errorf("decode user info: %w", err)
	}

	quota := keyprovider.Quota{Used: info.UserInfo.Spend}
	if info.UserInfo.MaxBudget != nil {
		quota.Limit = *info.UserInfo.MaxBudget
	}
	if info.UserInfo.BudgetResetAt != nil {
		if t, err := time.Parse(time.RFC3339, *info.UserInfo.BudgetResetAt); err == nil {
			quota.ResetsAt = t
		}
	}
	return quota, nil
}
