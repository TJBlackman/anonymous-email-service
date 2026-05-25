package web

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestAdminIndexListsDomainsWithoutInboxCookie(t *testing.T) {
	repo := newMemoryRepository()
	handler := newTestHandler(t, repo)

	req := httptest.NewRequest(http.MethodGet, "/admin", nil)
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d", rec.Code, http.StatusOK)
	}
	// Admin lives outside SessionMiddleware: it must not create an inbox cookie.
	for _, c := range rec.Result().Cookies() {
		if c.Name == "inbox_token" {
			t.Fatal("admin page set an inbox cookie; should be outside SessionMiddleware")
		}
	}
	if !strings.Contains(rec.Body.String(), "example.test") {
		t.Fatalf("admin body missing seeded domain: %q", rec.Body.String())
	}
}

func TestAdminAddDomain(t *testing.T) {
	repo := newMemoryRepository()
	handler := newTestHandler(t, repo)

	req := httptest.NewRequest(http.MethodPost, "http://service.test/admin/domains", strings.NewReader("name=New.Test"))
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
	handler := newTestHandler(t, repo)

	req := httptest.NewRequest(http.MethodPost, "http://service.test/admin/domains", strings.NewReader("name=example.test"))
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
	handler := newTestHandler(t, repo)

	req := httptest.NewRequest(http.MethodPost, "http://service.test/admin/domains", strings.NewReader("name=bad@domain"))
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
	handler := newTestHandler(t, repo)

	req := httptest.NewRequest(http.MethodPost, "http://service.test/admin/domains/example.test/toggle", nil)
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
	handler := newTestHandler(t, repo)

	req := httptest.NewRequest(http.MethodPost, "http://service.test/admin/domains", strings.NewReader("name=evil.test"))
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
