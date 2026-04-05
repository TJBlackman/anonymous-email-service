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

func (w *CleanupWorker) Start(ctx context.Context) {
	if w.done == nil {
		w.done = make(chan struct{})
	}

	ticker := time.NewTicker(w.interval)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			return
		case <-w.done:
			return
		case <-ticker.C:
			if w.logger != nil {
				w.logger.Debug("cleanup tick skipped", "reason", "worker not implemented")
			}
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
