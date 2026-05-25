package repository

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"time"

	"anonymous-email-service/internal/models"
	_ "modernc.org/sqlite"
)

type SQLite struct {
	path string
	db   *sql.DB
}

func NewSQLite(path string) (Repository, error) {
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return nil, fmt.Errorf("create database directory: %w", err)
	}

	db, err := sql.Open("sqlite", path)
	if err != nil {
		return nil, fmt.Errorf("open sqlite database: %w", err)
	}

	db.SetMaxOpenConns(1)
	db.SetMaxIdleConns(1)

	store := &SQLite{
		path: path,
		db:   db,
	}

	if err := store.init(context.Background()); err != nil {
		_ = db.Close()
		return nil, err
	}

	return store, nil
}

func (s *SQLite) init(ctx context.Context) error {
	if err := s.execScript(ctx, `PRAGMA foreign_keys = ON; PRAGMA journal_mode = WAL;`); err != nil {
		return fmt.Errorf("configure sqlite pragmas: %w", err)
	}

	for _, name := range []string{"001_initial.sql", "002_domains.sql"} {
		migration, err := loadMigration(name)
		if err != nil {
			return err
		}
		if err := s.execScript(ctx, migration); err != nil {
			return fmt.Errorf("apply migration %q: %w", name, err)
		}
	}

	return nil
}

func (s *SQLite) execScript(ctx context.Context, script string) error {
	_, err := s.db.ExecContext(ctx, script)
	return err
}

func loadMigration(name string) (string, error) {
	_, file, _, ok := runtime.Caller(0)
	if !ok {
		return "", fmt.Errorf("resolve migration path: runtime.Caller failed")
	}

	path := filepath.Join(filepath.Dir(file), "..", "..", "migrations", name)
	content, err := os.ReadFile(path)
	if err != nil {
		return "", fmt.Errorf("read migration %q: %w", path, err)
	}

	return string(content), nil
}

func (s *SQLite) CreateInbox(ctx context.Context, inbox *models.Inbox) error {
	result, err := s.db.ExecContext(ctx, `
		INSERT INTO inboxes (address, local_part, token, expires_at)
		VALUES (?, ?, ?, ?)
	`, inbox.Address, inbox.LocalPart, inbox.Token, toUnix(inbox.ExpiresAt))
	if err != nil {
		return mapSQLError("create inbox", err)
	}

	id, err := result.LastInsertId()
	if err != nil {
		return fmt.Errorf("create inbox: read inserted id: %w", err)
	}

	row := s.db.QueryRowContext(ctx, `
		SELECT id, created_at, last_accessed_at
		FROM inboxes
		WHERE id = ?
	`, id)
	var createdAt int64
	var lastAccessedAt int64
	if err := row.Scan(&inbox.ID, &createdAt, &lastAccessedAt); err != nil {
		return mapScanError("create inbox", err)
	}

	inbox.CreatedAt = fromUnix(createdAt)
	inbox.LastAccessedAt = fromUnix(lastAccessedAt)
	inbox.ExpiresAt = inbox.ExpiresAt.UTC()
	return nil
}

func (s *SQLite) GetInboxByToken(ctx context.Context, token string) (*models.Inbox, error) {
	row := s.db.QueryRowContext(ctx, `
		SELECT id, address, local_part, token, created_at, last_accessed_at, expires_at
		FROM inboxes
		WHERE token = ? AND expires_at > ?
	`, token, nowUnix())
	return scanInbox(row, "get inbox by token")
}

func (s *SQLite) GetInboxByAddress(ctx context.Context, address string) (*models.Inbox, error) {
	row := s.db.QueryRowContext(ctx, `
		SELECT id, address, local_part, token, created_at, last_accessed_at, expires_at
		FROM inboxes
		WHERE address = ? COLLATE NOCASE AND expires_at > ?
	`, address, nowUnix())
	return scanInbox(row, "get inbox by address")
}

func (s *SQLite) UpdateLastAccessed(ctx context.Context, inboxID int64) error {
	result, err := s.db.ExecContext(ctx, `
		UPDATE inboxes
		SET last_accessed_at = ?
		WHERE id = ?
	`, nowUnix(), inboxID)
	if err != nil {
		return fmt.Errorf("update last accessed: %w", err)
	}

	return requireRowsAffected("update last accessed", result)
}

func (s *SQLite) DeleteExpiredInboxes(ctx context.Context) (int64, error) {
	result, err := s.db.ExecContext(ctx, `
		DELETE FROM inboxes
		WHERE expires_at <= ?
	`, nowUnix())
	if err != nil {
		return 0, fmt.Errorf("delete expired inboxes: %w", err)
	}

	rows, err := result.RowsAffected()
	if err != nil {
		return 0, fmt.Errorf("delete expired inboxes: read affected rows: %w", err)
	}

	return rows, nil
}

