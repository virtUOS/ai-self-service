package litellm

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

// The owner's window survives a key rotation — that is the whole point of
// holding it on the internal user (#26). Its usage must therefore come from the
// owner's own spend, not from the new key's spend log, which is empty on a key
// that was just issued.
//
// Reading it from the key log made the dashboard draw the widest window at zero
// straight after a regeneration, contradicting the text right above it and
// suggesting a rotation clears the weekly allowance. It does not.
func TestOwnerWindowUsesOwnerSpendNotTheKeyLog(t *testing.T) {
	now := time.Now().UTC()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/spend/logs":
			// A freshly rotated key: nothing logged against it yet.
			w.Write([]byte(`[]`))
		case "/key/info":
			fmt.Fprintf(w, `{"info":{"spend":0,"budget_limits":[
			  {"budget_duration":"1h","max_budget":5,"reset_at":%q}
			]}}`, now.Add(30*time.Minute).Format(time.RFC3339))
		case "/user/info":
			// The person has spent $10 of their $25 weekly allowance.
			fmt.Fprintf(w, `{"user_info":{"spend":10,"max_budget":25,
			  "budget_duration":"7d","budget_reset_at":%q}}`,
				now.Add(72*time.Hour).Format(time.RFC3339))
		default:
			w.Write([]byte(`{}`))
		}
	}))
	defer srv.Close()

	got, err := NewProvider(NewClient(srv.URL, "mk")).Windows(context.Background(), "sk-new", "owner-1")
	if err != nil {
		t.Fatal(err)
	}

	var owner *struct{ Used, Limit int64 }
	for _, w := range got {
		if w.Period == "7d" {
			owner = &struct{ Used, Limit int64 }{w.UsedTokens, w.LimitTokens}
		}
	}
	if owner == nil {
		t.Fatalf("no 7d window in %+v", got)
	}
	if owner.Used == 0 {
		t.Error("owner window shows 0 used after a rotation; its spend survives the key")
	}
	// $10 of a $25 allowance, so 40% of the window's tokens.
	if want := owner.Limit * 10 / 25; owner.Used != want {
		t.Errorf("owner used = %d, want %d (spend $10 of $25)", owner.Used, want)
	}
}

// The key's own windows keep coming from the key: those really do reset with a
// new key, and showing the owner's spend against them would overstate usage.
func TestKeyWindowsStillUseTheKeyLog(t *testing.T) {
	now := time.Now().UTC()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/spend/logs":
			w.Write([]byte(`[]`))
		case "/key/info":
			fmt.Fprintf(w, `{"info":{"spend":0,"budget_limits":[
			  {"budget_duration":"1h","max_budget":5,"reset_at":%q}
			]}}`, now.Add(30*time.Minute).Format(time.RFC3339))
		case "/user/info":
			fmt.Fprintf(w, `{"user_info":{"spend":10,"max_budget":25,
			  "budget_duration":"7d","budget_reset_at":%q}}`,
				now.Add(72*time.Hour).Format(time.RFC3339))
		default:
			w.Write([]byte(`{}`))
		}
	}))
	defer srv.Close()

	got, err := NewProvider(NewClient(srv.URL, "mk")).Windows(context.Background(), "sk-new", "owner-1")
	if err != nil {
		t.Fatal(err)
	}
	for _, w := range got {
		if w.Period == "1h" && w.UsedTokens != 0 {
			t.Errorf("1h window = %d used, want 0: a new key really has spent nothing", w.UsedTokens)
		}
	}
}
