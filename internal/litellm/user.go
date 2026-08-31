package litellm

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"time"

	"github.com/virtuos/ai-self-service/internal/keyprovider"
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
	Tokens int64
	Period string
}

// UpsertUser applies a budget to an internal user, creating the user if it
// does not exist yet.
//
// LiteLLM has no single upsert route: /user/new returns 409 once the user
// exists, and /user/update fails if it does not. Create-then-update on 409 is
// the order that works for both a first key and every later one, and it does
// not race — a concurrent create simply loses and falls through to the update.
func (c *Client) UpsertUser(ctx context.Context, userID string, budget *UserBudget) error {
	payload := map[string]any{"user_id": userID}

	// Both fields are always sent, null when there is no budget: LiteLLM
	// leaves an omitted field untouched, so a profile that drops its quota
	// would otherwise keep enforcing the allowance it no longer has.
	if budget != nil {
		payload["max_budget"] = TokensToBudget(budget.Tokens)
		payload["budget_duration"] = budget.Period
	} else {
		payload["max_budget"] = nil
		payload["budget_duration"] = nil
	}

	body, err := json.Marshal(payload)
	if err != nil {
		return err
	}

	resp, err := c.do(ctx, http.MethodPost, "/user/new", body)
	if err != nil {
		return err
	}
	defer resp.Body.Close()

	if resp.StatusCode == http.StatusOK {
		// /user/new does not only create the user: it also mints a key for
		// them and returns it. That key carries no alias and no expiry, and
		// the portal never records it, so leaving it behind strands a live
		// credential that no rotation or deletion here can ever reach.
		//
		// A body that cannot be read or carries no key is not an error: the
		// user exists, which is what this call was for.
		var created struct {
			Key string `json:"key"`
		}
		if err := json.NewDecoder(resp.Body).Decode(&created); err == nil && created.Key != "" {
			if delErr := c.DeleteKey(ctx, created.Key); delErr != nil {
				// The user is created and usable, so failing the whole upsert
				// would block a key the caller can still legitimately get.
				// Report the leak rather than the operation.
				return fmt.Errorf("revoke key minted by /user/new: %w", delErr)
			}
		}
		return nil
	}
	if resp.StatusCode != http.StatusConflict {
		b, _ := io.ReadAll(resp.Body)
		return fmt.Errorf("LiteLLM /user/new returned %d: %s", resp.StatusCode, b)
	}

	// The user already exists, which is the normal path for every key after
	// the first. Re-apply the budget so a profile edit takes effect.
	updateResp, err := c.do(ctx, http.MethodPost, "/user/update", body)
	if err != nil {
		return err
	}
	defer updateResp.Body.Close()

	if updateResp.StatusCode != http.StatusOK {
		b, _ := io.ReadAll(updateResp.Body)
		return fmt.Errorf("LiteLLM /user/update returned %d: %s", updateResp.StatusCode, b)
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

	quota := keyprovider.Quota{UsedTokens: BudgetToTokens(info.UserInfo.Spend)}
	if info.UserInfo.MaxBudget != nil {
		quota.LimitTokens = BudgetToTokens(*info.UserInfo.MaxBudget)
	}
	if info.UserInfo.BudgetResetAt != nil {
		if t, err := time.Parse(time.RFC3339, *info.UserInfo.BudgetResetAt); err == nil {
			quota.ResetsAt = t
		}
	}
	return quota, nil
}
