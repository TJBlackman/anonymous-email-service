package smtp

import (
	"bytes"
	"context"
	"errors"
	"strings"
	"testing"

	"anonymous-email-service/internal/config"
	"anonymous-email-service/internal/models"
	"anonymous-email-service/internal/repository"
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
		backend: &Backend{
			domain: "example.test",
			repo:   &stubRepository{},
		},
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
		backend: &Backend{
			domain: "example.test",
			repo:   &stubRepository{},
		},
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
	repo := &stubRepository{
		inboxByAddress: map[string]*models.Inbox{
			"user@example.test": {
				ID:      1,
				Address: "user@example.test",
			},
		},
	}
	session := &Session{
		backend: &Backend{
			domain: "example.test",
			repo:   repo,
		},
	}

	if err := session.Rcpt("user@example.test", nil); err != nil {
		t.Fatalf("Rcpt() returned error: %v", err)
	}

	if len(session.rcpts) != 1 || session.rcpts[0] != "user@example.test" {
		t.Fatalf("rcpts = %#v, want configured domain recipient", session.rcpts)
	}
	if session.inbox == nil || session.inbox.ID != 1 {
		t.Fatalf("inbox = %+v, want loaded inbox", session.inbox)
	}
}

func TestSessionRcptRejectsUnknownInbox(t *testing.T) {
	session := &Session{
		backend: &Backend{
			domain: "example.test",
			repo:   &stubRepository{},
		},
	}

	err := session.Rcpt("user@example.test", nil)
	if err == nil {
		t.Fatal("expected unknown inbox rejection")
	}

	var smtpErr *gosmtp.SMTPError
	if !errors.As(err, &smtpErr) {
		t.Fatalf("expected SMTPError, got %T", err)
	}
	if smtpErr.Code != 550 {
		t.Fatalf("Code = %d, want %d", smtpErr.Code, 550)
	}
	if smtpErr.EnhancedCode != (gosmtp.EnhancedCode{5, 1, 1}) {
		t.Fatalf("EnhancedCode = %v, want %v", smtpErr.EnhancedCode, gosmtp.EnhancedCode{5, 1, 1})
	}
}

func TestSessionDataStoresMultipartMessage(t *testing.T) {
	repo := &stubRepository{
		inboxByAddress: map[string]*models.Inbox{
			"user@example.test": {
				ID:      42,
				Address: "user@example.test",
			},
		},
	}
	session := &Session{
		backend: &Backend{
			domain: "example.test",
			config: &config.Config{
				Domain:            "example.test",
				MaxEmailSize:      1024 * 1024,
				MaxAttachmentSize: 1024,
			},
			repo: repo,
		},
	}

	if err := session.Rcpt("user@example.test", nil); err != nil {
		t.Fatalf("Rcpt() error = %v", err)
	}

	body := bytes.NewBufferString(strings.Join([]string{
		"From: Sender Name <sender@example.net>",
		"Subject: test message",
		"Message-Id: <msg-1@example.net>",
		"MIME-Version: 1.0",
		"Content-Type: multipart/mixed; boundary=abc123",
		"",
		"--abc123",
		"Content-Type: text/plain; charset=utf-8",
		"",
		"hello text",
		"--abc123",
		"Content-Type: text/html; charset=utf-8",
		"",
		"<p>hello html</p>",
		"--abc123",
		"Content-Type: text/plain; name=\"hello.txt\"",
		"Content-Disposition: attachment; filename=\"hello.txt\"",
		"",
		"attachment body",
		"--abc123--",
		"",
	}, "\r\n"))

	if err := session.Data(body); err != nil {
		t.Fatalf("Data() error = %v", err)
	}

	if repo.savedEmail == nil {
		t.Fatal("SaveInboundMessage() was not called")
	}
	if repo.savedEmail.Subject != "test message" {
		t.Fatalf("Subject = %q, want %q", repo.savedEmail.Subject, "test message")
	}
	if repo.savedEmail.Sender != "sender@example.net" {
		t.Fatalf("Sender = %q, want sender@example.net", repo.savedEmail.Sender)
	}
	if repo.savedEmail.SenderName != "Sender Name" {
		t.Fatalf("SenderName = %q, want Sender Name", repo.savedEmail.SenderName)
	}
	if repo.savedEmail.BodyText != "hello text" {
		t.Fatalf("BodyText = %q, want hello text", repo.savedEmail.BodyText)
	}
	if repo.savedEmail.BodyHTML != "<p>hello html</p>" {
		t.Fatalf("BodyHTML = %q, want HTML part", repo.savedEmail.BodyHTML)
	}
	if !repo.savedEmail.HasAttachments {
		t.Fatal("HasAttachments = false, want true")
	}
	if len(repo.savedAttachments) != 1 {
		t.Fatalf("attachments = %d, want 1", len(repo.savedAttachments))
	}
	if string(repo.savedAttachments[0].Content) != "attachment body" {
		t.Fatalf("attachment content = %q", string(repo.savedAttachments[0].Content))
	}
}

