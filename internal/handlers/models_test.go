package handlers

import (
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
)

func formReq(t *testing.T, values url.Values) *http.Request {
	t.Helper()
	r := httptest.NewRequest(http.MethodPost, "/admin/profiles",
		strings.NewReader(values.Encode()))
	r.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	if err := r.ParseForm(); err != nil {
		t.Fatal(err)
	}
	return r
}

// Ticked checkboxes arrive as repeated `model` fields.
func TestSelectedModelsFromCheckboxes(t *testing.T) {
	r := formReq(t, url.Values{"model": {"Qwen/Qwen3.8-27B-FP8", "bge-m3"}})
	got := selectedModels(r)
	if len(got) != 2 || got[0] != "Qwen/Qwen3.8-27B-FP8" || got[1] != "bge-m3" {
		t.Fatalf("got %#v", got)
	}
}

// Nothing ticked means "all models", which the gateway reads from an empty list.
func TestSelectedModelsNoneMeansAll(t *testing.T) {
	if got := selectedModels(formReq(t, url.Values{})); len(got) != 0 {
		t.Fatalf("got %#v, want empty", got)
	}
}

// The free-text field still works where the picker could not render.
func TestSelectedModelsFallsBackToText(t *testing.T) {
	r := formReq(t, url.Values{"models": {"a-model, another-model"}})
	got := selectedModels(r)
	if len(got) != 2 || got[0] != "a-model" || got[1] != "another-model" {
		t.Fatalf("got %#v", got)
	}
}

// Checkboxes win over a stale hidden text field if both somehow arrive.
func TestSelectedModelsPrefersCheckboxes(t *testing.T) {
	r := formReq(t, url.Values{
		"model":  {"real-model"},
		"models": {"typo-model"},
	})
	got := selectedModels(r)
	if len(got) != 1 || got[0] != "real-model" {
		t.Fatalf("got %#v", got)
	}
}
