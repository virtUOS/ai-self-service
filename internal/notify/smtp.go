package notify

import (
	"context"
	"fmt"
	"net"
	"net/smtp"
	"strings"
	"time"
)

// SMTP sends mail through a relay.
//
// Auth is optional: university relays commonly accept mail from known hosts
// without credentials, so an empty Username means no AUTH is attempted rather
// than an anonymous login.
type SMTP struct {
	Host     string // host:port
	From     string
	Username string
	Password string
}

var _ Notifier = (*SMTP)(nil)

func (s *SMTP) Notify(ctx context.Context, msg Message) error {
	host, _, err := net.SplitHostPort(s.Host)
	if err != nil {
		return fmt.Errorf("invalid SMTP host %q: %w", s.Host, err)
	}

	var auth smtp.Auth
	if s.Username != "" {
		auth = smtp.PlainAuth("", s.Username, s.Password, host)
	}

	body := buildMessage(s.From, msg)

	// smtp.SendMail has no context support, so bound it ourselves rather than
	// letting a hung relay stall the caller indefinitely.
	done := make(chan error, 1)
	go func() {
		done <- smtp.SendMail(s.Host, auth, s.From, []string{msg.To}, body)
	}()

	select {
	case err := <-done:
		if err != nil {
			return fmt.Errorf("send mail to %s: %w", msg.To, err)
		}
		return nil
	case <-ctx.Done():
		return ctx.Err()
	case <-time.After(30 * time.Second):
		return fmt.Errorf("send mail to %s: timed out", msg.To)
	}
}

func buildMessage(from string, msg Message) []byte {
	var b strings.Builder
	fmt.Fprintf(&b, "From: %s\r\n", from)
	fmt.Fprintf(&b, "To: %s\r\n", msg.To)
	fmt.Fprintf(&b, "Subject: %s\r\n", msg.Subject)
	b.WriteString("MIME-Version: 1.0\r\n")
	b.WriteString("Content-Type: text/plain; charset=UTF-8\r\n")
	b.WriteString("\r\n")
	b.WriteString(msg.Body)
	return []byte(b.String())
}
