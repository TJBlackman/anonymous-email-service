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

	"anonymous-email-service/internal/config"
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

	repo, err := repository.NewSQLite(cfg.DatabasePath)
	if err != nil {
		return err
	}
	defer repo.Close()

	handler, err := web.RegisterRoutes(repo, logger, "templates", web.Options{
		Domain:   cfg.Domain,
		InboxTTL: cfg.InboxTTL,
	})
	if err != nil {
		return err
	}

	httpServer := &http.Server{
		Addr:              cfg.HTTPListenAddr,
		Handler:           handler,
		ReadHeaderTimeout: 5 * time.Second,
	}
	smtpServer := appsmtp.NewServer(repo, cfg, logger)
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

func shutdownServers(httpServer *http.Server, smtpServer *gosmtp.Server, cause error) error {
	shutdownCtx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	_ = smtpServer.Shutdown(shutdownCtx)
	_ = httpServer.Shutdown(shutdownCtx)

	return cause
}