func (s *SQLite) SaveEmail(ctx context.Context, email *models.Email) error {
	return insertEmail(ctx, s.db, email)
}

func (s *SQLite) SaveInboundMessage(ctx context.Context, email *models.Email, attachments []*models.Attachment) (err error) {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("save inbound message: begin transaction: %w", err)
	}
	defer func() {
		if err != nil {
			_ = tx.Rollback()
		}
	}()

	if err = insertEmail(ctx, tx, email); err != nil {
		return err
	}

	for _, attachment := range attachments {
		attachment.EmailID = email.ID
		if err = insertAttachment(ctx, tx, attachment); err != nil {
			return err
		}
	}

	if err = tx.Commit(); err != nil {
		return fmt.Errorf("save inbound message: commit transaction: %w", err)
	}

	return nil
}

func (s *SQLite) GetEmailsByInboxID(ctx context.Context, inboxID int64, limit, offset int) ([]*models.Email, error) {
	if limit <= 0 {
		limit = 50
	}
	if offset < 0 {
		offset = 0
	}

	rows, err := s.db.QueryContext(ctx, `
		SELECT id, inbox_id, message_id, sender, sender_name, recipient, subject,
		       body_html, body_text, raw_headers, size_bytes, has_attachments,
		       received_at, is_read
		FROM emails
		WHERE inbox_id = ?
		ORDER BY received_at DESC, id DESC
		LIMIT ? OFFSET ?
	`, inboxID, limit, offset)
	if err != nil {
		return nil, fmt.Errorf("get emails by inbox id: %w", err)
	}
	defer rows.Close()

	var emails []*models.Email
	for rows.Next() {
		email, err := scanEmail(rows)
		if err != nil {
			return nil, fmt.Errorf("get emails by inbox id: %w", err)
		}
		emails = append(emails, email)
	}

	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("get emails by inbox id: %w", err)
	}

	return emails, nil
}

func (s *SQLite) GetEmailByID(ctx context.Context, inboxID int64, emailID int64) (*models.Email, error) {
	row := s.db.QueryRowContext(ctx, `
		SELECT id, inbox_id, message_id, sender, sender_name, recipient, subject,
		       body_html, body_text, raw_headers, size_bytes, has_attachments,
		       received_at, is_read
		FROM emails
		WHERE inbox_id = ? AND id = ?
	`, inboxID, emailID)

	email, err := scanEmailRow(row)
	if err != nil {
		return nil, err
	}

	return email, nil
}

func (s *SQLite) MarkEmailAsRead(ctx context.Context, emailID int64) error {
	result, err := s.db.ExecContext(ctx, `
		UPDATE emails
		SET is_read = 1
		WHERE id = ?
	`, emailID)
	if err != nil {
		return fmt.Errorf("mark email as read: %w", err)
	}

	return requireRowsAffected("mark email as read", result)
}

func (s *SQLite) GetUnreadCount(ctx context.Context, inboxID int64) (int, error) {
	var count int
	err := s.db.QueryRowContext(ctx, `
		SELECT COUNT(*)
		FROM emails
		WHERE inbox_id = ? AND is_read = 0
	`, inboxID).Scan(&count)
	if err != nil {
		return 0, fmt.Errorf("get unread count: %w", err)
	}

	return count, nil
}

func (s *SQLite) DeleteEmail(ctx context.Context, inboxID int64, emailID int64) error {
	result, err := s.db.ExecContext(ctx, `
		DELETE FROM emails
		WHERE inbox_id = ? AND id = ?
	`, inboxID, emailID)
	if err != nil {
		return fmt.Errorf("delete email: %w", err)
	}

	return requireRowsAffected("delete email", result)
}

func (s *SQLite) SaveAttachment(ctx context.Context, attachment *models.Attachment) error {
	return insertAttachment(ctx, s.db, attachment)
}

func (s *SQLite) GetAttachmentsByEmailID(ctx context.Context, emailID int64) ([]*models.Attachment, error) {
	rows, err := s.db.QueryContext(ctx, `
		SELECT id, email_id, filename, content_type, size_bytes, content
		FROM attachments
		WHERE email_id = ?
		ORDER BY id ASC
	`, emailID)
	if err != nil {
		return nil, fmt.Errorf("get attachments by email id: %w", err)
	}
	defer rows.Close()

	var attachments []*models.Attachment
	for rows.Next() {
		attachment, err := scanAttachment(rows)
		if err != nil {
			return nil, fmt.Errorf("get attachments by email id: %w", err)
		}
		attachments = append(attachments, attachment)
	}

	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("get attachments by email id: %w", err)
	}

	return attachments, nil
}

