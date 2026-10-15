package litellm

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

// Windows reads every figure from the gateway's own counters, for each shape a
// profile's windows can take upstream. The widest window always moves to the
// owner (#26); what stays on the key is then nothing, one window in the flat
// max_budget/budget_duration pair, or several in budget_limits with their
// spend in budget_limits_usage. The fixtures follow LiteLLM 1.105's responses.
//
// None of it may come from the spend log: that is the whole log of the key,
// and downloading it on every dashboard view stalled the gateway (#116).
func TestWindowsByShape(t *testing.T) {
	now := time.Now().UTC()
	hour := now.Add(30 * time.Minute).Format(time.RFC3339)
	day := now.Add(6 * time.Hour).Format(time.RFC3339)
	week := now.Add(72 * time.Hour).Format(time.RFC3339)

	// The person has spent 10 of a weekly 25.
	owner := fmt.Sprintf(`{"user_info":{"spend":10,"max_budget":25,
	  "budget_duration":"7d","budget_reset_at":%q}}`, week)
	// A person the portal never gave an allowance.
	noAllowance := `{"user_info":{"spend":0,"max_budget":null,
	  "budget_duration":null,"budget_reset_at":null}}`

	type want struct {
		period string
		used   float64
		limit  float64
		known  bool
	}
	cases := []struct {
		name string
		key  string
		// user is /user/info's body; empty answers 404, a user never created.
		user string
		want []want
	}{
		{
			name: "no limit",
			key: `{"info":{"status":"active","spend":0.5,"max_budget":null,
			  "budget_duration":null,"budget_reset_at":null,"budget_limits":null}}`,
			user: noAllowance,
			want: nil,
		},
		{
			name: "owner limit only",
			key: `{"info":{"status":"active","spend":0.5,"max_budget":null,
			  "budget_duration":null,"budget_reset_at":null,"budget_limits":null}}`,
			user: owner,
			want: []want{{"7d", 10, 25, true}},
		},
		{
			name: "owner limit and one key limit",
			// One window stays in the flat pair, and the key's spend counter
			// resets with it. budget_limits_usage is absent then.
			key: fmt.Sprintf(`{"info":{"status":"active","spend":0.3,"max_budget":1,
			  "budget_duration":"1h","budget_reset_at":%q,"budget_limits":null}}`, hour),
			user: owner,
			want: []want{{"1h", 0.3, 1, true}, {"7d", 10, 25, true}},
		},
		{
			name: "owner limit and two key limits",
			key: fmt.Sprintf(`{"info":{"status":"active","spend":0.4,"max_budget":null,
			  "budget_duration":null,"budget_reset_at":null,
			  "budget_limits":[
			    {"reset_at":%q,"max_budget":1,"budget_duration":"1h"},
			    {"reset_at":%q,"max_budget":5,"budget_duration":"24h"}],
			  "budget_limits_usage":{"1h":{"current_spend":0.1},"24h":{"current_spend":0.4}}}}`,
				hour, day),
			user: owner,
			want: []want{{"1h", 0.1, 1, true}, {"24h", 0.4, 5, true}, {"7d", 10, 25, true}},
		},
		{
			name: "window missing from the usage",
			// No figure for the 24h window: unknown, not a silent zero.
			key: fmt.Sprintf(`{"info":{"status":"active","spend":0.4,
			  "budget_limits":[
			    {"reset_at":%q,"max_budget":1,"budget_duration":"1h"},
			    {"reset_at":%q,"max_budget":5,"budget_duration":"24h"}],
			  "budget_limits_usage":{"1h":{"current_spend":0.1}}}}`,
				hour, day),
			user: noAllowance,
			want: []want{{"1h", 0.1, 1, true}, {"24h", 0, 5, false}},
		},
		{
			name: "key from before owners were tracked",
			// Its owner does not exist upstream, so every window is on the key.
			key: fmt.Sprintf(`{"info":{"status":"active","spend":2,
			  "budget_limits":[
			    {"reset_at":%q,"max_budget":1,"budget_duration":"1h"},
			    {"reset_at":%q,"max_budget":5,"budget_duration":"7d"}],
			  "budget_limits_usage":{"1h":{"current_spend":0.2},"7d":{"current_spend":2}}}}`,
				hour, week),
			user: "",
			want: []want{{"1h", 0.2, 1, true}, {"7d", 2, 5, true}},
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				switch r.URL.Path {
				case "/key/info":
					w.Write([]byte(tc.key))
				case "/user/info":
					if tc.user == "" {
						http.NotFound(w, r)
						return
					}
					w.Write([]byte(tc.user))
				default:
					t.Errorf("unexpected request to %s", r.URL.Path)
					http.NotFound(w, r)
				}
			}))
			defer srv.Close()

			got, err := NewProvider(NewClient(srv.URL, "mk")).Windows(context.Background(), "h", "owner-1")
			if err != nil {
				t.Fatal(err)
			}
			if len(got) != len(tc.want) {
				t.Fatalf("got %d windows %+v, want %d", len(got), got, len(tc.want))
			}
			for i, w := range tc.want {
				g := got[i]
				if g.Period != w.period || !nearly(g.Used, w.used) || g.Limit != w.limit || g.UsedKnown != w.known {
					t.Errorf("window %d = %s used=%v limit=%v known=%v, want %s used=%v limit=%v known=%v",
						i, g.Period, g.Used, g.Limit, g.UsedKnown, w.period, w.used, w.limit, w.known)
				}
				if g.ResetsAt.IsZero() {
					t.Errorf("%s window has no reset time", g.Period)
				}
			}
		})
	}
}
