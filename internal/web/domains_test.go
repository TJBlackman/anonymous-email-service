package web

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"anonymous-email-service/internal/models"
	"golang.org/x/crypto/bcrypt"
)

func registerOptions() Options {
	return Options{
		DefaultDomain: "example.test",
		SessionTTL:    7 * 24 * time.Hour,
		BcryptCost:    bcrypt.MinCost,
	}
}

func TestRegisterUsesSelectedDomain(t *testing.T) {
	repo := newMemoryRepository()
	if _, err := repo.CreateDomain(context.Background(), "second.test"); err != nil {
		t.Fatalf("CreateDomain() error = %v", err)
	}
	handler := newTestHandlerWithOptions(t, repo, registerOptions())

	req := httptest.NewRequest(http.MethodPost, "http://service.test/register", strings.NewReader("domain=second.test"))
	req.Header.Set("Origin", "http://service.test")
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d", rec.Code, http.StatusOK)
	}
	if len(repo.inboxes) != 1 {
		t.Fatalf("inboxes = %d, want 1", len(repo.inboxes))
	}
	for _, inbox := range repo.inboxes {
		if !strings.HasSuffix(inbox.Address, "@second.test") {
			t.Fatalf("address = %q, want @second.test suffix", inbox.Address)
		}
	}
}

func TestRegisterFallsBackWhenDomainDisabled(t *testing.T) {
	repo := newMemoryRepository()
	if _, err := repo.CreateDomain(context.Background(), "second.test"); err != nil {
		t.Fatalf("CreateDomain() error = %v", err)
	}
	if err := repo.SetDomainEnabled(context.Background(), "second.test", false); err != nil {
		t.Fatalf("SetDomainEnabled() error = %v", err)
	}
	handler := newTestHandlerWithOptions(t, repo, registerOptions())

	req := httptest.NewRequest(http.MethodPost, "http://service.test/register", strings.NewReader("domain=second.test"))
	req.Header.Set("Origin", "http://service.test")
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)

	// A stale/disabled domain selection falls back to the default enabled domain
	// rather than failing the registration.
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d", rec.Code, http.StatusOK)
	}
	for _, inbox := range repo.inboxes {
		if !strings.HasSuffix(inbox.Address, "@example.test") {
			t.Fatalf("address = %q, want fallback @example.test suffix", inbox.Address)
		}
	}
}

func TestRegisterFormRendersDomainDropdown(t *testing.T) {
	repo := newMemoryRepository()
	if _, err := repo.CreateDomain(context.Background(), "second.test"); err != nil {
		t.Fatalf("CreateDomain() error = %v", err)
	}
	handler := newTestHandler(t, repo)

	req := httptest.NewRequest(http.MethodGet, "/register", nil)
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)

	body := rec.Body.String()
	if !strings.Contains(body, `name="domain"`) {
		t.Fatalf("body missing domain select: %q", body)
	}
	for _, name := range []string{"example.test", "second.test"} {
		if !strings.Contains(body, name) {
			t.Fatalf("body missing domain option %q", name)
		}
	}
}

func TestRegisterFailsWithNoDomains(t *testing.T) {
	repo := &memoryRepository{
		nextInboxID:    1,
		nextDomainID:   1,
		inboxes:        map[int64]*models.Inbox{},
		inboxTokens:    map[string]int64{},
		inboxAddresses: map[string]int64{},
		emails:         map[int64]*models.Email{},
		attachments:    map[int64]*models.Attachment{},
		sessions:       map[string]*models.Session{},
	}
	// No seeded domain and no DefaultDomain option.
	handler := newTestHandlerWithOptions(t, repo, Options{SessionTTL: 7 * 24 * time.Hour, BcryptCost: bcrypt.MinCost})

	req := httptest.NewRequest(http.MethodPost, "http://service.test/register", strings.NewReader("domain="))
	req.Header.Set("Origin", "http://service.test")
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusServiceUnavailable {
		t.Fatalf("status = %d, want %d when no domains configured", rec.Code, http.StatusServiceUnavailable)
	}
}
