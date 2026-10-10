package litellm

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
)

// /key/info runs on every dashboard load. A key in its query string ends up
// in the access log of the gateway and of any proxy in front of it, so the
// key is addressed by its hash, as /spend/logs already is.
func TestKeyInfoSendsTheHashNotTheKey(t *testing.T) {
	const key = "sk-secret-key-info"

	var mu sync.Mutex
	var queries []string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/key/info" {
			mu.Lock()
			queries = append(queries, r.URL.RawQuery)
			mu.Unlock()
			if got := r.URL.Query().Get("key"); got != keyHash(key) {
				t.Errorf("key = %q, want the hash %q", got, keyHash(key))
			}
		}
		w.Write([]byte(`{"info":{}}`))
	}))
	defer srv.Close()

	c := NewClient(srv.URL, "mk")
	ctx := context.Background()
	if _, err := c.KeyQuota(ctx, key); err != nil {
		t.Fatal(err)
	}
	if _, err := c.KeySpend(ctx, key); err != nil {
		t.Fatal(err)
	}
	if _, err := NewProvider(c).Windows(ctx, key, ""); err != nil {
		t.Fatal(err)
	}

	if len(queries) != 3 {
		t.Fatalf("saw %d /key/info requests, want 3", len(queries))
	}
	for _, q := range queries {
		if strings.Contains(q, key) {
			t.Errorf("query %q carries the key", q)
		}
	}
}

// When the gateway is unreachable, the transport error names the URL, and the
// handlers log it. The query must not come along.
func TestTransportErrorOmitsTheQuery(t *testing.T) {
	const key = "sk-secret-transport"

	srv := httptest.NewServer(http.NotFoundHandler())
	srv.Close()

	_, err := NewClient(srv.URL, "mk").KeyQuota(context.Background(), key)
	if err == nil {
		t.Fatal("want an error from a closed server")
	}
	msg := err.Error()
	if strings.Contains(msg, key) || strings.Contains(msg, keyHash(key)) {
		t.Errorf("error %q carries the key or its hash", msg)
	}
	if !strings.Contains(msg, "/key/info") {
		t.Errorf("error %q no longer names the route", msg)
	}
}
