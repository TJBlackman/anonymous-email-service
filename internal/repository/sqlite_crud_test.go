package repository

import (
	"context"
	"errors"
	"path/filepath"
	"testing"
	"time"

	"anonymous-email-service/internal/models"
)

func TestSQLiteInboxLifecycleAndExpiry(t *testing.T) {
	repo := openTestSQLite(t)
	ctx := context.Background()

	inbox := &models.Inbox{
		Address:   "Case@Test.example",
		LocalPart: "Case",
		Token:     "token-1",
		ExpiresAt: time.Now().Add(2 * time.Hour),
	}
	if err := repo.CreateInbox(ctx, inbox); err != nil {
		t.Fatalf("CreateInbox() error = %v", err)
	}

	if inbox.ID == 0 {
		t.Fatal("CreateInbox() did not assign ID")
	}
	if inbox.CreatedAt.IsZero() || inbox.LastAccessedAt.IsZero() {
		t.Fatal("CreateInbox() did not assign timestamps")
	}

	gotByToken, err := repo.GetInboxByToken(ctx, "token-1")
	if err != nil {
		t.Fatalf("GetInboxByToken() error = %v", err)
	}
	if gotByToken.Address != "Case@Test.example" {
		t.Fatalf("GetInboxByToken().Address = %q", gotByToken.Address)
	}

	gotByAddress, err := repo.GetInboxByAddress(ctx, "case@test.EXAMPLE")
	if err != nil {
		t.Fatalf("GetInboxByAddress() error = %v", err)
	}
	if gotByAddress.ID != inbox.ID {
		t.Fatalf("GetInboxByAddress().ID = %d, want %d", gotByAddress.ID, inbox.ID)
	}

	before := gotByAddress.LastAccessedAt
	time.Sleep(1 * time.Second)
	if err := repo.UpdateLastAccessed(ctx, inbox.ID); err != nil {
		t.Fatalf("UpdateLastAccessed() error = %v", err)
	}

	updated, err := repo.GetInboxByToken(ctx, inbox.Token)
	if err != nil {
		t.Fatalf("GetInboxByToken() after update error = %v", err)
	}
	if !updated.LastAccessedAt.After(before) {
		t.Fatalf("LastAccessedAt = %v, want after %v", updated.LastAccessedAt, before)
	}

	// An inbox with a past expiry is still returned by GetInboxByAddress: address
	// lookup is for login and does not filter on expiry (registered mailboxes are
	// accounts and use the never-expires sentinel). The cleanup worker is what
	// removes any inbox whose expiry has passed.
	expired := &models.Inbox{
		Address:   "expired@test.example",
		LocalPart: "expired",
		Token:     "token-expired",
		ExpiresAt: time.Now().Add(-1 * time.Hour),
	}
	if err := repo.CreateInbox(ctx, expired); err != nil {
		t.Fatalf("CreateInbox(expired) error = %v", err)
	}

	if _, err = repo.GetInboxByAddress(ctx, expired.Address); err != nil {
		t.Fatalf("GetInboxByAddress(expired) error = %v, want it to be found", err)
	}

	deleted, err := repo.DeleteExpiredInboxes(ctx)
	if err != nil {
		t.Fatalf("DeleteExpiredInboxes() error = %v", err)
	}
	if deleted != 1 {
		t.Fatalf("DeleteExpiredInboxes() = %d, want 1", deleted)
	}
}

func TestSQLiteSaveInboundMessageRoundTrip(t *testing.T) {
	repo := openTestSQLite(t)
	ctx := context.Background()

	inbox := createTestInbox(t, repo, "mailbox@test.example", "mailbox", "token-mailbox")

	email := &models.Email{
		InboxID:        inbox.ID,
		MessageID:      "msg-1@example.net",
		Sender:         "sender@example.net",
		SenderName:     "Sender Name",
		Recipient:      inbox.Address,
		Subject:        "Hello",
		BodyText:       "Plain body",
		BodyHTML:       "<b>Hello</b>",
		RawHeaders:     "Subject: Hello",
		SizeBytes:      123,
		HasAttachments: true,
		ReceivedAt:     time.Now().UTC().Truncate(time.Second),
	}
	attachments := []*models.Attachment{
		{
			Filename:    "hello.txt",
			ContentType: "text/plain",
			SizeBytes:   int64(len("hello attachment")),
			Content:     []byte("hello attachment"),
		},
	}

	if err := repo.SaveInboundMessage(ctx, email, attachments); err != nil {
		t.Fatalf("SaveInboundMessage() error = %v", err)
	}

	if email.ID == 0 {
		t.Fatal("SaveInboundMessage() did not assign email ID")
	}
	if attachments[0].ID == 0 || attachments[0].EmailID != email.ID {
		t.Fatalf("SaveInboundMessage() attachment IDs = %+v", attachments[0])
	}

	emails, err := repo.GetEmailsByInboxID(ctx, inbox.ID, 50, 0)
	if err != nil {
		t.Fatalf("GetEmailsByInboxID() error = %v", err)
	}
	if len(emails) != 1 {
		t.Fatalf("GetEmailsByInboxID() len = %d, want 1", len(emails))
	}
	if emails[0].Subject != "Hello" || !emails[0].HasAttachments {
		t.Fatalf("GetEmailsByInboxID()[0] = %+v", emails[0])
	}

	gotEmail, err := repo.GetEmailByID(ctx, inbox.ID, email.ID)
	if err != nil {
		t.Fatalf("GetEmailByID() error = %v", err)
	}
	if gotEmail.MessageID != "msg-1@example.net" {
		t.Fatalf("GetEmailByID().MessageID = %q", gotEmail.MessageID)
	}

	unread, err := repo.GetUnreadCount(ctx, inbox.ID)
	if err != nil {
		t.Fatalf("GetUnreadCount() error = %v", err)
	}
	if unread != 1 {
		t.Fatalf("GetUnreadCount() = %d, want 1", unread)
	}

	if err := repo.MarkEmailAsRead(ctx, email.ID); err != nil {
		t.Fatalf("MarkEmailAsRead() error = %v", err)
	}

	unread, err = repo.GetUnreadCount(ctx, inbox.ID)
	if err != nil {
		t.Fatalf("GetUnreadCount() after mark read error = %v", err)
	}
	if unread != 0 {
		t.Fatalf("GetUnreadCount() after mark read = %d, want 0", unread)
	}

	gotAttachments, err := repo.GetAttachmentsByEmailID(ctx, email.ID)
	if err != nil {
		t.Fatalf("GetAttachmentsByEmailID() error = %v", err)
	}
	if len(gotAttachments) != 1 {
		t.Fatalf("GetAttachmentsByEmailID() len = %d, want 1", len(gotAttachments))
	}
	if string(gotAttachments[0].Content) != "hello attachment" {
		t.Fatalf("GetAttachmentsByEmailID()[0].Content = %q", string(gotAttachments[0].Content))
	}

	gotAttachment, err := repo.GetAttachment(ctx, gotAttachments[0].ID)
	if err != nil {
		t.Fatalf("GetAttachment() error = %v", err)
	}
	if gotAttachment.EmailID != email.ID {
		t.Fatalf("GetAttachment().EmailID = %d, want %d", gotAttachment.EmailID, email.ID)
	}

	if err := repo.DeleteEmail(ctx, inbox.ID, email.ID); err != nil {
		t.Fatalf("DeleteEmail() error = %v", err)
	}

	_, err = repo.GetEmailByID(ctx, inbox.ID, email.ID)
	if !errors.Is(err, ErrNotFound) {
		t.Fatalf("GetEmailByID() after delete error = %v, want ErrNotFound", err)
	}

	_, err = repo.GetAttachment(ctx, gotAttachments[0].ID)
	if !errors.Is(err, ErrNotFound) {
		t.Fatalf("GetAttachment() after delete error = %v, want ErrNotFound", err)
	}
}

