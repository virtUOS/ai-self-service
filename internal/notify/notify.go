// Package notify delivers messages to users about their API keys.
//
// The portal's whole premise is keys that expire, so a user who is never told
// their key is about to die will discover it when a pipeline breaks. Delivery
// is behind an interface because the portal has no mail infrastructure yet:
// Discard is the default, and an SMTP sender can be dropped in later without
// touching the scheduling logic.
package notify

import (
	"context"
	"log"
)

// Message is a notification addressed to one person.
type Message struct {
	To      string
	Subject string
	Body    string
}

// Notifier delivers messages.
type Notifier interface {
	Notify(ctx context.Context, msg Message) error
}

// Discard drops messages, logging that it did. It is the default so the portal
// runs unconfigured without pretending mail was sent.
type Discard struct{}

func (Discard) Notify(_ context.Context, msg Message) error {
	log.Printf("notify: no mail transport configured, dropping message to %s (%q)", msg.To, msg.Subject)
	return nil
}
