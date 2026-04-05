package worker

import (
	"context"
	"sync"
	"testing"
	"time"

	"anonymous-email-service/internal/models"
)

func TestCleanupWorkerRunsImmediatelyAndRepeats(t *testing.T) {
	repo := &stubRepository{
		deleteExpiredFn: func(context.Context) (int64, error) { return 1, nil },
	}
	worker := NewCleanupWorker(repo, 20*time.Millisecond, nil)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	done := make(chan struct{})
	go func() {
		worker.Start(ctx)
		close(done)
	}()

	waitForCount(t, repo, 1)
	waitForCount(t, repo, 2)

	cancel()
	select {
	case <-done:
	case <-time.After(250 * time.Millisecond):
		t.Fatal("worker did not stop after context cancellation")
	}
}

func TestCleanupWorkerStopIsIdempotent(t *testing.T) {
	repo := &stubRepository{
		deleteExpiredFn: func(context.Context) (int64, error) { return 0, nil },
	}
	worker := NewCleanupWorker(repo, time.Hour, nil)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	done := make(chan struct{})
	go func() {
		worker.Start(ctx)
		close(done)
	}()

	waitForCount(t, repo, 1)
	worker.Stop()
	worker.Stop()

	select {
	case <-done:
	case <-time.After(250 * time.Millisecond):
		t.Fatal("worker did not stop after Stop()")
	}
}

func waitForCount(t *testing.T, repo *stubRepository, want int) {
	t.Helper()

	deadline := time.Now().Add(500 * time.Millisecond)
	for time.Now().Before(deadline) {
		if repo.calls() >= want {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}

	t.Fatalf("DeleteExpiredInboxes() count = %d, want at least %d", repo.calls(), want)
}

type stubRepository struct {
	mu              sync.Mutex
	deleteCalls     int
	deleteExpiredFn func(context.Context) (int64, error)
}

func (s *stubRepository) calls() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.deleteCalls
}

func (s *stubRepository) DeleteExpiredInboxes(ctx context.Context) (int64, error) {
	s.mu.Lock()
	s.deleteCalls++
	s.mu.Unlock()
	if s.deleteExpiredFn != nil {
		return s.deleteExpiredFn(ctx)
	}
	return 0, nil
}

func (s *stubRepository) CreateInbox(context.Context, *models.Inbox) error { return nil }
func (s *stubRepository) GetInboxByToken(context.Context, string) (*models.Inbox, error) {
	return nil, nil
}
func (s *stubRepository) GetInboxByAddress(context.Context, string) (*models.Inbox, error) {
	return nil, nil
}
func (s *stubRepository) UpdateLastAccessed(context.Context, int64) error { return nil }
func (s *stubRepository) SaveEmail(context.Context, *models.Email) error  { return nil }
func (s *stubRepository) SaveInboundMessage(context.Context, *models.Email, []*models.Attachment) error {
	return nil
}
func (s *stubRepository) GetEmailsByInboxID(context.Context, int64, int, int) ([]*models.Email, error) {
	return nil, nil
}
func (s *stubRepository) GetEmailByID(context.Context, int64, int64) (*models.Email, error) {
	return nil, nil
}
func (s *stubRepository) MarkEmailAsRead(context.Context, int64) error             { return nil }
func (s *stubRepository) GetUnreadCount(context.Context, int64) (int, error)       { return 0, nil }
func (s *stubRepository) DeleteEmail(context.Context, int64, int64) error          { return nil }
func (s *stubRepository) SaveAttachment(context.Context, *models.Attachment) error { return nil }
func (s *stubRepository) GetAttachmentsByEmailID(context.Context, int64) ([]*models.Attachment, error) {
	return nil, nil
}
func (s *stubRepository) GetAttachment(context.Context, int64) (*models.Attachment, error) {
	return nil, nil
}
func (s *stubRepository) Close() error { return nil }