func (s *SQLite) GetAttachment(ctx context.Context, attachmentID int64) (*models.Attachment, error) {
	row := s.db.QueryRowContext(ctx, `
		SELECT id, email_id, filename, content_type, size_bytes, content
		FROM attachments
		WHERE id = ?
	`, attachmentID)
	return scanAttachmentRow(row)
}

func (s *SQLite) CreateDomain(ctx context.Context, name string) (*models.Domain, error) {
	name = strings.ToLower(strings.TrimSpace(name))
	result, err := s.db.ExecContext(ctx, `
		INSERT INTO domains (name, enabled)
		VALUES (?, 1)
	`, name)
	if err != nil {
		return nil, mapSQLError("create domain", err)
	}

	id, err := result.LastInsertId()
	if err != nil {
		return nil, fmt.Errorf("create domain: read inserted id: %w", err)
	}

	row := s.db.QueryRowContext(ctx, `
		SELECT id, name, enabled, created_at
		FROM domains
		WHERE id = ?
	`, id)
	return scanDomain(row, "create domain")
}

func (s *SQLite) ListDomains(ctx context.Context, enabledOnly bool) ([]*models.Domain, error) {
	query := `
		SELECT id, name, enabled, created_at
		FROM domains
	`
	if enabledOnly {
		query += " WHERE enabled = 1"
	}
	query += " ORDER BY created_at ASC, id ASC"

	rows, err := s.db.QueryContext(ctx, query)
	if err != nil {
		return nil, fmt.Errorf("list domains: %w", err)
	}
	defer rows.Close()

	var domains []*models.Domain
	for rows.Next() {
		domain, err := scanDomain(rows, "list domains")
		if err != nil {
			return nil, err
		}
		domains = append(domains, domain)
	}

	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("list domains: %w", err)
	}

	return domains, nil
}

func (s *SQLite) GetDomain(ctx context.Context, name string) (*models.Domain, error) {
	row := s.db.QueryRowContext(ctx, `
		SELECT id, name, enabled, created_at
		FROM domains
		WHERE name = ? COLLATE NOCASE
	`, strings.TrimSpace(name))
	return scanDomain(row, "get domain")
}

func (s *SQLite) SetDomainEnabled(ctx context.Context, name string, enabled bool) error {
	result, err := s.db.ExecContext(ctx, `
		UPDATE domains
		SET enabled = ?
		WHERE name = ? COLLATE NOCASE
	`, boolToInt(enabled), strings.TrimSpace(name))
	if err != nil {
		return fmt.Errorf("set domain enabled: %w", err)
	}

	return requireRowsAffected("set domain enabled", result)
}

func (s *SQLite) IsDomainEnabled(ctx context.Context, name string) (bool, error) {
	var one int
	err := s.db.QueryRowContext(ctx, `
		SELECT 1
		FROM domains
		WHERE name = ? COLLATE NOCASE AND enabled = 1
	`, strings.TrimSpace(name)).Scan(&one)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return false, nil
		}
		return false, fmt.Errorf("is domain enabled: %w", err)
	}

	return true, nil
}

func (s *SQLite) Close() error {
	if s.db == nil {
		return nil
	}
	return s.db.Close()
}

func (s *SQLite) Ping(ctx context.Context) error {
	if err := s.db.PingContext(ctx); err != nil {
		return fmt.Errorf("ping sqlite: %w", err)
	}
	return nil
}

type execContexter interface {
	ExecContext(context.Context, string, ...any) (sql.Result, error)
}

type queryRowContexter interface {
	QueryRowContext(context.Context, string, ...any) *sql.Row
}

type scanner interface {
	Scan(dest ...any) error
}

func insertEmail(ctx context.Context, execer execContexter, email *models.Email) error {
	receivedAt := email.ReceivedAt
	if receivedAt.IsZero() {
		receivedAt = time.Now().UTC()
	}

	result, err := execer.ExecContext(ctx, `
		INSERT INTO emails (
			inbox_id, message_id, sender, sender_name, recipient, subject,
			body_html, body_text, raw_headers, size_bytes, has_attachments,
			received_at, is_read
		)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
	`, email.InboxID, nullableString(email.MessageID), email.Sender, nullableString(email.SenderName),
		email.Recipient, defaultString(email.Subject, "(no subject)"), nullableString(email.BodyHTML),
		nullableString(email.BodyText), nullableString(email.RawHeaders), email.SizeBytes,
		boolToInt(email.HasAttachments), toUnix(receivedAt), boolToInt(email.IsRead))
	if err != nil {
		return mapSQLError("save email", err)
	}

	id, err := result.LastInsertId()
	if err != nil {
		return fmt.Errorf("save email: read inserted id: %w", err)
	}

	email.ID = id
	email.ReceivedAt = receivedAt.UTC()
	return nil
}

