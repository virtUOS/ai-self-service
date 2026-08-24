package notify

import (
	"context"
	"fmt"
	"log"
	"time"

	"github.com/virtuos/ai-self-service/internal/database"
)

// DefaultThresholds are how many days before expiry a user is warned.
// Ordered widest-first so a key created inside the window still gets the
// earliest applicable notice rather than only the last-minute one.
var DefaultThresholds = []int{14, 3, 1}

// Reminder warns users whose keys are close to expiring.
type Reminder struct {
	store      *database.Store
	notifier   Notifier
	frontend   string
	thresholds []int
}

func NewReminder(store *database.Store, n Notifier, frontendURL string, thresholds []int) *Reminder {
	if len(thresholds) == 0 {
		thresholds = DefaultThresholds
	}
	return &Reminder{store: store, notifier: n, frontend: frontendURL, thresholds: thresholds}
}

// Run sends any notices due now. It is safe to call repeatedly: a notice is
// recorded per (key, threshold) and skipped thereafter.
func (r *Reminder) Run(ctx context.Context) error {
	for _, days := range r.thresholds {
		keys, err := r.store.KeysExpiringWithin(ctx, days)
		if err != nil {
			return fmt.Errorf("find keys expiring within %dd: %w", days, err)
		}
		for _, k := range keys {
			msg := r.message(k, days)
			if err := r.notifier.Notify(ctx, msg); err != nil {
				// Leave the notice unrecorded so the next run retries.
				log.Printf("notify %s about key %s: %v", k.Email, k.KeyPrefix, err)
				continue
			}
			if err := r.store.MarkExpiryNoticeSent(ctx, k.ID, days); err != nil {
				// A duplicate here means a concurrent run already sent it.
				log.Printf("record expiry notice for key %d: %v", k.ID, err)
			}
		}
	}
	return nil
}

// Start runs the reminder on an interval until ctx is cancelled.
func (r *Reminder) Start(ctx context.Context, every time.Duration) {
	// Run once at startup so a restart does not delay overdue notices.
	if err := r.Run(ctx); err != nil {
		log.Printf("expiry reminder: %v", err)
	}
	t := time.NewTicker(every)
	defer t.Stop()
	for {
		select {
		case <-t.C:
			if err := r.Run(ctx); err != nil {
				log.Printf("expiry reminder: %v", err)
			}
		case <-ctx.Done():
			return
		}
	}
}

func (r *Reminder) message(k database.ExpiringKey, days int) Message {
	when := "in " + pluralDays(days)
	if days == 1 {
		when = "tomorrow"
	}

	name := k.Name
	if name == "" {
		name = k.Email
	}

	body := fmt.Sprintf(`Hello %s,

your AI API key (%s…) expires %s, on %s.

You can extend it for a full period with one click, or generate a
replacement, at:

  %s

If you no longer need the key you can ignore this message; it will stop
working on its own.
`, name, k.KeyPrefix, when, k.ExpiresAt.Format("2006-01-02"), r.frontend)

	return Message{
		To:      k.Email,
		Subject: fmt.Sprintf("Your AI API key expires %s", when),
		Body:    body,
	}
}

func pluralDays(d int) string {
	if d == 1 {
		return "1 day"
	}
	return fmt.Sprintf("%d days", d)
}
