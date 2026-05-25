package web

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"anonymous-email-service/internal/auth"
	"golang.org/x/crypto/bcrypt"
)

const (
	testAdminUser = "admin"
	testAdminPass = "admin-secret"
)

// newAdminHandler builds a handler with admin credentials configured.
func newAdminHandler(t *testing.T, repo *memoryRepository) http.Handler {
	t.Helper()
	hash, err := auth.HashPassword(testAdminPass, bcrypt.MinCost)
	if err != nil {
		t.Fatalf("HashPassword() error = %v", err)
	}
	return newTestHandlerWithOptions(t, repo, Options{
		DefaultDomain:     "example.test",
		SessionTTL:        7 * 24 * time.Hour,
		BcryptCost:        bcrypt.MinCost,
		AdminUsername:     testAdminUser,
		AdminPasswordHash: hash,
	})
}

// adminLogin performs the admin login and returns the admin_session cookie.
func adminLogin(t *testing.T, handler http.Handler) *http.Cookie {
	t.Helper()
	form := "username=" + testAdminUser + "&password=" + testAdminPass
	req := httptest.NewRequest(http.MethodPost, "http://service.test/admin/login", strings.NewReader(form))
	req.Header.Set("Origin", "http://service.test")
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusSeeOther {
		t.Fatalf("admin login status = %d, want %d", rec.Code, http.StatusSeeOther)
	}
	for _, c := range rec.Result().Cookies() {
		if c.Name == "admin_session" && c.Value != "" {
			return c
		}
	}
	t.Fatal("admin login did not set admin_session cookie")
	return nil
}

func TestAdminIndexRequiresLogin(t *testing.T) {
	repo := newMemoryRepository()
	handler := newAdminHandler(t, repo)

	req := httptest.NewRequest(http.MethodGet, "/admin", nil)
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusSeeOther {
		t.Fatalf("status = %d, want %d", rec.Code, http.StatusSeeOther)
	}
	if got := rec.Header().Get("Location"); got != "/admin/login" {
		t.Fatalf("Location = %q, want /admin/login", got)
	}
}

func TestAdminDisabledWhenUnconfigured(t *testing.T) {
	repo := newMemoryRepository()
	handler := newTestHandler(t, repo) // no admin credentials

	req := httptest.NewRequest(http.MethodGet, "/admin", nil)
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusServiceUnavailable {
		t.Fatalf("status = %d, want %d", rec.Code, http.StatusServiceUnavailable)
	}
}

func TestAdminLoginRejectsBadCredentials(t *testing.T) {
	repo := newMemoryRepository()
	handler := newAdminHandler(t, repo)

	req := httptest.NewRequest(http.MethodPost, "http://service.test/admin/login", strings.NewReader("username=admin&password=wrong"))
	req.Header.Set("Origin", "http://service.test")
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("status = %d, want %d", rec.Code, http.StatusUnauthorized)
	}
	for _, c := range rec.Result().Cookies() {
		if c.Name == "admin_session" && c.Value != "" {
			t.Fatal("bad admin login set a session cookie")
		}
	}
}

func TestAdminIndexListsDomainsWhenAuthenticated(t *testing.T) {
	repo := newMemoryRepository()
	handler := newAdminHandler(t, repo)
	cookie := adminLogin(t, handler)

	req := httptest.NewRequest(http.MethodGet, "/admin", nil)
	req.AddCookie(cookie)
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d", rec.Code, http.StatusOK)
	}
	if !strings.Contains(rec.Body.String(), "example.test") {
		t.Fatalf("admin body missing seeded domain: %q", rec.Body.String())
	}
}

func TestAdminAddDomain(t *testing.T) {
	repo := newMemoryRepository()
	handler := newAdminHandler(t, repo)
	cookie := adminLogin(t, handler)

	req := httptest.NewRequest(http.MethodPost, "http://service.test/admin/domains", strings.NewReader("name=New.Test"))
	req.AddCookie(cookie)
	req.Header.Set("Origin", "http://service.test")
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusSeeOther {
		t.Fatalf("status = %d, want %d", rec.Code, http.StatusSeeOther)
	}
	got, err := repo.GetDomain(context.Background(), "new.test")
	if err != nil {
		t.Fatalf("GetDomain() error = %v", err)
	}
	if !got.Enabled {
		t.Fatal("added domain should be enabled")
	}
}

func TestAdminAddDomainDuplicate(t *testing.T) {
	repo := newMemoryRepository()
	handler := newAdminHandler(t, repo)
	cookie := adminLogin(t, handler)

	req := httptest.NewRequest(http.MethodPost, "http://service.test/admin/domains", strings.NewReader("name=example.test"))
	req.AddCookie(cookie)
	req.Header.Set("Origin", "http://service.test")
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusConflict {
		t.Fatalf("status = %d, want %d for duplicate", rec.Code, http.StatusConflict)
	}
	if !strings.Contains(rec.Body.String(), "already exists") {
		t.Fatalf("body missing duplicate error: %q", rec.Body.String())
	}
}

func TestAdminAddDomainInvalid(t *testing.T) {
	repo := newMemoryRepository()
	handler := newAdminHandler(t, repo)
	cookie := adminLogin(t, handler)

	req := httptest.NewRequest(http.MethodPost, "http://service.test/admin/domains", strings.NewReader("name=bad@domain"))
	req.AddCookie(cookie)
	req.Header.Set("Origin", "http://service.test")
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want %d for invalid domain", rec.Code, http.StatusBadRequest)
	}
}

func TestAdminToggleDomain(t *testing.T) {
	repo := newMemoryRepository()
	handler := newAdminHandler(t, repo)
	cookie := adminLogin(t, handler)

	req := httptest.NewRequest(http.MethodPost, "http://service.test/admin/domains/example.test/toggle", nil)
	req.AddCookie(cookie)
	req.Header.Set("Origin", "http://service.test")
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusSeeOther {
		t.Fatalf("status = %d, want %d", rec.Code, http.StatusSeeOther)
	}
	got, err := repo.GetDomain(context.Background(), "example.test")
	if err != nil {
		t.Fatalf("GetDomain() error = %v", err)
	}
	if got.Enabled {
		t.Fatal("domain should have been disabled by toggle")
	}
}

func TestAdminRejectsCrossOriginPost(t *testing.T) {
	repo := newMemoryRepository()
	handler := newAdminHandler(t, repo)
	cookie := adminLogin(t, handler)

	req := httptest.NewRequest(http.MethodPost, "http://service.test/admin/domains", strings.NewReader("name=evil.test"))
	req.AddCookie(cookie)
	req.Header.Set("Origin", "http://attacker.test")
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)

	if rec.Code == http.StatusSeeOther {
		t.Fatal("cross-origin admin POST should be rejected by same-origin check")
	}
	if _, err := repo.GetDomain(context.Background(), "evil.test"); err == nil {
		t.Fatal("cross-origin POST created a domain")
	}
}

func TestMailboxSessionCannotAccessAdmin(t *testing.T) {
	repo := newMemoryRepository()
	handler := newAdminHandler(t, repo)
	_, mailboxCookie := loginInbox(repo, "user@example.test")

	req := httptest.NewRequest(http.MethodGet, "/admin", nil)
	req.AddCookie(mailboxCookie) // mailbox session, not an admin session
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusSeeOther || rec.Header().Get("Location") != "/admin/login" {
		t.Fatalf("status = %d, Location = %q; mailbox session must not reach /admin", rec.Code, rec.Header().Get("Location"))
	}
}