func insertAttachment(ctx context.Context, execer execContexter, attachment *models.Attachment) error {
	result, err := execer.ExecContext(ctx, `
		INSERT INTO attachments (email_id, filename, content_type, size_bytes, content)
		VALUES (?, ?, ?, ?, ?)
	`, attachment.EmailID, attachment.Filename, attachment.ContentType, attachment.SizeBytes, attachment.Content)
	if err != nil {
		return mapSQLError("save attachment", err)
	}

	id, err := result.LastInsertId()
	if err != nil {
		return fmt.Errorf("save attachment: read inserted id: %w", err)
	}

	attachment.ID = id
	return nil
}

func scanInbox(row scanner, op string) (*models.Inbox, error) {
	inbox := &models.Inbox{}
	var createdAt int64
	var lastAccessedAt int64
	var expiresAt int64

	err := row.Scan(
		&inbox.ID,
		&inbox.Address,
		&inbox.LocalPart,
		&inbox.Token,
		&createdAt,
		&lastAccessedAt,
		&expiresAt,
	)
	if err != nil {
		return nil, mapScanError(op, err)
	}

	inbox.CreatedAt = fromUnix(createdAt)
	inbox.LastAccessedAt = fromUnix(lastAccessedAt)
	inbox.ExpiresAt = fromUnix(expiresAt)
	return inbox, nil
}

func scanDomain(row scanner, op string) (*models.Domain, error) {
	domain := &models.Domain{}
	var enabled int64
	var createdAt int64

	err := row.Scan(
		&domain.ID,
		&domain.Name,
		&enabled,
		&createdAt,
	)
	if err != nil {
		return nil, mapScanError(op, err)
	}

	domain.Enabled = enabled != 0
	domain.CreatedAt = fromUnix(createdAt)
	return domain, nil
}

func scanEmailRow(row scanner) (*models.Email, error) {
	email := &models.Email{}
	var messageID sql.NullString
	var senderName sql.NullString
	var bodyHTML sql.NullString
	var bodyText sql.NullString
	var rawHeaders sql.NullString
	var hasAttachments int64
	var isRead int64
	var receivedAt int64

	err := row.Scan(
		&email.ID,
		&email.InboxID,
		&messageID,
		&email.Sender,
		&senderName,
		&email.Recipient,
		&email.Subject,
		&bodyHTML,
		&bodyText,
		&rawHeaders,
		&email.SizeBytes,
		&hasAttachments,
		&receivedAt,
		&isRead,
	)
	if err != nil {
		return nil, mapScanError("scan email", err)
	}

	email.MessageID = messageID.String
	email.SenderName = senderName.String
	email.BodyHTML = bodyHTML.String
	email.BodyText = bodyText.String
	email.RawHeaders = rawHeaders.String
	email.HasAttachments = hasAttachments != 0
	email.IsRead = isRead != 0
	email.ReceivedAt = fromUnix(receivedAt)
	return email, nil
}

func scanEmail(row *sql.Rows) (*models.Email, error) {
	return scanEmailRow(row)
}

func scanAttachmentRow(row scanner) (*models.Attachment, error) {
	attachment := &models.Attachment{}
	err := row.Scan(
		&attachment.ID,
		&attachment.EmailID,
		&attachment.Filename,
		&attachment.ContentType,
		&attachment.SizeBytes,
		&attachment.Content,
	)
	if err != nil {
		return nil, mapScanError("scan attachment", err)
	}

	return attachment, nil
}

func scanAttachment(row *sql.Rows) (*models.Attachment, error) {
	return scanAttachmentRow(row)
}

func mapScanError(op string, err error) error {
	if errors.Is(err, sql.ErrNoRows) {
		return fmt.Errorf("%s: %w", op, ErrNotFound)
	}
	return fmt.Errorf("%s: %w", op, err)
}

func requireRowsAffected(op string, result sql.Result) error {
	rows, err := result.RowsAffected()
	if err != nil {
		return fmt.Errorf("%s: read affected rows: %w", op, err)
	}
	if rows == 0 {
		return fmt.Errorf("%s: %w", op, ErrNotFound)
	}
	return nil
}

func mapSQLError(op string, err error) error {
	if err == nil {
		return nil
	}

	lower := strings.ToLower(err.Error())
	if strings.Contains(lower, "unique constraint failed") {
		return fmt.Errorf("%s: %w", op, ErrConflict)
	}

	return fmt.Errorf("%s: %w", op, err)
}

func nullableString(value string) any {
	if value == "" {
		return nil
	}
	return value
}

func defaultString(value, fallback string) string {
	if value == "" {
		return fallback
	}
	return value
}

func boolToInt(value bool) int {
	if value {
		return 1
	}
	return 0
}

func nowUnix() int64 {
	return time.Now().UTC().Unix()
}

func toUnix(t time.Time) int64 {
	return t.UTC().Unix()
}

func fromUnix(v int64) time.Time {
	return time.Unix(v, 0).UTC()
}
