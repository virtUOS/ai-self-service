package notify

import (
	"bufio"
	"bytes"
	"context"
	"io"
	"mime"
	"mime/quotedprintable"
	"net"
	"net/mail"
	"regexp"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/virtuos/keyosk/internal/database"
)

// expiryNotice is the mail the reminder sends, with the umlauts and the
// ellipsis that made the headers break.
func expiryNotice() Message {
	r := &Reminder{frontend: "https://ai.uni-osnabrueck.de"}
	return r.message(database.ExpiringKey{
		APIKey: database.APIKey{KeyPrefix: "sk-abcdef", ExpiresAt: time.Date(2026, 10, 19, 0, 0, 0, 0, time.UTC)},
		Email:  "juergen@uni-osnabrueck.de",
		Name:   "Jürgen Müller",
	}, 7)
}

// readBack parses a message the way a mail client does and returns its
// headers and decoded body.
func readBack(t *testing.T, raw []byte) (mail.Header, string) {
	t.Helper()
	m, err := mail.ReadMessage(bytes.NewReader(raw))
	if err != nil {
		t.Fatalf("not a valid message: %v", err)
	}
	if enc := m.Header.Get("Content-Transfer-Encoding"); enc != "quoted-printable" {
		t.Fatalf("Content-Transfer-Encoding = %q, want quoted-printable", enc)
	}
	body, err := io.ReadAll(quotedprintable.NewReader(m.Body))
	if err != nil {
		t.Fatalf("body is not quoted-printable: %v", err)
	}
	return m.Header, strings.ReplaceAll(string(body), "\r\n", "\n")
}

// What a mail client shows is what the reminder wrote, and nothing on the wire
// relies on the relay passing 8-bit text through.
func TestBuildMessageReadsBackAsWritten(t *testing.T) {
	msg := expiryNotice()
	now := time.Date(2026, 10, 12, 9, 30, 0, 0, time.UTC)
	raw, err := buildMessage("AI Portal <noreply@uni-osnabrueck.de>", msg, now)
	if err != nil {
		t.Fatal(err)
	}

	for i, line := range strings.Split(string(raw), "\r\n") {
		for _, c := range []byte(line) {
			if c >= 0x80 {
				t.Fatalf("line %d is not ASCII: %q", i+1, line)
			}
		}
		// RFC 5322 caps a line at 998 characters. The subject goes over the
		// recommended 78, which every relay accepts.
		if len(line) > 998 {
			t.Errorf("line %d is %d characters long, over the limit of 998", i+1, len(line))
		}
	}

	header, body := readBack(t, raw)
	subject, err := new(mime.WordDecoder).DecodeHeader(header.Get("Subject"))
	if err != nil {
		t.Fatalf("subject does not decode: %v", err)
	}
	if subject != msg.Subject {
		t.Errorf("subject reads %q, want %q", subject, msg.Subject)
	}
	if body != msg.Body {
		t.Errorf("body reads\n%s\nwant\n%s", body, msg.Body)
	}
	if date, err := header.Date(); err != nil || !date.Equal(now) {
		t.Errorf("Date = %v (%v), want %v", date, err, now)
	}
	if id := header.Get("Message-ID"); !regexp.MustCompile(`^<[0-9a-f]{32}@uni-osnabrueck\.de>$`).MatchString(id) {
		t.Errorf("Message-ID = %q, want a unique ID in the sender's domain", id)
	}
	if ct := header.Get("Content-Type"); ct != "text/plain; charset=UTF-8" {
		t.Errorf("Content-Type = %q", ct)
	}
}

// Each message gets its own Message-ID; a repeated one can make a client
// drop the later mail as a duplicate.
func TestBuildMessageIDsAreUnique(t *testing.T) {
	ids := map[string]bool{}
	for range 3 {
		raw, err := buildMessage("noreply@uni-osnabrueck.de", expiryNotice(), time.Now())
		if err != nil {
			t.Fatal(err)
		}
		header, _ := readBack(t, raw)
		ids[header.Get("Message-ID")] = true
	}
	if len(ids) != 3 {
		t.Errorf("3 messages got %d distinct Message-IDs", len(ids))
	}
}

