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

// newAdminHandler builds a handler with the admin username configured and an
// admin password already set (the post-setup steady state). The hash is seeded
// directly into the settings store, mirroring what the /admin/setup flow writes.
func newAdminHandler(t *testing.T, repo *memoryRepository) http.Handler {
	t.Helper()
	hash, err := auth.HashPassword(testAdminPass, bcrypt.MinCost)
	if err != nil {
		t.Fatalf("HashPassword() error = %v", err)
	}
	repo.settings[AdminPasswordHashKey] = hash
	return newAdminHandlerNeedingSetup(t, repo)
}

// newAdminHandlerNeedingSetup builds a handler with ADMIN_USERNAME set but no
// admin password yet, i.e. the first-run setup state.
func newAdminHandlerNeedingSetup(t *testing.T, repo *memoryRepository) http.Handler {
	t.Helper()
	return newTestHandlerWithOptions(t, repo, Options{
		DefaultDomain: "example.test",
		SessionTTL:    7 * 24 * time.Hour,
		BcryptCost:    bcrypt.MinCost,
		AdminUsername: testAdminUser,
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

func TestAdminRedirectsToSetupWhenPasswordUnset(t *testing.T) {
	repo := newMemoryRepository()
	handler := newAdminHandlerNeedingSetup(t, repo)

	for _, path := range []string{"/admin", "/admin/login"} {
		t.Run(path, func(t *testing.T) {
			req := httptest.NewRequest(http.MethodGet, path, nil)
			rec := httptest.NewRecorder()
			handler.ServeHTTP(rec, req)

			if rec.Code != http.StatusSeeOther {
				t.Fatalf("status = %d, want %d", rec.Code, http.StatusSeeOther)
			}
			if got := rec.Header().Get("Location"); got != "/admin/setup" {
				t.Fatalf("Location = %q, want /admin/setup", got)
			}
		})
	}
}

func TestAdminSetupFormShownWhenPasswordUnset(t *testing.T) {
	repo := newMemoryRepository()
	handler := newAdminHandlerNeedingSetup(t, repo)

	req := httptest.NewRequest(http.MethodGet, "/admin/setup", nil)
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d", rec.Code, http.StatusOK)
	}
	if !strings.Contains(rec.Body.String(), "/admin/setup") {
		t.Fatalf("setup body missing form: %q", rec.Body.String())
	}
}

func TestAdminSetupPersistsPasswordAndLogsIn(t *testing.T) {
	repo := newMemoryRepository()
	handler := newAdminHandlerNeedingSetup(t, repo)

	const newPass = "correct-horse-battery"
	form := "password=" + newPass + "&confirm_password=" + newPass
	req := httptest.NewRequest(http.MethodPost, "http://service.test/admin/setup", strings.NewReader(form))
	req.Header.Set("Origin", "http://service.test")
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusSeeOther {
		t.Fatalf("status = %d, want %d", rec.Code, http.StatusSeeOther)
	}
	if got := rec.Header().Get("Location"); got != "/admin" {
		t.Fatalf("Location = %q, want /admin", got)
	}

	// The hash is persisted, not the plaintext, and it verifies.
	stored, err := repo.GetSetting(context.Background(), AdminPasswordHashKey)
	if err != nil {
		t.Fatalf("GetSetting() error = %v", err)
	}
	if stored == newPass {
		t.Fatal("stored admin password is plaintext")
	}
	if !auth.VerifyPassword(stored, newPass) {
		t.Fatal("stored hash does not verify against the chosen password")
	}

	// Setup logs the operator straight in: an admin session cookie is set and it
	// already grants access to /admin.
	var adminCookie *http.Cookie
	for _, c := range rec.Result().Cookies() {
		if c.Name == "admin_session" && c.Value != "" {
			adminCookie = c
		}
	}
	if adminCookie == nil {
		t.Fatal("setup did not set an admin_session cookie")
	}

	req = httptest.NewRequest(http.MethodGet, "/admin", nil)
	req.AddCookie(adminCookie)
	rec = httptest.NewRecorder()
	handler.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("post-setup /admin status = %d, want %d", rec.Code, http.StatusOK)
	}
}

// TestAdminSetupAcceptsSameOriginNavigationWithNullOrigin reproduces the reported
// /admin/setup 403: Chrome reaches the setup page via a redirect and submits the
// form with an opaque "Origin: null" but a truthful "Sec-Fetch-Site: same-origin".
// The CSRF check must trust Sec-Fetch-Site and let the setup through.
func TestAdminSetupAcceptsSameOriginNavigationWithNullOrigin(t *testing.T) {
	repo := newMemoryRepository()
	handler := newAdminHandlerNeedingSetup(t, repo)

	const newPass = "correct-horse-battery"
	form := "password=" + newPass + "&confirm_password=" + newPass
	req := httptest.NewRequest(http.MethodPost, "http://service.test/admin/setup", strings.NewReader(form))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.Header.Set("Origin", "null")
	req.Header.Set("Sec-Fetch-Site", "same-origin")
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusSeeOther {
		t.Fatalf("status = %d, want %d", rec.Code, http.StatusSeeOther)
	}
	if got := rec.Header().Get("Location"); got != "/admin" {
		t.Fatalf("Location = %q, want /admin", got)
	}
	if !auth.VerifyPassword(repo.settings[AdminPasswordHashKey], newPass) {
		t.Fatal("admin password was not persisted by same-origin setup")
	}
}

func TestAdminSetupRejectsShortAndMismatchedPasswords(t *testing.T) {
	cases := []struct{ name, form string }{
		{"too short", "password=short&confirm_password=short"},
		{"mismatch", "password=longenoughpassword&confirm_password=different-one!"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			repo := newMemoryRepository()
			handler := newAdminHandlerNeedingSetup(t, repo)

			req := httptest.NewRequest(http.MethodPost, "http://service.test/admin/setup", strings.NewReader(tc.form))
			req.Header.Set("Origin", "http://service.test")
			req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
			rec := httptest.NewRecorder()
			handler.ServeHTTP(rec, req)

			if rec.Code != http.StatusBadRequest {
				t.Fatalf("status = %d, want %d", rec.Code, http.StatusBadRequest)
			}
			if _, err := repo.GetSetting(context.Background(), AdminPasswordHashKey); err == nil {
				t.Fatal("invalid setup persisted a password")
			}
		})
	}
}

