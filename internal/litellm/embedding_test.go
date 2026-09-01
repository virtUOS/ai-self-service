package litellm

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"
)

// The dashboard shows a curl example per model, and an embedding model takes a
// different endpoint and body than a chat model. The gateway reports which is
// which in model_info.mode, so the portal must read it rather than assume every
// model is a chat model.
func TestEmbeddingModelsReadsModeFromGateway(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte(`{"data":[
			{"model_name":"chat-a","model_info":{"mode":"chat"}},
			{"model_name":"bge-m3","model_info":{"mode":"embedding"}},
			{"model_name":"test/bge-m3","model_info":{"mode":"embedding"}}
		]}`))
	}))
	defer srv.Close()

	got, err := NewClient(srv.URL, "mk").EmbeddingModels(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 2 {
		t.Fatalf("got %d embedding models, want 2: %v", len(got), got)
	}
	for _, want := range []string{"bge-m3", "test/bge-m3"} {
		if !got[want] {
			t.Errorf("%q missing; its curl example would use /chat/completions", want)
		}
	}
	if got["chat-a"] {
		t.Error("chat-a reported as an embedding model")
	}
}

// A model the gateway does not classify must not be treated as an embedding
// model: several models report a null mode, and a chat example is the safe
// default — it is what nearly every model here is.
func TestEmbeddingModelsTreatsUnknownModeAsChat(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte(`{"data":[
			{"model_name":"no-mode"},
			{"model_name":"null-mode","model_info":{"mode":null}}
		]}`))
	}))
	defer srv.Close()

	got, err := NewClient(srv.URL, "mk").EmbeddingModels(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 0 {
		t.Errorf("got %v, want none treated as embedding", got)
	}
}
