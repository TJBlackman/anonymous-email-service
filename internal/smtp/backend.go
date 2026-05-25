package smtp

import (
	"context"
	"log/slog"
	"strings"
	"time"

	"anonymous-email-service/internal/clientip"
	"anonymous-email-service/internal/config"
	"anonymous-email-service/internal/ratelimit"
	"anonymous-email-service/internal/repository"
	gosmtp "github.com/emersion/go-smtp"
)

type Backend struct {
	repo              repository.Repository
	domains           *domainCache
	config            *config.Config
	logger            *slog.Logger
	connectionLimiter *ratelimit.FixedWindowLimiter
}

func NewBackend(repo repository.Repository, cfg *config.Config, logger *slog.Logger, limiter *ratelimit.FixedWindowLimiter) *Backend {
	return &Backend{
		repo:              repo,
		domains:           newDomainCache(domainCacheTTL),
		config:            cfg,
		logger:            logger,
		connectionLimiter: limiter,
	}
}

// domainAllowed reports whether inbound mail should be accepted for the given
// recipient domain, consulting the short-lived cache before the database.
func (b *Backend) domainAllowed(ctx context.Context, name string) (bool, error) {
	name = strings.ToLower(name)
	if enabled, ok := b.domains.get(name); ok {
		return enabled, nil
	}

	enabled, err := b.repo.IsDomainEnabled(ctx, name)
	if err != nil {
		return false, err
	}
	b.domains.set(name, enabled)
	return enabled, nil
}

func NewServer(repo repository.Repository, cfg *config.Config, logger *slog.Logger, limiter *ratelimit.FixedWindowLimiter) *gosmtp.Server {
	backend := NewBackend(repo, cfg, logger, limiter)

	server := gosmtp.NewServer(backend)
	server.Addr = cfg.SMTPListenAddr
	server.Domain = cfg.SMTPHostname
	server.MaxMessageBytes = cfg.MaxEmailSize
	server.MaxRecipients = 1
	server.AllowInsecureAuth = false
	server.ReadTimeout = 30 * time.Second
	server.WriteTimeout = 30 * time.Second

	return server
}

func (b *Backend) NewSession(c *gosmtp.Conn) (gosmtp.Session, error) {
	if b.connectionLimiter != nil {
		decision := b.connectionLimiter.Allow(clientip.FromSMTPConn(c))
		if !decision.Allowed {
			return nil, smtpError(421, gosmtp.EnhancedCode{4, 7, 0}, "rate limit exceeded")
		}
	}

	return &Session{
		backend: b,
	}, nil
}