func TestAdminSetupClosedOncePasswordSet(t *testing.T) {
	repo := newMemoryRepository()
	handler := newAdminHandler(t, repo) // password already set

	// GET /admin/setup bounces to login once a password exists.
	req := httptest.NewRequest(http.MethodGet, "/admin/setup", nil)
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)
	if rec.Code != http.StatusSeeOther || rec.Header().Get("Location") != "/admin/login" {
		t.Fatalf("GET setup status = %d, Location = %q; want redirect to /admin/login", rec.Code, rec.Header().Get("Location"))
	}

	// POST /admin/setup must not overwrite the existing password.
	before, err := repo.GetSetting(context.Background(), AdminPasswordHashKey)
	if err != nil {
		t.Fatalf("GetSetting() error = %v", err)
	}
	form := "password=brand-new-password&confirm_password=brand-new-password"
	req = httptest.NewRequest(http.MethodPost, "http://service.test/admin/setup", strings.NewReader(form))
	req.Header.Set("Origin", "http://service.test")
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	rec = httptest.NewRecorder()
	handler.ServeHTTP(rec, req)
	if rec.Code != http.StatusSeeOther || rec.Header().Get("Location") != "/admin/login" {
		t.Fatalf("POST setup status = %d, Location = %q; want redirect to /admin/login", rec.Code, rec.Header().Get("Location"))
	}
	after, err := repo.GetSetting(context.Background(), AdminPasswordHashKey)
	if err != nil {
		t.Fatalf("GetSetting() error = %v", err)
	}
	if after != before {
		t.Fatal("setup overwrote an already-set admin password")
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
