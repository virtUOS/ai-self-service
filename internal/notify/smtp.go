package notify

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"mime"
	"mime/quotedprintable"
	"net"
	"net/mail"
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

	body, err := buildMessage(s.From, msg, time.Now())
	if err != nil {
		return err
	}

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

// buildMessage writes msg as an RFC 5322 message.
//
// Headers must be ASCII, since smtp.SendMail does not negotiate SMTPUTF8, so
// the subject is sent as RFC 2047 encoded-words: raw UTF-8 there ("Schlüssel")
// shows garbled in some clients. The body is quoted-printable for the same
// reason. Date and Message-ID are set here rather than left to the relay,
// because spam filters score their absence.
func buildMessage(from string, msg Message, now time.Time) ([]byte, error) {
	// To comes from the identity provider. A line break in it would let it
	// add headers of its own.
	for _, v := range []string{from, msg.To} {
		if strings.ContainsAny(v, "\r\n") {
			return nil, fmt.Errorf("line break in mail address %q", v)
		}
	}

	var b strings.Builder
	fmt.Fprintf(&b, "From: %s\r\n", from)
	fmt.Fprintf(&b, "To: %s\r\n", msg.To)
	fmt.Fprintf(&b, "Subject: %s\r\n", mime.QEncoding.Encode("UTF-8", msg.Subject))
	fmt.Fprintf(&b, "Date: %s\r\n", now.Format(time.RFC1123Z))
	fmt.Fprintf(&b, "Message-ID: %s\r\n", messageID(from))
	b.WriteString("MIME-Version: 1.0\r\n")
	b.WriteString("Content-Type: text/plain; charset=UTF-8\r\n")
	b.WriteString("Content-Transfer-Encoding: quoted-printable\r\n")
	b.WriteString("\r\n")

	qp := quotedprintable.NewWriter(&b)
	if _, err := qp.Write([]byte(msg.Body)); err != nil {
		return nil, err
	}
	if err := qp.Close(); err != nil {
		return nil, err
	}
	return []byte(b.String()), nil
}

// messageID returns a unique Message-ID in the sender's domain.
func messageID(from string) string {
	domain := "localhost"
	if addr, err := mail.ParseAddress(from); err == nil {
		if i := strings.LastIndex(addr.Address, "@"); i >= 0 {
			domain = addr.Address[i+1:]
		}
	}
	b := make([]byte, 16)
	// Never fails since Go 1.24; a broken entropy source crashes the process.
	_, _ = rand.Read(b)
	return "<" + hex.EncodeToString(b) + "@" + domain + ">"
}
