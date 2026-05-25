package repository

import (
	"context"
	"errors"
	"time"

	"anonymous-email-service/internal/models"
)

var (
	ErrNotFound = errors.New("not found")
	ErrConflict = errors.New("conflict")
)

type Repository interface {
	CreateInbox(ctx context.Context, inbox *models.Inbox) error
	GetInboxByToken(ctx context.Context, token string) (*models.Inbox, error)
	GetInboxByAddress(ctx context.Context, address string) (*models.Inbox, error)
	UpdateLastAccessed(ctx context.Context, inboxID int64) error
	DeleteExpiredInboxes(ctx context.Context) (int64, error)

	SaveEmail(ctx context.Context, email *models.Email) error
	SaveInboundMessage(ctx context.Context, email *models.Email, attachments []*models.Attachment) error
	GetEmailsByInboxID(ctx context.Context, inboxID int64, limit, offset int) ([]*models.Email, error)
	GetEmailByID(ctx context.Context, inboxID, emailID int64) (*models.Email, error)
	MarkEmailAsRead(ctx context.Context, emailID int64) error
	GetUnreadCount(ctx context.Context, inboxID int64) (int, error)
	DeleteEmail(ctx context.Context, inboxID, emailID int64) error

	SaveAttachment(ctx context.Context, attachment *models.Attachment) error
	GetAttachmentsByEmailID(ctx context.Context, emailID int64) ([]*models.Attachment, error)
	GetAttachment(ctx context.Context, attachmentID int64) (*models.Attachment, error)

	CreateDomain(ctx context.Context, name string) (*models.Domain, error)
	ListDomains(ctx context.Context, enabledOnly bool) ([]*models.Domain, error)
	GetDomain(ctx context.Context, name string) (*models.Domain, error)
	SetDomainEnabled(ctx context.Context, name string, enabled bool) error
	IsDomainEnabled(ctx context.Context, name string) (bool, error)

	CreateSession(ctx context.Context, session *models.Session) error
	GetSession(ctx context.Context, token string) (*models.Session, *models.Inbox, error)
	DeleteSession(ctx context.Context, token string) error
	TouchSession(ctx context.Context, token string, expiresAt time.Time) error
	DeleteExpiredSessions(ctx context.Context) (int64, error)

	Ping(ctx context.Context) error
	Close() error
}
