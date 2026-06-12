// Package email provides a small mail-sending abstraction used for
// transactional messages such as password resets.
//
// The Mailer interface decouples handlers from the transport. In production an
// SMTPMailer delivers mail through a configured SMTP relay; in local
// development (no SMTP configured) a LogMailer writes the message to the server
// log so the flow is testable without a mail server.
package email

import (
	"context"
	"crypto/tls"
	"fmt"
	"log"
	"net"
	"net/smtp"
	"strings"
	"time"
)

// Mailer sends a plaintext email. Implementations must be safe for concurrent
// use by multiple goroutines.
type Mailer interface {
	// Send delivers a plaintext message to a single recipient.
	Send(ctx context.Context, to, subject, body string) error
}

// LogMailer is a no-op transport that logs messages instead of sending them.
// It is the default when SMTP is not configured so the password-reset flow can
// be exercised locally (the reset link appears in the server log).
type LogMailer struct{}

// Send logs the message. The body is logged in full so a developer can copy the
// reset link during local testing.
func (LogMailer) Send(_ context.Context, to, subject, body string) error {
	log.Printf("[email:log] to=%q subject=%q\n%s", to, subject, body)
	return nil
}

// SMTPConfig holds the settings for an SMTPMailer.
type SMTPConfig struct {
	// Host is the SMTP server hostname (without port).
	Host string
	// Port is the SMTP server port. 465 implies implicit TLS; other ports
	// (typically 587) use STARTTLS when offered by the server.
	Port string
	// Username / Password authenticate to the relay. When Username is empty,
	// the connection is made without authentication.
	Username string
	Password string
	// From is the envelope/header From address, e.g.
	// "Core Team Builder <noreply@example.com>".
	From string
}

// SMTPMailer delivers mail through an SMTP relay.
type SMTPMailer struct {
	cfg SMTPConfig
}

// NewSMTPMailer constructs an SMTPMailer from the given config.
func NewSMTPMailer(cfg SMTPConfig) *SMTPMailer {
	return &SMTPMailer{cfg: cfg}
}

// Send delivers a plaintext message. It uses implicit TLS on port 465 and
// STARTTLS otherwise (when the server advertises it).
func (m *SMTPMailer) Send(ctx context.Context, to, subject, body string) error {
	addr := net.JoinHostPort(m.cfg.Host, m.cfg.Port)
	msg := buildMessage(m.cfg.From, to, subject, body)

	var auth smtp.Auth
	if m.cfg.Username != "" {
		auth = smtp.PlainAuth("", m.cfg.Username, m.cfg.Password, m.cfg.Host)
	}

	if m.cfg.Port == "465" {
		return m.sendImplicitTLS(ctx, addr, auth, to, msg)
	}
	// STARTTLS / plain path. smtp.SendMail upgrades to TLS when the server
	// advertises STARTTLS.
	if err := smtp.SendMail(addr, auth, senderAddress(m.cfg.From), []string{to}, msg); err != nil {
		return fmt.Errorf("smtp send: %w", err)
	}
	return nil
}

// sendImplicitTLS dials a TLS connection up front (port 465) and speaks SMTP
// over it.
func (m *SMTPMailer) sendImplicitTLS(ctx context.Context, addr string, auth smtp.Auth, to string, msg []byte) error {
	dialer := &net.Dialer{Timeout: 15 * time.Second}
	conn, err := tls.DialWithDialer(dialer, "tcp", addr, &tls.Config{ServerName: m.cfg.Host})
	if err != nil {
		return fmt.Errorf("smtp tls dial: %w", err)
	}
	defer conn.Close()

	client, err := smtp.NewClient(conn, m.cfg.Host)
	if err != nil {
		return fmt.Errorf("smtp client: %w", err)
	}
	defer client.Close()

	if auth != nil {
		if err := client.Auth(auth); err != nil {
			return fmt.Errorf("smtp auth: %w", err)
		}
	}
	if err := client.Mail(senderAddress(m.cfg.From)); err != nil {
		return fmt.Errorf("smtp mail from: %w", err)
	}
	if err := client.Rcpt(to); err != nil {
		return fmt.Errorf("smtp rcpt to: %w", err)
	}
	wc, err := client.Data()
	if err != nil {
		return fmt.Errorf("smtp data: %w", err)
	}
	if _, err := wc.Write(msg); err != nil {
		return fmt.Errorf("smtp write: %w", err)
	}
	if err := wc.Close(); err != nil {
		return fmt.Errorf("smtp close data: %w", err)
	}
	return client.Quit()
}

// buildMessage assembles RFC 5322 headers plus the plaintext body.
func buildMessage(from, to, subject, body string) []byte {
	var b strings.Builder
	fmt.Fprintf(&b, "From: %s\r\n", from)
	fmt.Fprintf(&b, "To: %s\r\n", to)
	fmt.Fprintf(&b, "Subject: %s\r\n", subject)
	b.WriteString("MIME-Version: 1.0\r\n")
	b.WriteString("Content-Type: text/plain; charset=\"utf-8\"\r\n")
	b.WriteString("\r\n")
	b.WriteString(body)
	return []byte(b.String())
}

// senderAddress extracts the bare address from a possibly display-name-wrapped
// From value ("Name <addr@host>" → "addr@host"), as required by the SMTP
// MAIL FROM command.
func senderAddress(from string) string {
	if i := strings.LastIndex(from, "<"); i >= 0 {
		if j := strings.LastIndex(from, ">"); j > i {
			return from[i+1 : j]
		}
	}
	return strings.TrimSpace(from)
}
