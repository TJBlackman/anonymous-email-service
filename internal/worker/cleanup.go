package worker

import (
	"context"
	"log/slog"
	"time"

	"anonymous-email-service/internal/repository"
)

type CleanupWorker struct {
	repo     repository.Repository
	interval time.Duration
	logger   *slog.Logger
	done     chan struct{}
}

func NewCleanupWorker(repo repository.Repository, interval time.Duration, logger *slog.Logger) *CleanupWorker {
	return &CleanupWorker{
		repo:     repo,
		interval: interval,
		logger:   logger,
		done:     make(chan struct{}),
	}
}

func (w *CleanupWorker) Start(ctx context.Context) {
	if w.done == nil {
		w.done = make(chan struct{})
	}

	w.runCleanup(ctx)

	ticker := time.NewTicker(w.interval)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			return
		case <-w.done:
			return
		case <-ticker.C:
			w.runCleanup(ctx)
		}
	}
}

func (w *CleanupWorker) Stop() {
	if w.done == nil {
		return
	}

	select {
	case <-w.done:
	default:
		close(w.done)
	}
}

func (w *CleanupWorker) runCleanup(ctx context.Context) {
	deletedInboxes, err := w.repo.DeleteExpiredInboxes(ctx)
	if err != nil {
		if w.logger != nil {
			w.logger.Error("cleanup failed", "error", err)
		}
		return
	}

	deletedSessions, err := w.repo.DeleteExpiredSessions(ctx)
	if err != nil {
		if w.logger != nil {
			w.logger.Error("session cleanup failed", "error", err)
		}
		return
	}

	if (deletedInboxes > 0 || deletedSessions > 0) && w.logger != nil {
		w.logger.Info("cleanup complete", "deleted_inboxes", deletedInboxes, "deleted_sessions", deletedSessions)
	}
}
