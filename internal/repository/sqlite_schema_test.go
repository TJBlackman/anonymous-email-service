package repository

import (
	"database/sql"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"

	_ "modernc.org/sqlite"
)

func TestInitialMigrationSchemaContract(t *testing.T) {
	db := openSchemaTestDB(t)

	migration := readInitialMigration(t)
	execMigration(t, db, migration)
	execMigration(t, db, migration)

	assertTableExists(t, db, "inboxes")
	assertTableExists(t, db, "emails")
	assertTableExists(t, db, "attachments")

	assertIndexExists(t, db, "idx_inboxes_token")
	assertIndexExists(t, db, "idx_inboxes_address")
	assertIndexExists(t, db, "idx_inboxes_expires_at")
	assertIndexExists(t, db, "idx_emails_inbox_id")
	assertIndexExists(t, db, "idx_emails_received_at")
	assertIndexExists(t, db, "idx_attachments_email_id")

	assertJournalMode(t, db, "wal")
	assertInboxConstraintsAndDefaults(t, db)
	assertEmailDefaults(t, db)
	assertAttachmentBlobStorage(t, db)
	assertCascadeDeleteFromEmailToAttachments(t, db)
	assertCascadeDeleteFromInboxToEmails(t, db)
}

func openSchemaTestDB(t *testing.T) *sql.DB {
	t.Helper()

	tempDir := t.TempDir()
	dbPath := filepath.Join(tempDir, "schema.db")

	db, err := sql.Open("sqlite", dbPath)
	if err != nil {
		t.Fatalf("sql.Open() error = %v", err)
	}
	t.Cleanup(func() {
		_ = db.Close()
	})

	db.SetMaxOpenConns(1)
	db.SetMaxIdleConns(1)

	return db
}

func readInitialMigration(t *testing.T) string {
	t.Helper()

	_, file, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("runtime.Caller() failed")
	}

	path := filepath.Join(filepath.Dir(file), "..", "..", "migrations", "001_initial.sql")
	content, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("os.ReadFile(%q) error = %v", path, err)
	}

	return string(content)
}

func execMigration(t *testing.T, db *sql.DB, migration string) {
	t.Helper()

	if _, err := db.Exec(migration); err != nil {
		t.Fatalf("db.Exec(migration) error = %v", err)
	}
}

func assertTableExists(t *testing.T, db *sql.DB, name string) {
	t.Helper()

	var found string
	err := db.QueryRow(`SELECT name FROM sqlite_master WHERE type = 'table' AND name = ?`, name).Scan(&found)
	if err != nil {
		t.Fatalf("table %q lookup error = %v", name, err)
	}
	if found != name {
		t.Fatalf("table lookup returned %q, want %q", found, name)
	}
}

func assertIndexExists(t *testing.T, db *sql.DB, name string) {
	t.Helper()

	var found string
	err := db.QueryRow(`SELECT name FROM sqlite_master WHERE type = 'index' AND name = ?`, name).Scan(&found)
	if err != nil {
		t.Fatalf("index %q lookup error = %v", name, err)
	}
	if found != name {
		t.Fatalf("index lookup returned %q, want %q", found, name)
	}
}

func assertJournalMode(t *testing.T, db *sql.DB, want string) {
	t.Helper()

	var got string
	if err := db.QueryRow(`PRAGMA journal_mode;`).Scan(&got); err != nil {
		t.Fatalf("PRAGMA journal_mode error = %v", err)
	}
	if got != want {
		t.Fatalf("journal_mode = %q, want %q", got, want)
	}
}

func assertInboxConstraintsAndDefaults(t *testing.T, db *sql.DB) {
	t.Helper()

	now := time.Now().Unix()

	result, err := db.Exec(`
		INSERT INTO inboxes (address, local_part, token, expires_at)
		VALUES (?, ?, ?, ?)
	`, "User@example.test", "User", "token-1", now+3600)
	if err != nil {
		t.Fatalf("insert inbox error = %v", err)
	}

	inboxID, err := result.LastInsertId()
	if err != nil {
		t.Fatalf("LastInsertId() error = %v", err)
	}

	_, err = db.Exec(`
		INSERT INTO inboxes (address, local_part, token, expires_at)
		VALUES (?, ?, ?, ?)
	`, "user@example.test", "user", "token-2", now+3600)
	if err == nil {
		t.Fatal("expected case-insensitive unique constraint on inbox address")
	}
	if !strings.Contains(err.Error(), "UNIQUE constraint failed") {
		t.Fatalf("expected unique constraint error for inbox address, got %v", err)
	}

	_, err = db.Exec(`
		INSERT INTO inboxes (address, local_part, token, expires_at)
		VALUES (?, ?, ?, ?)
	`, "other@example.test", "other", "token-1", now+3600)
	if err == nil {
		t.Fatal("expected unique constraint on inbox token")
	}
	if !strings.Contains(err.Error(), "UNIQUE constraint failed") {
		t.Fatalf("expected unique constraint error for inbox token, got %v", err)
	}

	var createdAt int64
	var lastAccessedAt int64
	var expiresAt int64
	err = db.QueryRow(`
		SELECT created_at, last_accessed_at, expires_at
		FROM inboxes
		WHERE id = ?
	`, inboxID).Scan(&createdAt, &lastAccessedAt, &expiresAt)
	if err != nil {
		t.Fatalf("query inbox defaults error = %v", err)
	}

	if createdAt < now-5 || createdAt > now+5 {
		t.Fatalf("created_at = %d, want near %d", createdAt, now)
	}
	if lastAccessedAt < now-5 || lastAccessedAt > now+5 {
		t.Fatalf("last_accessed_at = %d, want near %d", lastAccessedAt, now)
	}
	if expiresAt != now+3600 {
		t.Fatalf("expires_at = %d, want %d", expiresAt, now+3600)
	}
}