func TestSessionDataRejectsOversizedAttachment(t *testing.T) {
	repo := &stubRepository{
		inboxByAddress: map[string]*models.Inbox{
			"user@example.test": {
				ID:      1,
				Address: "user@example.test",
			},
		},
	}
	session := &Session{
		backend: &Backend{
			domain: "example.test",
			config: &config.Config{
				Domain:            "example.test",
				MaxEmailSize:      1024 * 1024,
				MaxAttachmentSize: 4,
			},
			repo: repo,
		},
	}

	if err := session.Rcpt("user@example.test", nil); err != nil {
		t.Fatalf("Rcpt() error = %v", err)
	}

	body := bytes.NewBufferString(strings.Join([]string{
		"From: sender@example.net",
		"Subject: oversized",
		"MIME-Version: 1.0",
		"Content-Type: multipart/mixed; boundary=abc123",
		"",
		"--abc123",
		"Content-Type: text/plain; name=\"big.txt\"",
		"Content-Disposition: attachment; filename=\"big.txt\"",
		"",
		"toolarge",
		"--abc123--",
		"",
	}, "\r\n"))

	err := session.Data(body)
	if err == nil {
		t.Fatal("expected oversized attachment rejection")
	}

	var smtpErr *gosmtp.SMTPError
	if !errors.As(err, &smtpErr) {
		t.Fatalf("expected SMTPError, got %T", err)
	}
	if smtpErr.Code != 552 {
		t.Fatalf("Code = %d, want %d", smtpErr.Code, 552)
	}
}

func TestSessionDataReturnsTemporaryFailureOnRepositoryError(t *testing.T) {
	repo := &stubRepository{
		inboxByAddress: map[string]*models.Inbox{
			"user@example.test": {
				ID:      1,
				Address: "user@example.test",
			},
		},
		saveErr: errors.New("boom"),
	}
	session := &Session{
		backend: &Backend{
			domain: "example.test",
			config: &config.Config{
				Domain:            "example.test",
				MaxEmailSize:      1024 * 1024,
				MaxAttachmentSize: 1024,
			},
			repo: repo,
		},
	}

	if err := session.Rcpt("user@example.test", nil); err != nil {
		t.Fatalf("Rcpt() error = %v", err)
	}

	err := session.Data(bytes.NewBufferString("From: sender@example.net\r\nSubject: test\r\n\r\nhello"))
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
}

type stubRepository struct {
	inboxByAddress   map[string]*models.Inbox
	savedEmail       *models.Email
	savedAttachments []*models.Attachment
	saveErr          error
}

func (s *stubRepository) CreateInbox(context.Context, *models.Inbox) error { return nil }
func (s *stubRepository) GetInboxByToken(context.Context, string) (*models.Inbox, error) {
	return nil, repository.ErrNotFound
}
func (s *stubRepository) GetInboxByAddress(_ context.Context, address string) (*models.Inbox, error) {
	if inbox, ok := s.inboxByAddress[strings.ToLower(address)]; ok {
		return inbox, nil
	}
	return nil, repository.ErrNotFound
}
func (s *stubRepository) UpdateLastAccessed(context.Context, int64) error     { return nil }
func (s *stubRepository) DeleteExpiredInboxes(context.Context) (int64, error) { return 0, nil }
func (s *stubRepository) SaveEmail(context.Context, *models.Email) error      { return nil }
func (s *stubRepository) SaveInboundMessage(_ context.Context, email *models.Email, attachments []*models.Attachment) error {
	if s.saveErr != nil {
		return s.saveErr
	}
	s.savedEmail = email
	s.savedAttachments = attachments
	return nil
}
func (s *stubRepository) GetEmailsByInboxID(context.Context, int64, int, int) ([]*models.Email, error) {
	return nil, nil
}
func (s *stubRepository) GetEmailByID(context.Context, int64, int64) (*models.Email, error) {
	return nil, repository.ErrNotFound
}
func (s *stubRepository) MarkEmailAsRead(context.Context, int64) error             { return nil }
func (s *stubRepository) GetUnreadCount(context.Context, int64) (int, error)       { return 0, nil }
func (s *stubRepository) DeleteEmail(context.Context, int64, int64) error          { return nil }
func (s *stubRepository) SaveAttachment(context.Context, *models.Attachment) error { return nil }
func (s *stubRepository) GetAttachmentsByEmailID(context.Context, int64) ([]*models.Attachment, error) {
	return nil, nil
}
func (s *stubRepository) GetAttachment(context.Context, int64) (*models.Attachment, error) {
	return nil, repository.ErrNotFound
}
func (s *stubRepository) Close() error { return nil }
