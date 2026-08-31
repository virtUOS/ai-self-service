package litellm

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"sort"
)

// Pricing is what the gateway charges per token, read from the models it
// actually serves rather than assumed.
//
// A token quota is only exact while every model costs the same: LiteLLM
// enforces one spend cap per key whatever model a request names, so a dearer
// model draws the allowance down faster. When prices disagree the portal can
// still convert, but the figure an admin sets is then a floor rather than an
// exact count, which is why the disagreement is reported rather than averaged.
type Pricing struct {
	// TokenPrice is the rate used to convert a token allowance into a spend
	// cap. It is the dearest rate any model charges, so a quota under-spends
	// rather than letting someone exceed the allowance an admin intended.
	// Zero means the gateway named no priced model and the caller must fall
	// back.
	TokenPrice float64

	// Uniform reports whether every model agrees on one non-zero price. Only
	// then is a token quota exact.
	Uniform bool

	// Outliers are the models that disagree with the cheapest rate, including
	// any priced at zero — those accrue no spend at all, so a quota over them
	// can never trigger.
	Outliers []PriceOutlier
}

// PriceOutlier is one model whose price differs from the rest.
type PriceOutlier struct {
	Model  string
	Input  float64
	Output float64
}

// pricedModel is the part of /model/info this needs.
type pricedModel struct {
	ModelName string `json:"model_name"`
	Params    struct {
		InputCost  float64 `json:"input_cost_per_token"`
		OutputCost float64 `json:"output_cost_per_token"`
	} `json:"litellm_params"`
}

// Pricing reads the per-token prices the gateway charges.
//
// Both directions are considered: a model priced differently for input and
// output breaks the proportionality a token quota rests on just as surely as
// two models disagreeing with each other.
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

// summarisePricing reduces the gateway's models to one rate and the models
// that disagree with it.
func summarisePricing(models []pricedModel) Pricing {
	var p Pricing

	// The cheapest non-zero rate is the baseline: it is what a well-configured
	// deployment charges everywhere, so anything above it is the outlier.
	cheapest := 0.0
	for _, m := range models {
		for _, rate := range []float64{m.Params.InputCost, m.Params.OutputCost} {
			if rate > 0 && (cheapest == 0 || rate < cheapest) {
				cheapest = rate
			}
		}
	}
	if cheapest == 0 {
		return p // nothing priced; the caller falls back
	}

	dearest := cheapest
	for _, m := range models {
		in, out := m.Params.InputCost, m.Params.OutputCost
		if in != cheapest || out != cheapest {
			p.Outliers = append(p.Outliers, PriceOutlier{Model: m.ModelName, Input: in, Output: out})
		}
		if in > dearest {
			dearest = in
		}
		if out > dearest {
			dearest = out
		}
	}

	sort.Slice(p.Outliers, func(i, j int) bool { return p.Outliers[i].Model < p.Outliers[j].Model })
	p.TokenPrice = dearest
	p.Uniform = len(p.Outliers) == 0
	return p
}
