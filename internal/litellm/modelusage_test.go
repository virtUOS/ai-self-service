package litellm

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
)

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
