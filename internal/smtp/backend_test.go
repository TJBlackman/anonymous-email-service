package smtp

import (
	"bytes"
	"errors"
	"io"
	"testing"

	"anonymous-email-service/internal/config"
	gosmtp "github.com/emersion/go-smtp"
)

func TestNewServerAppliesConfig(t *testing.T) {
	cfg := &config.Config{
		Domain:         "example.test",
		SMTPListenAddr: ":2525",
		MaxEmailSize:   4 * 1024,
	}

	server := NewServer(nil, cfg, nil)

	if server.Addr != ":2525" {
		t.Fatalf("Addr = %q, want %q", server.Addr, ":2525")
	}
	if server.Domain != "example.test" {
		t.Fatalf("Domain = %q, want %q", server.Domain, "example.test")
	}
	if server.MaxMessageBytes != 4*1024 {
		t.Fatalf("MaxMessageBytes = %d, want %d", server.MaxMessageBytes, 4*1024)
	}
	if server.MaxRecipients != 1 {
		t.Fatalf("MaxRecipients = %d, want %d", server.MaxRecipients, 1)
	}
}

func TestSessionRcptRejectsInvalidAddress(t *testing.T) {
	session := &Session{
		backend: &Backend{domain: "example.test"},
	}

	err := session.Rcpt("not-an-address", nil)
	if err == nil {
		t.Fatal("expected error for invalid address")
	}

	var smtpErr *gosmtp.SMTPError
	if !errors.As(err, &smtpErr) {
		t.Fatalf("expected SMTPError, got %T", err)
	}
	if smtpErr.Code != 550 {
		t.Fatalf("Code = %d, want %d", smtpErr.Code, 550)
	}
}

func TestSessionRcptRejectsRelay(t *testing.T) {
	session := &Session{
		backend: &Backend{domain: "example.test"},
	}

	err := session.Rcpt("user@other.test", nil)
	if err == nil {
		t.Fatal("expected relay rejection")
	}

	var smtpErr *gosmtp.SMTPError
	if !errors.As(err, &smtpErr) {
		t.Fatalf("expected SMTPError, got %T", err)
	}
	if smtpErr.Code != 550 {
		t.Fatalf("Code = %d, want %d", smtpErr.Code, 550)
	}
}

func TestSessionRcptAcceptsConfiguredDomain(t *testing.T) {
	session := &Session{
		backend: &Backend{domain: "example.test"},
	}

	if err := session.Rcpt("user@example.test", nil); err != nil {
		t.Fatalf("Rcpt() returned error: %v", err)
	}

	if len(session.rcpts) != 1 || session.rcpts[0] != "user@example.test" {
		t.Fatalf("rcpts = %#v, want configured domain recipient", session.rcpts)
	}
}

func TestSessionDataConsumesMessageAndReturnsTemporaryFailure(t *testing.T) {
	session := &Session{
		backend: &Backend{domain: "example.test"},
	}

	body := bytes.NewBufferString("Subject: test\r\n\r\nhello")
	err := session.Data(body)
	if err == nil {
		t.Fatal("expected temporary failure")
	}

	var smtpErr *gosmtp.SMTPError
	if !errors.As(err, &smtpErr) {
		t.Fatalf("expected SMTPError, got %T", err)
	}
	if smtpErr.Code != 451 {
		t.Fatalf("Code = %d, want %d", smtpErr.Code, 451)
	}
	if remaining, readErr := io.ReadAll(body); readErr != nil || len(remaining) != 0 {
		t.Fatalf("expected message reader to be consumed, remaining=%q, err=%v", string(remaining), readErr)
	}
}