func assertEmailDefaults(t *testing.T, db *sql.DB) {
	t.Helper()

	now := time.Now().Unix()

	result, err := db.Exec(`
		INSERT INTO inboxes (address, local_part, token, expires_at)
		VALUES (?, ?, ?, ?)
	`, "email-defaults@example.test", "email-defaults", "token-email-defaults", now+3600)
	if err != nil {
		t.Fatalf("insert inbox for email defaults error = %v", err)
	}

	inboxID, err := result.LastInsertId()
	if err != nil {
		t.Fatalf("LastInsertId() error = %v", err)
	}

	result, err = db.Exec(`
		INSERT INTO emails (inbox_id, sender, recipient)
		VALUES (?, ?, ?)
	`, inboxID, "sender@example.net", "email-defaults@example.test")
	if err != nil {
		t.Fatalf("insert email error = %v", err)
	}

	emailID, err := result.LastInsertId()
	if err != nil {
		t.Fatalf("LastInsertId() error = %v", err)
	}

	var subject string
	var sizeBytes int64
	var hasAttachments int64
	var receivedAt int64
	var isRead int64
	err = db.QueryRow(`
		SELECT subject, size_bytes, has_attachments, received_at, is_read
		FROM emails
		WHERE id = ?
	`, emailID).Scan(&subject, &sizeBytes, &hasAttachments, &receivedAt, &isRead)
	if err != nil {
		t.Fatalf("query email defaults error = %v", err)
	}

	if subject != "(no subject)" {
		t.Fatalf("subject = %q, want %q", subject, "(no subject)")
	}
	if sizeBytes != 0 {
		t.Fatalf("size_bytes = %d, want 0", sizeBytes)
	}
	if hasAttachments != 0 {
		t.Fatalf("has_attachments = %d, want 0", hasAttachments)
	}
	if receivedAt < now-5 || receivedAt > now+5 {
		t.Fatalf("received_at = %d, want near %d", receivedAt, now)
	}
	if isRead != 0 {
		t.Fatalf("is_read = %d, want 0", isRead)
	}

	_, err = db.Exec(`
		INSERT INTO emails (inbox_id, sender, recipient)
		VALUES (?, ?, ?)
	`, inboxID+9999, "sender@example.net", "missing@example.test")
	if err == nil {
		t.Fatal("expected foreign key violation for missing inbox")
	}
	if !strings.Contains(strings.ToLower(err.Error()), "foreign key") {
		t.Fatalf("expected foreign key error for missing inbox, got %v", err)
	}
}

func assertAttachmentBlobStorage(t *testing.T, db *sql.DB) {
	t.Helper()

	now := time.Now().Unix()

	result, err := db.Exec(`
		INSERT INTO inboxes (address, local_part, token, expires_at)
		VALUES (?, ?, ?, ?)
	`, "blob@example.test", "blob", "token-blob", now+3600)
	if err != nil {
		t.Fatalf("insert inbox for attachment test error = %v", err)
	}

	inboxID, err := result.LastInsertId()
	if err != nil {
		t.Fatalf("LastInsertId() error = %v", err)
	}

	result, err = db.Exec(`
		INSERT INTO emails (inbox_id, sender, recipient, subject)
		VALUES (?, ?, ?, ?)
	`, inboxID, "sender@example.net", "blob@example.test", "blob")
	if err != nil {
		t.Fatalf("insert email for attachment test error = %v", err)
	}

	emailID, err := result.LastInsertId()
	if err != nil {
		t.Fatalf("LastInsertId() error = %v", err)
	}

	payload := []byte("hello attachment")
	result, err = db.Exec(`
		INSERT INTO attachments (email_id, filename, content_type, size_bytes, content)
		VALUES (?, ?, ?, ?, ?)
	`, emailID, "hello.txt", "text/plain", len(payload), payload)
	if err != nil {
		t.Fatalf("insert attachment error = %v", err)
	}

	attachmentID, err := result.LastInsertId()
	if err != nil {
		t.Fatalf("LastInsertId() error = %v", err)
	}

	var sizeBytes int64
	var content []byte
	err = db.QueryRow(`
		SELECT size_bytes, content
		FROM attachments
		WHERE id = ?
	`, attachmentID).Scan(&sizeBytes, &content)
	if err != nil {
		t.Fatalf("query attachment error = %v", err)
	}

	if sizeBytes != int64(len(payload)) {
		t.Fatalf("size_bytes = %d, want %d", sizeBytes, len(payload))
	}
	if string(content) != string(payload) {
		t.Fatalf("content = %q, want %q", string(content), string(payload))
	}
}

