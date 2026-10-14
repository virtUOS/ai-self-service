package litellm

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/virtuos/ai-self-service/internal/keyprovider"
)

// /key/info runs on every dashboard load. A key in its query string ends up in
// the access log of the gateway and of any proxy in front of it. The ref the portal holds is already the key's hash, so it goes upstream
// as it is: hashing it again would address no key at all.
func TestUsageRoutesSendTheRefVerbatim(t *testing.T) {
	const key = "sk-secret-key-info"
	ref := keyHash(key)

	var mu sync.Mutex
	var queries []string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		queries = append(queries, r.URL.RawQuery)
		mu.Unlock()
		switch r.URL.Path {
		case "/key/info":
			if got := r.URL.Query().Get("key"); got != ref {
				t.Errorf("/key/info key = %q, want the ref %q", got, ref)
			}
			w.Write([]byte(`{"info":{}}`))
		default:
			w.Write([]byte(`{}`))
		}
	}))
	defer srv.Close()

	c := NewClient(srv.URL, "mk")
	ctx := context.Background()
	if _, err := c.KeyQuota(ctx, ref); err != nil {
		t.Fatal(err)
	}
	if _, err := NewProvider(c).Windows(ctx, ref, ""); err != nil {
		t.Fatal(err)
	}

	if len(queries) < 2 {
		t.Fatalf("saw %d requests, want at least 2", len(queries))
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
	ref := keyHash("sk-secret-transport")

	srv := httptest.NewServer(http.NotFoundHandler())
	srv.Close()

	_, err := NewClient(srv.URL, "mk").KeyQuota(context.Background(), ref)
	if err == nil {
		t.Fatal("want an error from a closed server")
	}
	msg := err.Error()
	if strings.Contains(msg, ref) {
		t.Errorf("error %q carries the key's hash", msg)
	}
	if !strings.Contains(msg, "/key/info") {
		t.Errorf("error %q no longer names the route", msg)
	}
}

// The portal stores the ref, so it must be the gateway's hash of the key and
// never the key: anyone reading the database would otherwise hold working keys.
func TestCreateKeyRefIsTheHash(t *testing.T) {
	for name, tc := range map[string]struct {
		body    string
		wantRef string
	}{
		"token from the gateway": {`{"key":"sk-new","token":"gateway-token"}`, "gateway-token"},
		"no token, derived":      {`{"key":"sk-new"}`, keyHash("sk-new")},
	} {
		t.Run(name, func(t *testing.T) {
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				w.Write([]byte(tc.body))
			}))
			defer srv.Close()

			got, err := NewProvider(NewClient(srv.URL, "mk")).CreateKey(context.Background(),
				keyprovider.KeyRequest{Alias: "a", ExpiresAt: time.Now().Add(24 * time.Hour)})
			if err != nil {
				t.Fatal(err)
			}
			if got.Secret != "sk-new" {
				t.Errorf("secret = %q, want sk-new", got.Secret)
			}
			if got.Ref != tc.wantRef {
				t.Errorf("ref = %q, want %q", got.Ref, tc.wantRef)
			}
		})
	}
}

// Revoking and extending address the key by its ref, sent as it is.
func TestManagementRoutesSendTheRefVerbatim(t *testing.T) {
	const ref = "0123abcd"
	var bodies []map[string]any
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var body map[string]any
		json.NewDecoder(r.Body).Decode(&body)
		bodies = append(bodies, body)
		w.Write([]byte(`{}`))
	}))
	defer srv.Close()

	c := NewClient(srv.URL, "mk")
	ctx := context.Background()
	if err := c.UpdateKeyExpiry(ctx, ref, time.Now().Add(24*time.Hour)); err != nil {
		t.Fatal(err)
	}
	if err := c.UpdateKeyLimits(ctx, ref, "", keyprovider.Limits{}); err != nil {
		t.Fatal(err)
	}
	if err := c.DeleteKey(ctx, ref); err != nil {
		t.Fatal(err)
	}

	if len(bodies) != 3 {
		t.Fatalf("saw %d requests, want 3", len(bodies))
	}
	for i, b := range bodies[:2] {
		if b["key"] != ref {
			t.Errorf("update %d: key = %v, want %q", i, b["key"], ref)
		}
	}
	if keys, _ := bodies[2]["keys"].([]any); len(keys) != 1 || keys[0] != ref {
		t.Errorf("delete: keys = %v, want [%q]", bodies[2]["keys"], ref)
	}
}
