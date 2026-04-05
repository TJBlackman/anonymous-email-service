package repository

import (
	"context"

	"anonymous-email-service/internal/models"
)

type Repository interface {
	CreateInbox(ctx context.Context, inbox *models.Inbox) error
	GetInboxByToken(ctx context.Context, token string) (*models.Inbox, error)
	GetInboxByAddress(ctx context.Context, address string) (*models.Inbox, error)
	UpdateLastAccessed(ctx context.Context, inboxID int64) error
	DeleteExpiredInboxes(ctx context.Context) (int64, error)

	SaveEmail(ctx context.Context, email *models.Email) error
	GetEmailsByInboxID(ctx context.Context, inboxID int64, limit, offset int) ([]*models.Email, error)
	GetEmailByID(ctx context.Context, inboxID, emailID int64) (*models.Email, error)
	MarkEmailAsRead(ctx context.Context, emailID int64) error
	GetUnreadCount(ctx context.Context, inboxID int64) (int, error)
	DeleteEmail(ctx context.Context, inboxID, emailID int64) error

	SaveAttachment(ctx context.Context, attachment *models.Attachment) error
	GetAttachmentsByEmailID(ctx context.Context, emailID int64) ([]*models.Attachment, error)
	GetAttachment(ctx context.Context, attachmentID int64) (*models.Attachment, error)

	Close() error
}
