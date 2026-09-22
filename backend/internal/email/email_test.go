package email

import (
	"bufio"
	"context"
	"fmt"
	"log"
	"net"
	"os"
	"strings"
	"sync"
	"testing"
)

func TestBuildMessage(t *testing.T) {
	got := string(buildMessage("Core Team Builder <noreply@example.com>", "user@example.com", "Reset your password", "Click here.\nThanks."))

	// Headers must be CRLF-terminated and separated from the body by a blank
	// line, or relays reject or mangle the message.
	for _, want := range []string{
		"From: Core Team Builder <noreply@example.com>\r\n",
		"To: user@example.com\r\n",
		"Subject: Reset your password\r\n",
		"MIME-Version: 1.0\r\n",
		"Content-Type: text/plain; charset=\"utf-8\"\r\n",
	} {
		if !strings.Contains(got, want) {
			t.Errorf("missing header %q in:\n%s", want, got)
		}
	}

	head, body, found := strings.Cut(got, "\r\n\r\n")
	if !found {
		t.Fatalf("no blank line between headers and body:\n%s", got)
	}
	if body != "Click here.\nThanks." {
		t.Errorf("body = %q", body)
	}
	if strings.Contains(head, "Click here") {
		t.Error("body leaked into the headers")
	}
}

func TestSenderAddress(t *testing.T) {
	cases := []struct {
		name string
		in   string
		want string
	}{
		{"display name wrapped", "Core Team Builder <noreply@example.com>", "noreply@example.com"},
		{"bare address", "noreply@example.com", "noreply@example.com"},
		{"surrounding space trimmed", "  noreply@example.com  ", "noreply@example.com"},
		{"empty", "", ""},
		// A stray '<' with no closing '>' is not a valid wrapper, so the value
		// is used as-is rather than silently truncated.
		{"unterminated bracket", "noreply<example.com", "noreply<example.com"},
		{"angle brackets only", "<a@b.c>", "a@b.c"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := senderAddress(tc.in); got != tc.want {
				t.Errorf("got %q, want %q", got, tc.want)
			}
		})
	}
}

// TestLogMailerLogsTheMessage pins the local-development behaviour the reset
// flow depends on: the whole body (including the reset link) reaches the log so
// a developer can follow the link without a mail server.
func TestLogMailerLogsTheMessage(t *testing.T) {
	var buf strings.Builder
	log.SetOutput(&buf)
	log.SetFlags(0)
	t.Cleanup(func() {
		log.SetOutput(os.Stderr)
		log.SetFlags(log.LstdFlags)
	})

	if err := (LogMailer{}).Send(context.Background(), "user@example.com", "Reset", "link: https://example.com/reset?token=abc"); err != nil {
		t.Fatalf("Send: %v", err)
	}

	got := buf.String()
	for _, want := range []string{"user@example.com", "Reset", "token=abc"} {
		if !strings.Contains(got, want) {
			t.Errorf("missing %q in log output %q", want, got)
		}
	}
}

// TestLogMailerSatisfiesMailer is a compile-time-ish guard that the local
// transport is still substitutable for the SMTP one.
func TestLogMailerSatisfiesMailer(t *testing.T) {
	var m Mailer = LogMailer{}
	if err := m.Send(context.Background(), "a@b.c", "s", "b"); err != nil {
		t.Errorf("Send: %v", err)
	}
	var _ Mailer = NewSMTPMailer(SMTPConfig{})
}

// TestSMTPMailerSendsOverTheWire runs the real SMTP client against a minimal
// in-process server, so the happy path (envelope, recipient, DATA body) is
// exercised without reaching the network.
func TestSMTPMailerSendsOverTheWire(t *testing.T) {
	srv := newFakeSMTP(t)

	host, port, err := net.SplitHostPort(srv.addr)
	if err != nil {
		t.Fatalf("split host port: %v", err)
	}

	m := NewSMTPMailer(SMTPConfig{
		Host: host,
		Port: port,
		From: "Core Team Builder <noreply@example.com>",
	})
	if err := m.Send(context.Background(), "user@example.com", "Reset", "body text"); err != nil {
		t.Fatalf("Send: %v", err)
	}

	got := srv.transcript()
	for _, want := range []string{
		"MAIL FROM:<noreply@example.com>", // display name stripped for the envelope
		"RCPT TO:<user@example.com>",
		"Subject: Reset",
		"body text",
	} {
		if !strings.Contains(got, want) {
			t.Errorf("missing %q in SMTP transcript:\n%s", want, got)
		}
	}
}

