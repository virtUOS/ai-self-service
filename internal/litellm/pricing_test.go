package litellm

import (
	"context"
	"math"
	"net/http"
	"net/http/httptest"
	"testing"
)

// nearly compares floats that are equal in value but need not be bit-identical
// after multiplication.
func nearly(a, b float64) bool { return math.Abs(a-b) < 1e-12 }

const modelInfoBody = `{"data":[
  {"model_name":"a","litellm_params":{"input_cost_per_token":1e-07,"output_cost_per_token":1e-07}},
  {"model_name":"b","litellm_params":{"input_cost_per_token":1e-07,"output_cost_per_token":1e-07}}
]}`

// A token quota is converted to the spend cap LiteLLM enforces using the price
// the gateway actually charges, rather than a constant compiled into the
// portal that nothing keeps in step.
func TestPricingReadsTheGatewayPrice(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte(modelInfoBody))
	}))
	defer srv.Close()

	p, err := NewClient(srv.URL, "mk").Pricing(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if p.TokenPrice != 1e-07 {
		t.Errorf("price = %v, want 1e-07", p.TokenPrice)
	}
	if !p.Uniform {
		t.Error("prices agree, so the pricing should report as uniform")
	}
}

// The quota is exact only while every model costs the same per token: LiteLLM
// enforces one spend cap per key whatever model is used, so a dearer model
// eats the allowance faster. A disagreement has to be reported, not averaged
// away — that is how Ornith's 5x output price went unnoticed.
func TestPricingReportsDisagreement(t *testing.T) {
	body := `{"data":[
	  {"model_name":"cheap","litellm_params":{"input_cost_per_token":1e-07,"output_cost_per_token":1e-07}},
	  {"model_name":"dear","litellm_params":{"input_cost_per_token":1e-07,"output_cost_per_token":5e-07}}
	]}`
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte(body))
	}))
	defer srv.Close()

	p, err := NewClient(srv.URL, "mk").Pricing(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if p.Uniform {
		t.Error("prices differ, so the pricing must not report as uniform")
	}
	if len(p.Outliers) != 1 || p.Outliers[0].Model != "dear" {
		t.Errorf("outliers = %+v, want the dear model named", p.Outliers)
	}
	// The cheapest price is used, so a quota under-spends rather than letting
	// someone exceed the allowance an admin thought they were setting.
	if p.TokenPrice != 5e-07 {
		t.Errorf("price = %v, want the dearest so the cap is not overshot", p.TokenPrice)
	}
}

// A model priced at zero accrues no spend, so a quota over it can never
// trigger. It must be called out rather than dragging the price to zero.
func TestPricingFlagsUnpricedModels(t *testing.T) {
	body := `{"data":[
	  {"model_name":"priced","litellm_params":{"input_cost_per_token":1e-07,"output_cost_per_token":1e-07}},
	  {"model_name":"free","litellm_params":{"input_cost_per_token":0,"output_cost_per_token":0}}
	]}`
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte(body))
	}))
	defer srv.Close()

	p, err := NewClient(srv.URL, "mk").Pricing(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if p.Uniform {
		t.Error("an unpriced model must not read as uniform pricing")
	}
	if p.TokenPrice != 1e-07 {
		t.Errorf("price = %v, want the priced model's rate, not zero", p.TokenPrice)
	}
	var named bool
	for _, o := range p.Outliers {
		if o.Model == "free" {
			named = true
		}
	}
	if !named {
		t.Error("the unpriced model was not named as an outlier")
	}
}

// A gateway that reports no models at all leaves nothing to price from, so the
// caller has to fall back rather than divide by zero.
func TestPricingEmptyGateway(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte(`{"data":[]}`))
	}))
	defer srv.Close()

	p, err := NewClient(srv.URL, "mk").Pricing(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if p.TokenPrice != 0 {
		t.Errorf("price = %v, want 0 so the caller knows to fall back", p.TokenPrice)
	}
}

// The conversion uses the gateway's own price, so a deployment that prices its
// models differently is converted correctly rather than against a constant
// compiled into the portal.
func TestClientConvertsAtTheGatewayPrice(t *testing.T) {
	// Ten times the nominal rate: a token is worth ten times as much, so the
	// same allowance costs ten times as much budget.
	body := `{"data":[{"model_name":"a","litellm_params":{"input_cost_per_token":1e-06,"output_cost_per_token":1e-06}}]}`
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte(body))
	}))
	defer srv.Close()

	c := NewClient(srv.URL, "mk")
	if err := c.RefreshPricing(context.Background()); err != nil {
		t.Fatal(err)
	}

	if got := c.TokensToBudget(1_000_000); !nearly(got, 1.0) {
		t.Errorf("1M tokens = %v, want 1.0 at 1e-06", got)
	}
	if got := c.BudgetToTokens(1.0); got != 1_000_000 {
		t.Errorf("$1 = %d tokens, want 1,000,000 at 1e-06", got)
	}
}

// Before the first refresh, and when the gateway cannot be reached, the
// conversion has to keep working: a dashboard that cannot price a quota is
// worse than one priced at the rate the deployment is expected to use.
func TestClientFallsBackToTheNominalPrice(t *testing.T) {
	c := NewClient("http://127.0.0.1:1", "mk") // nothing listening

	if got := c.TokensToBudget(1_000_000); !nearly(got, 1_000_000*NominalTokenPrice) {
		t.Errorf("unrefreshed conversion = %v, want the nominal rate", got)
	}

	// A failed refresh must not zero the price and make every quota free.
	if err := c.RefreshPricing(context.Background()); err == nil {
		t.Error("expected an error from an unreachable gateway")
	}
	if got := c.TokensToBudget(1_000_000); !nearly(got, 1_000_000*NominalTokenPrice) {
		t.Errorf("after a failed refresh = %v, want the nominal rate", got)
	}
}

// A gateway serving no priced model must not drag the rate to zero, which
// would make every quota cost nothing and so never bind.
func TestClientIgnoresAnUnpricedGateway(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte(`{"data":[{"model_name":"a","litellm_params":{}}]}`))
	}))
	defer srv.Close()

	c := NewClient(srv.URL, "mk")
	if err := c.RefreshPricing(context.Background()); err != nil {
		t.Fatal(err)
	}
	if got := c.TokensToBudget(1_000_000); !nearly(got, 1_000_000*NominalTokenPrice) {
		t.Errorf("conversion = %v, want the nominal rate rather than zero", got)
	}
}
