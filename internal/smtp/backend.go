package smtp

import (
	"log/slog"
	"time"

	"anonymous-email-service/internal/config"
	"anonymous-email-service/internal/repository"
	gosmtp "github.com/emersion/go-smtp"
)

type Backend struct {
	repo   repository.Repository
	domain string
	config *config.Config
	logger *slog.Logger
}

func NewBackend(repo repository.Repository, cfg *config.Config, logger *slog.Logger) *Backend {
	return &Backend{
		repo:   repo,
		domain: cfg.Domain,
		config: cfg,
		logger: logger,
	}
}

func NewServer(repo repository.Repository, cfg *config.Config, logger *slog.Logger) *gosmtp.Server {
	backend := NewBackend(repo, cfg, logger)

	server := gosmtp.NewServer(backend)
	server.Addr = cfg.SMTPListenAddr
	server.Domain = cfg.Domain
	server.MaxMessageBytes = cfg.MaxEmailSize
	server.MaxRecipients = 1
	server.AllowInsecureAuth = false
	server.ReadTimeout = 30 * time.Second
	server.WriteTimeout = 30 * time.Second

	return server
}

func (b *Backend) NewSession(c *gosmtp.Conn) (gosmtp.Session, error) {
	return &Session{
		backend: b,
	}, nil
}
