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
