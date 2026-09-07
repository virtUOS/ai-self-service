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

	h, err := NewClient(srv.URL, "mk").History(context.Background(), "sk-x", 30)
	if err != nil {
		t.Fatal(err)
	}
	got := h.Models
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

// The log's "model" is the deployment name with its provider prefix
// ("openai/Qwen/…"); "model_group" is the public name users pick from the
// dashboard's model list. The table must show the name they know.
func TestModelUsageLabelsRowsByPublicModelName(t *testing.T) {
	now := time.Now().UTC()
	rows := []spendRow{
		{Model: "openai/Qwen/Qwen3.8-27B-FP8", ModelGroup: "Qwen/Qwen3.8-27B-FP8", TotalTokens: 10, StartTime: now.Format(time.RFC3339)},
		{Model: "openai/Qwen/Qwen3.8-27B-FP8", ModelGroup: "Qwen/Qwen3.8-27B-FP8", TotalTokens: 5, StartTime: now.Format(time.RFC3339)},
		// An older gateway may log no group; the deployment name still labels the row.
		{Model: "local/embed", TotalTokens: 3, StartTime: now.Format(time.RFC3339)},
	}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		json.NewEncoder(w).Encode(rows)
	}))
	defer srv.Close()

	got, err := NewClient(srv.URL, "mk").History(context.Background(), "sk-x", 30)
	if err != nil {
		t.Fatal(err)
	}
	if len(got.Models) != 2 {
		t.Fatalf("got %d models, want 2: %+v", len(got.Models), got.Models)
	}
	if got.Models[0].Model != "Qwen/Qwen3.8-27B-FP8" || got.Models[0].TotalTokens != 15 {
		t.Errorf("first = %+v, want the public name with both rows summed", got.Models[0])
	}
	if got.Models[1].Model != "local/embed" {
		t.Errorf("second = %+v, want the deployment name as fallback", got.Models[1])
	}
}
