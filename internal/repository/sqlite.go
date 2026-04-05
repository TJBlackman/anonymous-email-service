package repository

import (
	"context"
	"errors"

	"anonymous-email-service/internal/models"
)

var ErrNotImplemented = errors.New("not implemented")

type SQLite struct {
	path string
}

func NewSQLite(path string) (Repository, error) {
	return &SQLite{path: path}, nil
}

func (s *SQLite) CreateInbox(context.Context, *models.Inbox) error {
	return ErrNotImplemented
}

func (s *SQLite) GetInboxByToken(context.Context, string) (*models.Inbox, error) {
	return nil, ErrNotImplemented
}

func (s *SQLite) GetInboxByAddress(context.Context, string) (*models.Inbox, error) {
	return nil, ErrNotImplemented
}

func (s *SQLite) UpdateLastAccessed(context.Context, int64) error {
	return ErrNotImplemented
}

func (s *SQLite) DeleteExpiredInboxes(context.Context) (int64, error) {
	return 0, ErrNotImplemented
}

func (s *SQLite) SaveEmail(context.Context, *models.Email) error {
	return ErrNotImplemented
}

func (s *SQLite) GetEmailsByInboxID(context.Context, int64, int, int) ([]*models.Email, error) {
	return nil, ErrNotImplemented
}

func (s *SQLite) GetEmailByID(context.Context, int64, int64) (*models.Email, error) {
	return nil, ErrNotImplemented
}

func (s *SQLite) MarkEmailAsRead(context.Context, int64) error {
	return ErrNotImplemented
}

func (s *SQLite) GetUnreadCount(context.Context, int64) (int, error) {
	return 0, ErrNotImplemented
}

func (s *SQLite) DeleteEmail(context.Context, int64, int64) error {
	return ErrNotImplemented
}

func (s *SQLite) SaveAttachment(context.Context, *models.Attachment) error {
	return ErrNotImplemented
}

func (s *SQLite) GetAttachmentsByEmailID(context.Context, int64) ([]*models.Attachment, error) {
	return nil, ErrNotImplemented
}

func (s *SQLite) GetAttachment(context.Context, int64) (*models.Attachment, error) {
	return nil, ErrNotImplemented
}

func (s *SQLite) Close() error {
	return nil
}
