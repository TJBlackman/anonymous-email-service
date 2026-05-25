package web

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"anonymous-email-service/internal/models"
)

func TestNewInboxUsesSelectedDomain(t *testing.T) {
	repo := newMemoryRepository()
	if _, err := repo.CreateDomain(context.Background(), "second.test"); err != nil {
		t.Fatalf("CreateDomain() error = %v", err)
	}
	handler := newTestHandler(t, repo)

	req := httptest.NewRequest(http.MethodPost, "http://service.test/inbox/new", strings.NewReader("domain=second.test"))
	req.Header.Set("Origin", "http://service.test")
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusSeeOther {
		t.Fatalf("status = %d, want %d", rec.Code, http.StatusSeeOther)
	}

	// SessionMiddleware auto-creates an inbox (default domain) before
	// HandleNewInbox runs, so the response carries two inbox_token cookies. The
	// last one is the freshly rotated inbox created with the selected domain.
	var token string
	for _, c := range rec.Result().Cookies() {
		if c.Name == "inbox_token" {
			token = c.Value
		}
	}
	if token == "" {
		t.Fatal("expected inbox cookie")
	}
	inbox, err := repo.GetInboxByToken(context.Background(), token)
	if err != nil {
		t.Fatalf("GetInboxByToken() error = %v", err)
	}
	if !strings.HasSuffix(inbox.Address, "@second.test") {
		t.Fatalf("address = %q, want @second.test suffix", inbox.Address)
	}
}

func TestNewInboxRejectsDisabledDomain(t *testing.T) {
	repo := newMemoryRepository()
	if _, err := repo.CreateDomain(context.Background(), "second.test"); err != nil {
		t.Fatalf("CreateDomain() error = %v", err)
	}
	if err := repo.SetDomainEnabled(context.Background(), "second.test", false); err != nil {
		t.Fatalf("SetDomainEnabled() error = %v", err)
	}
	handler := newTestHandler(t, repo)

	req := httptest.NewRequest(http.MethodPost, "http://service.test/inbox/new", strings.NewReader("domain=second.test"))
	req.Header.Set("Origin", "http://service.test")
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want %d for disabled domain", rec.Code, http.StatusBadRequest)
	}
}

func TestIndexRendersDomainDropdown(t *testing.T) {
	repo := newMemoryRepository()
	if _, err := repo.CreateDomain(context.Background(), "second.test"); err != nil {
		t.Fatalf("CreateDomain() error = %v", err)
	}
	handler := newTestHandler(t, repo)

	req := httptest.NewRequest(http.MethodGet, "/", nil)
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

func TestInboxCreationFailsWithNoDomains(t *testing.T) {
	repo := &memoryRepository{
		nextInboxID:    1,
		nextDomainID:   1,
		inboxes:        map[int64]*models.Inbox{},
		inboxTokens:    map[string]int64{},
		inboxAddresses: map[string]int64{},
		emails:         map[int64]*models.Email{},
		attachments:    map[int64]*models.Attachment{},
	}
	// No seeded domain and no DefaultDomain option.
	handler := newTestHandlerWithOptions(t, repo, Options{InboxTTL: 24 * time.Hour})

	req := httptest.NewRequest(http.MethodGet, "/", nil)
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusServiceUnavailable {
		t.Fatalf("status = %d, want %d when no domains configured", rec.Code, http.StatusServiceUnavailable)
	}
}
