package main

import (
	"context"
	"errors"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"anonymous-email-service/internal/auth"
	"anonymous-email-service/internal/config"
	"anonymous-email-service/internal/ratelimit"
	"anonymous-email-service/internal/repository"
	appsmtp "anonymous-email-service/internal/smtp"
	"anonymous-email-service/internal/web"
	"anonymous-email-service/internal/worker"
	gosmtp "github.com/emersion/go-smtp"
)

func main() {
	if err := run(); err != nil {
		slog.New(slog.NewTextHandler(os.Stderr, nil)).Error("server exited", "error", err)
		os.Exit(1)
	}
}

func run() error {
	cfg, err := config.Load()
	if err != nil {
		return err
	}

	logger := slog.New(slog.NewTextHandler(os.Stdout, &slog.HandlerOptions{
		Level: config.LogLevel(cfg.LogLevel),
	}))

	inboxCreateLimiter := ratelimit.NewFixedWindowLimiter(cfg.InboxCreateLimitPerHour, time.Hour)
	smtpConnectionLimiter := ratelimit.NewFixedWindowLimiter(cfg.SMTPConnectionLimitPerMin, time.Minute)

	repo, err := repository.NewSQLite(cfg.DatabasePath)
	if err != nil {
		return err
	}
	defer repo.Close()

	if err := seedDomain(context.Background(), repo, cfg.Domain, logger); err != nil {
		return err
	}

	adminPasswordHash, err := resolveAdminCredentials(cfg, logger)
	if err != nil {
		return err
	}

	handler, err := web.RegisterRoutes(repo, logger, "templates", web.Options{
		DefaultDomain:      cfg.Domain,
		CookieSecure:       cfg.CookieSecure,
		SessionTTL:         cfg.SessionTTL,
		BcryptCost:         cfg.BcryptCost,
		AdminUsername:      cfg.AdminUsername,
		AdminPasswordHash:  adminPasswordHash,
		CreateInboxLimiter: inboxCreateLimiter,
	})
	if err != nil {
		return err
	}

	httpServer := &http.Server{
		Addr:              cfg.HTTPListenAddr,
		Handler:           handler,
		ReadHeaderTimeout: 5 * time.Second,
	}
	smtpServer := appsmtp.NewServer(repo, cfg, logger, smtpConnectionLimiter)
	cleanupWorker := worker.NewCleanupWorker(repo, cfg.CleanupInterval, logger)

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	serverErr := make(chan error, 2)
	go cleanupWorker.Start(ctx)
	go func() {
		logger.Info("http server listening", "addr", cfg.HTTPListenAddr, "domain", cfg.Domain)
		err := httpServer.ListenAndServe()
		if err != nil && !errors.Is(err, http.ErrServerClosed) {
			serverErr <- err
			return
		}
		serverErr <- nil
	}()
	go func() {
		logger.Info("smtp server listening", "addr", cfg.SMTPListenAddr, "domain", cfg.Domain)
		err := smtpServer.ListenAndServe()
		if err != nil && !errors.Is(err, gosmtp.ErrServerClosed) {
			serverErr <- err
			return
		}
		serverErr <- nil
	}()

	select {
	case <-ctx.Done():
		logger.Info("shutdown signal received")
	case err := <-serverErr:
		stop()
		if err != nil {
			return shutdownServers(httpServer, smtpServer, err)
		}
	}

	shutdownCtx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	if err := smtpServer.Shutdown(shutdownCtx); err != nil && !errors.Is(err, gosmtp.ErrServerClosed) {
		return err
	}
	if err := httpServer.Shutdown(shutdownCtx); err != nil && !errors.Is(err, http.ErrServerClosed) {
		return err
	}
	cleanupWorker.Stop()

	var firstErr error
	for i := 0; i < 2; i++ {
		err := <-serverErr
		if err != nil && firstErr == nil {
			firstErr = err
		}
	}

	return firstErr
}

// resolveAdminCredentials hashes the configured admin password for use by the
// admin auth realm. Both ADMIN_USERNAME and ADMIN_PASSWORD must be set to enable
// the admin area; otherwise it logs a warning and returns an empty hash, leaving
// /admin disabled rather than open.
func resolveAdminCredentials(cfg *config.Config, logger *slog.Logger) (string, error) {
	if cfg.AdminUsername == "" || cfg.AdminPassword == "" {
		logger.Warn("admin credentials not configured; /admin is disabled (set ADMIN_USERNAME and ADMIN_PASSWORD to enable)")
		return "", nil
	}

	hash, err := auth.HashPassword(cfg.AdminPassword, cfg.BcryptCost)
	if err != nil {
		return "", err
	}
	return hash, nil
}

// seedDomain inserts the optional DOMAIN bootstrap seed as the first enabled
// domain when the domains table is empty. This keeps deployments that set
// DOMAIN working without manual admin steps. When no enabled domain exists
// afterward, it logs a warning since inbox creation and SMTP acceptance will
// fail until an admin adds one via /admin.
func seedDomain(ctx context.Context, repo repository.Repository, seed string, logger *slog.Logger) error {
	domains, err := repo.ListDomains(ctx, false)
	if err != nil {
		return err
	}

	if len(domains) == 0 && seed != "" {
		if _, err := repo.CreateDomain(ctx, seed); err != nil && !errors.Is(err, repository.ErrConflict) {
			return err
		}
		logger.Info("seeded bootstrap domain", "domain", seed)
		return nil
	}

	enabled, err := repo.ListDomains(ctx, true)
	if err != nil {
		return err
	}
	if len(enabled) == 0 {
		logger.Warn("no enabled domains configured; inbox creation and SMTP will be rejected until a domain is added at /admin")
	}
	return nil
}

func shutdownServers(httpServer *http.Server, smtpServer *gosmtp.Server, cause error) error {
	shutdownCtx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	_ = smtpServer.Shutdown(shutdownCtx)
	_ = httpServer.Shutdown(shutdownCtx)

	return cause
}
