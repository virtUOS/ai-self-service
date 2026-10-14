package litellm

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/virtuos/ai-self-service/internal/keyprovider"
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
		if _, err := gw.KeyQuota(ctx, "abc"); !errors.Is(err, keyprovider.ErrKeyNotFound) {
			t.Errorf("%s: KeyQuota err = %v, want ErrKeyNotFound", name, err)
		}
		if _, err := gw.keyWindows(ctx, "abc"); !errors.Is(err, keyprovider.ErrKeyNotFound) {
			t.Errorf("%s: keyWindows err = %v, want ErrKeyNotFound", name, err)
		}
	}
}

func testExpiry() time.Time { return time.Now().AddDate(0, 0, 30) }
