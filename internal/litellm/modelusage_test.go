package litellm

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"
)

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
