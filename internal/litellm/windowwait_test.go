package litellm

import (
	"testing"
	"time"

	"github.com/virtuos/ai-self-service/internal/keyprovider"
)

// A quota window that rolls over between a request and the check reads zero
// against a key that has spent, so the end-to-end tests wait out a boundary
// that is close. One far away, one already passed (a stale reset time) or one
// unknown needs no wait, and with several windows the latest imminent one
// decides.
func TestWindowBoundaryWait(t *testing.T) {
	now := time.Date(2026, 10, 16, 7, 59, 50, 0, time.UTC)
	at := func(d time.Duration) time.Time { return now.Add(d) }
	margin := 90 * time.Second
	for _, tc := range []struct {
		name    string
		windows []keyprovider.WindowUsage
		want    time.Duration
	}{
		{"no windows", nil, 0},
		{"no reset time", []keyprovider.WindowUsage{{Period: "1h"}}, 0},
		{"far away", []keyprovider.WindowUsage{{Period: "1h", ResetsAt: at(40 * time.Minute)}}, 0},
		{"imminent", []keyprovider.WindowUsage{{Period: "1h", ResetsAt: at(10 * time.Second)}},
			10*time.Second + boundarySlack},
		{"already passed", []keyprovider.WindowUsage{{Period: "1h", ResetsAt: at(-10 * time.Second)}}, 0},
		{"the latest imminent one wins", []keyprovider.WindowUsage{
			{Period: "1h", ResetsAt: at(10 * time.Second)},
			{Period: "24h", ResetsAt: at(50 * time.Second)},
			{Period: "7d", ResetsAt: at(72 * time.Hour)},
		}, 50*time.Second + boundarySlack},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if got := windowBoundaryWait(tc.windows, now, margin); got != tc.want {
				t.Errorf("windowBoundaryWait = %v, want %v", got, tc.want)
			}
		})
	}
}

// boundarySlack is how far past a window's reset the wait reaches, so the
// requests land inside the new window rather than on the boundary itself.
const boundarySlack = 2 * time.Second

// windowBoundaryWait is how long to wait so that no window in windows rolls
// over within margin of now. A reset time already in the past is stale, since
// the gateway refreshes it on a timer, and the next boundary is then a whole
// period away.
func windowBoundaryWait(windows []keyprovider.WindowUsage, now time.Time, margin time.Duration) time.Duration {
	var wait time.Duration
	for _, w := range windows {
		if w.ResetsAt.IsZero() {
			continue
		}
		until := w.ResetsAt.Sub(now)
		if until > 0 && until < margin && until+boundarySlack > wait {
			wait = until + boundarySlack
		}
	}
	return wait
}