// The recipient comes from the identity provider. A line break in it would
// let it add headers, such as a Bcc to someone else.
func TestBuildMessageRefusesALineBreakInAnAddress(t *testing.T) {
	msg := expiryNotice()
	msg.To = "a@uni-osnabrueck.de\r\nBcc: someone@example.com"
	if _, err := buildMessage("noreply@uni-osnabrueck.de", msg, time.Now()); err == nil {
		t.Error("message built with a line break in the recipient")
	}
}

// fakeRelay is an SMTP server that accepts one message without TLS or AUTH,
// as a relay inside the university network does.
type fakeRelay struct {
	addr string

	mu   sync.Mutex
	from string
	to   []string
	data []byte
}

func newFakeRelay(t *testing.T) *fakeRelay {
	t.Helper()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { ln.Close() })
	r := &fakeRelay{addr: ln.Addr().String()}
	go func() {
		conn, err := ln.Accept()
		if err != nil {
			return
		}
		defer conn.Close()
		r.serve(conn)
	}()
	return r
}

func (r *fakeRelay) serve(conn net.Conn) {
	in := bufio.NewReader(conn)
	reply := func(s string) { _, _ = io.WriteString(conn, s+"\r\n") }
	reply("220 relay ready")
	for {
		line, err := in.ReadString('\n')
		if err != nil {
			return
		}
		cmd := strings.TrimRight(line, "\r\n")
		switch verb := strings.ToUpper(strings.SplitN(cmd, " ", 2)[0]); verb {
		case "EHLO", "HELO":
			reply("250 relay")
		case "MAIL":
			r.mu.Lock()
			r.from = cmd
			r.mu.Unlock()
			reply("250 OK")
		case "RCPT":
			r.mu.Lock()
			r.to = append(r.to, cmd)
			r.mu.Unlock()
			reply("250 OK")
		case "DATA":
			reply("354 go ahead")
			var data bytes.Buffer
			for {
				l, err := in.ReadString('\n')
				if err != nil {
					return
				}
				if l == ".\r\n" {
					break
				}
				data.WriteString(strings.TrimPrefix(l, "."))
			}
			r.mu.Lock()
			r.data = data.Bytes()
			r.mu.Unlock()
			reply("250 queued")
		case "QUIT":
			reply("221 bye")
			return
		default:
			reply("502 not implemented")
		}
	}
}

// Notify hands the message to the relay with the right envelope, and it
// arrives intact.
func TestNotifyDeliversThroughTheRelay(t *testing.T) {
	relay := newFakeRelay(t)
	msg := expiryNotice()

	s := &SMTP{Host: relay.addr, From: "noreply@uni-osnabrueck.de"}
	if err := s.Notify(context.Background(), msg); err != nil {
		t.Fatalf("Notify: %v", err)
	}

	relay.mu.Lock()
	defer relay.mu.Unlock()
	if relay.from != "MAIL FROM:<noreply@uni-osnabrueck.de>" && !strings.HasPrefix(relay.from, "MAIL FROM:<noreply@uni-osnabrueck.de> ") {
		t.Errorf("envelope sender = %q", relay.from)
	}
	if len(relay.to) != 1 || relay.to[0] != "RCPT TO:<juergen@uni-osnabrueck.de>" {
		t.Errorf("envelope recipients = %q, want only juergen@uni-osnabrueck.de", relay.to)
	}
	header, body := readBack(t, relay.data)
	if header.Get("To") != msg.To {
		t.Errorf("To = %q, want %q", header.Get("To"), msg.To)
	}
	if body != msg.Body {
		t.Error("body arrived changed")
	}
}

// A relay that is not there fails the send, so the reminder retries it next
// run rather than recording it as sent.
func TestNotifyReportsAnUnreachableRelay(t *testing.T) {
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	addr := ln.Addr().String()
	ln.Close()

	s := &SMTP{Host: addr, From: "noreply@uni-osnabrueck.de"}
	if err := s.Notify(context.Background(), expiryNotice()); err == nil {
		t.Error("Notify reported success with no relay listening")
	}
}