func TestSQLiteSaveInboundMessageRollsBackOnAttachmentFailure(t *testing.T) {
	repo := openTestSQLite(t)
	ctx := context.Background()

	inbox := createTestInbox(t, repo, "rollback@test.example", "rollback", "token-rollback")

	email := &models.Email{
		InboxID:    inbox.ID,
		Sender:     "sender@example.net",
		Recipient:  inbox.Address,
		Subject:    "Rollback",
		BodyText:   "body",
		SizeBytes:  10,
		ReceivedAt: time.Now(),
	}
	attachments := []*models.Attachment{
		{
			Filename:    "broken.bin",
			ContentType: "application/octet-stream",
			SizeBytes:   1,
			Content:     nil,
		},
	}

	if err := repo.SaveInboundMessage(ctx, email, attachments); err == nil {
		t.Fatal("SaveInboundMessage() error = nil, want failure")
	}

	emails, err := repo.GetEmailsByInboxID(ctx, inbox.ID, 50, 0)
	if err != nil {
		t.Fatalf("GetEmailsByInboxID() error = %v", err)
	}
	if len(emails) != 0 {
		t.Fatalf("GetEmailsByInboxID() len = %d, want 0 after rollback", len(emails))
	}
}

func TestSQLitePing(t *testing.T) {
	repo := openTestSQLite(t)

	if err := repo.Ping(context.Background()); err != nil {
		t.Fatalf("Ping() error = %v", err)
	}
}

func TestSQLiteSettingsGetSetUpsert(t *testing.T) {
	repo := openTestSQLite(t)
	ctx := context.Background()

	if _, err := repo.GetSetting(ctx, "admin_password_hash"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("GetSetting() on unset key error = %v, want ErrNotFound", err)
	}

	if err := repo.SetSetting(ctx, "admin_password_hash", "hash-1"); err != nil {
		t.Fatalf("SetSetting() error = %v", err)
	}
	got, err := repo.GetSetting(ctx, "admin_password_hash")
	if err != nil {
		t.Fatalf("GetSetting() error = %v", err)
	}
	if got != "hash-1" {
		t.Fatalf("GetSetting() = %q, want %q", got, "hash-1")
	}

	// A second set on the same key upserts rather than failing the PRIMARY KEY.
	if err := repo.SetSetting(ctx, "admin_password_hash", "hash-2"); err != nil {
		t.Fatalf("SetSetting() upsert error = %v", err)
	}
	got, err = repo.GetSetting(ctx, "admin_password_hash")
	if err != nil {
		t.Fatalf("GetSetting() after upsert error = %v", err)
	}
	if got != "hash-2" {
		t.Fatalf("GetSetting() after upsert = %q, want %q", got, "hash-2")
	}
}

func openTestSQLite(t *testing.T) Repository {
	t.Helper()

	path := filepath.Join(t.TempDir(), "mail.db")
	repo, err := NewSQLite(path, "")
	if err != nil {
		t.Fatalf("NewSQLite() error = %v", err)
	}
	t.Cleanup(func() {
		_ = repo.Close()
	})

	return repo
}

func createTestInbox(t *testing.T, repo Repository, address, localPart, token string) *models.Inbox {
	t.Helper()

	inbox := &models.Inbox{
		Address:   address,
		LocalPart: localPart,
		Token:     token,
		ExpiresAt: time.Now().Add(24 * time.Hour),
	}
	if err := repo.CreateInbox(context.Background(), inbox); err != nil {
		t.Fatalf("CreateInbox() error = %v", err)
	}

	return inbox
}