func assertCascadeDeleteFromEmailToAttachments(t *testing.T, db *sql.DB) {
	t.Helper()

	now := time.Now().Unix()

	result, err := db.Exec(`
		INSERT INTO inboxes (address, local_part, token, expires_at)
		VALUES (?, ?, ?, ?)
	`, "email-cascade@example.test", "email-cascade", "token-email-cascade", now+3600)
	if err != nil {
		t.Fatalf("insert inbox for email cascade test error = %v", err)
	}

	inboxID, err := result.LastInsertId()
	if err != nil {
		t.Fatalf("LastInsertId() error = %v", err)
	}

	result, err = db.Exec(`
		INSERT INTO emails (inbox_id, sender, recipient, subject)
		VALUES (?, ?, ?, ?)
	`, inboxID, "sender@example.net", "email-cascade@example.test", "cascade")
	if err != nil {
		t.Fatalf("insert email for email cascade test error = %v", err)
	}

	emailID, err := result.LastInsertId()
	if err != nil {
		t.Fatalf("LastInsertId() error = %v", err)
	}

	_, err = db.Exec(`
		INSERT INTO attachments (email_id, filename, content_type, size_bytes, content)
		VALUES (?, ?, ?, ?, ?)
	`, emailID, "cascade.txt", "text/plain", 3, []byte("abc"))
	if err != nil {
		t.Fatalf("insert attachment for email cascade test error = %v", err)
	}

	if _, err := db.Exec(`DELETE FROM emails WHERE id = ?`, emailID); err != nil {
		t.Fatalf("delete email error = %v", err)
	}

	var count int
	if err := db.QueryRow(`SELECT COUNT(*) FROM attachments WHERE email_id = ?`, emailID).Scan(&count); err != nil {
		t.Fatalf("count attachments after email delete error = %v", err)
	}
	if count != 0 {
		t.Fatalf("attachment count after email delete = %d, want 0", count)
	}
}

func assertCascadeDeleteFromInboxToEmails(t *testing.T, db *sql.DB) {
	t.Helper()

	now := time.Now().Unix()

	result, err := db.Exec(`
		INSERT INTO inboxes (address, local_part, token, expires_at)
		VALUES (?, ?, ?, ?)
	`, "inbox-cascade@example.test", "inbox-cascade", "token-inbox-cascade", now+3600)
	if err != nil {
		t.Fatalf("insert inbox for inbox cascade test error = %v", err)
	}

	inboxID, err := result.LastInsertId()
	if err != nil {
		t.Fatalf("LastInsertId() error = %v", err)
	}

	result, err = db.Exec(`
		INSERT INTO emails (inbox_id, sender, recipient, subject)
		VALUES (?, ?, ?, ?)
	`, inboxID, "sender@example.net", "inbox-cascade@example.test", "cascade")
	if err != nil {
		t.Fatalf("insert email for inbox cascade test error = %v", err)
	}

	emailID, err := result.LastInsertId()
	if err != nil {
		t.Fatalf("LastInsertId() error = %v", err)
	}

	_, err = db.Exec(`
		INSERT INTO attachments (email_id, filename, content_type, size_bytes, content)
		VALUES (?, ?, ?, ?, ?)
	`, emailID, "nested.txt", "text/plain", 4, []byte("nest"))
	if err != nil {
		t.Fatalf("insert attachment for inbox cascade test error = %v", err)
	}

	if _, err := db.Exec(`DELETE FROM inboxes WHERE id = ?`, inboxID); err != nil {
		t.Fatalf("delete inbox error = %v", err)
	}

	var emailCount int
	if err := db.QueryRow(`SELECT COUNT(*) FROM emails WHERE id = ?`, emailID).Scan(&emailCount); err != nil {
		t.Fatalf("count emails after inbox delete error = %v", err)
	}
	if emailCount != 0 {
		t.Fatalf("email count after inbox delete = %d, want 0", emailCount)
	}

	var attachmentCount int
	if err := db.QueryRow(`SELECT COUNT(*) FROM attachments WHERE email_id = ?`, emailID).Scan(&attachmentCount); err != nil {
		t.Fatalf("count attachments after inbox delete error = %v", err)
	}
	if attachmentCount != 0 {
		t.Fatalf("attachment count after inbox delete = %d, want 0", attachmentCount)
	}
}
