package litellm

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/virtuos/keyosk/internal/keyprovider"
)

// gatewayAnswering answers every request with the given status and body.
func gatewayAnswering(t *testing.T, status int, body string) *Client {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(status)
		w.Write([]byte(body))
	}))
	t.Cleanup(srv.Close)
	return NewClient(srv.URL, "mk")
}

// What LiteLLM 1.105 answers about a key it does not have.
const (
	updateNotFound = `{"error":{"message":"Key not found.","type":"not_found_error","param":"key","code":"404"}}`
	deleteNotFound = `{"error":{"message":"{'error': 'No keys found'}","type":"internal_server_error","param":null,"code":"404"}}`
	infoNotFound   = `{"error":{"message":"Key not found in database","type":"not_found_error","param":"key","code":"404"}}`
	infoDeleted    = `{"key":"abc","info":{"spend":0.5,"max_budget":1,"status":"deleted"}}`
)

// A delete wants the key gone, and a key LiteLLM no longer has is gone.
func TestDeleteKeyTreatsNotFoundAsDone(t *testing.T) {
	c := gatewayAnswering(t, http.StatusNotFound, deleteNotFound)
	if err := c.DeleteKey(context.Background(), "abc"); err != nil {
		t.Errorf("delete of a missing key failed: %v", err)
	}
}

// Any other failure still fails the delete, so the portal keeps its record of
// a key that may still be live.
func TestDeleteKeyStillFailsOnOtherErrors(t *testing.T) {
	c := gatewayAnswering(t, http.StatusInternalServerError, `{}`)
	if err := c.DeleteKey(context.Background(), "abc"); err == nil {
		t.Error("delete reported success on a 500")
	}
}

// 404s that are not LiteLLM's answer about the key: a proxy in front of the
// gateway, a maintenance page, and LiteLLM's own answer to an unknown route.
var foreignNotFound = map[string]string{
	"html":          `<html><body><h1>404 Not Found</h1></body></html>`,
	"empty":         ``,
	"unknown route": `{"detail":"Not Found"}`,
	"other json":    `{"error":"not found"}`,
	"other code":    `{"error":{"message":"Key not found.","code":"400"}}`,
}

// A 404 that does not come from LiteLLM says nothing about the key, so the
// delete fails and the portal keeps its record of a key that may be live.
func TestDeleteKeyFailsOnAForeignNotFound(t *testing.T) {
	for name, body := range foreignNotFound {
		c := gatewayAnswering(t, http.StatusNotFound, body)
		if err := c.DeleteKey(context.Background(), "abc"); err == nil {
			t.Errorf("%s: delete reported success", name)
		}
	}
}

// Nor does such a 404 report a missing key from any other key route.
func TestForeignNotFoundIsNotAMissingKey(t *testing.T) {
	ctx := context.Background()
	for name, body := range foreignNotFound {
		c := gatewayAnswering(t, http.StatusNotFound, body)
		for route, call := range map[string]func() error{
			"limits": func() error { return c.UpdateKeyLimits(ctx, "abc", "", keyprovider.Limits{}) },
			"alias":  func() error { return c.UpdateKeyAlias(ctx, "abc", "x") },
			"expiry": func() error { return c.UpdateKeyExpiry(ctx, "abc", testExpiry()) },
			"info":   func() error { _, err := c.keyInfo(ctx, "abc"); return err },
		} {
			if err := call(); err == nil || errors.Is(err, keyprovider.ErrKeyNotFound) {
				t.Errorf("%s, %s: err = %v, want a plain failure", name, route, err)
			}
		}
	}
}

// The code may come as a number, and the message may change.
func TestNotFoundEnvelopeIgnoresWording(t *testing.T) {
	c := gatewayAnswering(t, http.StatusNotFound, `{"error":{"message":"Token does not exist","type":"x","code":404}}`)
	err := c.UpdateKeyAlias(context.Background(), "abc", "x")
	if !errors.Is(err, keyprovider.ErrKeyNotFound) {
		t.Errorf("err = %v, want ErrKeyNotFound", err)
	}
}

// Every update of a missing key reports ErrKeyNotFound, so the caller can
// stop retrying it.
func TestKeyUpdatesReportAMissingKey(t *testing.T) {
	c := gatewayAnswering(t, http.StatusNotFound, updateNotFound)
	ctx := context.Background()
	for name, call := range map[string]func() error{
		"limits": func() error { return c.UpdateKeyLimits(ctx, "abc", "", keyprovider.Limits{}) },
		"alias":  func() error { return c.UpdateKeyAlias(ctx, "abc", "x") },
		"expiry": func() error { return c.UpdateKeyExpiry(ctx, "abc", testExpiry()) },
	} {
		if err := call(); !errors.Is(err, keyprovider.ErrKeyNotFound) {
			t.Errorf("%s: err = %v, want ErrKeyNotFound", name, err)
		}
	}
}

// A gateway that fails for another reason must not read as a missing key, or
// the portal would drop the record of a key that still works.
func TestOtherFailuresAreNotAMissingKey(t *testing.T) {
	c := gatewayAnswering(t, http.StatusInternalServerError, `{}`)
	err := c.UpdateKeyLimits(context.Background(), "abc", "", keyprovider.Limits{})
	if err == nil || errors.Is(err, keyprovider.ErrKeyNotFound) {
		t.Errorf("err = %v, want a plain failure", err)
	}
}

// /key/info reports a missing key two ways: 404 for one that never existed or
// predates soft deletion, and a record with status "deleted" since 1.105.
func TestKeyInfoReportsAMissingKey(t *testing.T) {
	for name, gw := range map[string]*Client{
		"404":     gatewayAnswering(t, http.StatusNotFound, infoNotFound),
		"deleted": gatewayAnswering(t, http.StatusOK, infoDeleted),
	} {
		ctx := context.Background()
		if _, err := gw.keyWindows(ctx, "abc"); !errors.Is(err, keyprovider.ErrKeyNotFound) {
			t.Errorf("%s: keyWindows err = %v, want ErrKeyNotFound", name, err)
		}
	}
}

func testExpiry() time.Time { return time.Now().AddDate(0, 0, 30) }

// CheckKey reads the key's record: an answer means the gateway has it, LiteLLM's
// not-found and a "deleted" status mean it does not, and anything else says
// nothing, so the portal keeps what it has.
func TestCheckKey(t *testing.T) {
	for name, tc := range map[string]struct {
		status  int
		body    string
		missing bool
		fails   bool
	}{
		"present":       {status: http.StatusOK, body: `{"key":"abc","info":{"status":"active"}}`},
		"not found":     {status: http.StatusNotFound, body: infoNotFound, missing: true, fails: true},
		"deleted":       {status: http.StatusOK, body: infoDeleted, missing: true, fails: true},
		"foreign 404":   {status: http.StatusNotFound, body: `<html>404</html>`, fails: true},
		"server error":  {status: http.StatusInternalServerError, body: `{}`, fails: true},
		"unreadable ok": {status: http.StatusOK, body: `not json`, fails: true},
	} {
		t.Run(name, func(t *testing.T) {
			p := NewProvider(gatewayAnswering(t, tc.status, tc.body))
			err := p.CheckKey(context.Background(), "abc")
			if (err != nil) != tc.fails {
				t.Fatalf("err = %v, want failure = %v", err, tc.fails)
			}
			if got := errors.Is(err, keyprovider.ErrKeyNotFound); got != tc.missing {
				t.Errorf("err = %v; ErrKeyNotFound = %v, want %v", err, got, tc.missing)
			}
		})
	}
}