// TestSMTPMailerReportsDialFailures checks a dead relay surfaces as an error
// rather than a silent success — a swallowed error here means a user never
// receives a reset link and nothing says so.
func TestSMTPMailerReportsDialFailures(t *testing.T) {
	// Port 0 is never listening.
	m := NewSMTPMailer(SMTPConfig{Host: "127.0.0.1", Port: "1"})

	err := m.Send(context.Background(), "user@example.com", "Reset", "body")
	if err == nil {
		t.Fatal("got nil error, want a dial failure")
	}
	if !strings.Contains(err.Error(), "smtp send") {
		t.Errorf("error = %q, want it wrapped with context", err)
	}
}

func TestSMTPMailerImplicitTLSDialFailure(t *testing.T) {
	// Port 465 selects the implicit-TLS path; nothing is listening, so the TLS
	// dial fails and must be reported with its own context.
	m := NewSMTPMailer(SMTPConfig{Host: "127.0.0.1", Port: "465"})

	err := m.Send(context.Background(), "user@example.com", "Reset", "body")
	if err == nil {
		t.Fatal("got nil error, want a TLS dial failure")
	}
	if !strings.Contains(err.Error(), "smtp tls dial") {
		t.Errorf("error = %q, want the implicit-TLS path to be taken", err)
	}
}

// fakeSMTP is a single-connection SMTP server that speaks just enough of the
// protocol for net/smtp's plain (no STARTTLS) path and records everything the
// client sent.
type fakeSMTP struct {
	addr string

	mu    sync.Mutex
	lines []string
}

func newFakeSMTP(t *testing.T) *fakeSMTP {
	t.Helper()

	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	t.Cleanup(func() { ln.Close() })

	s := &fakeSMTP{addr: ln.Addr().String()}
	done := make(chan struct{})
	t.Cleanup(func() { <-done })

	go func() {
		defer close(done)
		conn, err := ln.Accept()
		if err != nil {
			return
		}
		defer conn.Close()
		s.serve(conn)
	}()
	return s
}

func (s *fakeSMTP) serve(conn net.Conn) {
	r := bufio.NewReader(conn)
	fmt.Fprint(conn, "220 fake ESMTP\r\n")

	inData := false
	for {
		line, err := r.ReadString('\n')
		if err != nil {
			return
		}
		line = strings.TrimRight(line, "\r\n")
		s.record(line)

		if inData {
			if line == "." {
				inData = false
				fmt.Fprint(conn, "250 2.0.0 Ok\r\n")
			}
			continue
		}

		switch {
		case strings.HasPrefix(line, "EHLO"):
			// No STARTTLS advertised, so net/smtp stays in plaintext.
			fmt.Fprint(conn, "250-fake\r\n250 SIZE 35882577\r\n")
		case strings.HasPrefix(line, "HELO"):
			fmt.Fprint(conn, "250 fake\r\n")
		case strings.HasPrefix(line, "MAIL FROM"), strings.HasPrefix(line, "RCPT TO"):
			fmt.Fprint(conn, "250 2.0.0 Ok\r\n")
		case line == "DATA":
			inData = true
			fmt.Fprint(conn, "354 End data with <CR><LF>.<CR><LF>\r\n")
		case line == "QUIT":
			fmt.Fprint(conn, "221 2.0.0 Bye\r\n")
			return
		default:
			fmt.Fprint(conn, "250 2.0.0 Ok\r\n")
		}
	}
}

func (s *fakeSMTP) record(line string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.lines = append(s.lines, line)
}

func (s *fakeSMTP) transcript() string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return strings.Join(s.lines, "\n")
}
