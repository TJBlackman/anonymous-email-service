package web

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"net/url"
	"path/filepath"
	"runtime"
	"slices"
	"strconv"
	"strings"
	"testing"
	"time"

	"anonymous-email-service/internal/auth"
	"anonymous-email-service/internal/models"
	"anonymous-email-service/internal/ratelimit"
	"anonymous-email-service/internal/repository"
	"golang.org/x/crypto/bcrypt"
)

// loginInbox seeds a mailbox account and an active session for it, returning the
// inbox and the session cookie a request should carry to be authenticated.
func loginInbox(repo *memoryRepository, address string) (*models.Inbox, *http.Cookie) {
	localPart := address
	if at := strings.IndexByte(address, '@'); at >= 0 {
		localPart = address[:at]
	}
	inbox := repo.seedInbox(address, localPart, "token-"+localPart, "hash-"+localPart, neverExpires)
	repo.seedSession("session-"+localPart, inbox.ID, time.Now().Add(7*24*time.Hour))
	return inbox, &http.Cookie{Name: "session", Value: "session-" + localPart}
}

func TestHealthBypassesAuth(t *testing.T) {
	repo := newMemoryRepository()
	handler := newTestHandler(t, repo)

	req := httptest.NewRequest(http.MethodGet, "/health", nil)
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d", rec.Code, http.StatusOK)
	}
	if got := rec.Header().Get("Set-Cookie"); got != "" {
		t.Fatalf("Set-Cookie = %q, want empty", got)
	}
	assertSecurityHeaders(t, rec.Result())
}

func TestLandingPageShownToAnonymousVisitor(t *testing.T) {
	repo := newMemoryRepository()
	handler := newTestHandler(t, repo)

	req := httptest.NewRequest(http.MethodGet, "/", nil)
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d", rec.Code, http.StatusOK)
	}
	if len(repo.inboxes) != 0 {
		t.Fatalf("inboxes = %d, want 0 (no auto-create)", len(repo.inboxes))
	}
	body := rec.Body.String()
	if !strings.Contains(body, "/register") || !strings.Contains(body, "/login") {
		t.Fatalf("landing body missing create/login links: %q", body)
	}
}

func TestAppRequiresLoginRedirectsHTML(t *testing.T) {
	repo := newMemoryRepository()
	handler := newTestHandler(t, repo)

	req := httptest.NewRequest(http.MethodGet, "/app", nil)
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusSeeOther {
		t.Fatalf("status = %d, want %d", rec.Code, http.StatusSeeOther)
	}
	if got := rec.Header().Get("Location"); got != "/login" {
		t.Fatalf("Location = %q, want /login", got)
	}
}

func TestAppAPIRequiresLoginReturns401JSON(t *testing.T) {
	repo := newMemoryRepository()
	handler := newTestHandler(t, repo)

	req := httptest.NewRequest(http.MethodGet, "/app/api/emails", nil)
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("status = %d, want %d", rec.Code, http.StatusUnauthorized)
	}
	if ct := rec.Header().Get("Content-Type"); !strings.HasPrefix(ct, "application/json") {
		t.Fatalf("Content-Type = %q, want application/json", ct)
	}
	if !strings.Contains(rec.Body.String(), "authentication required") {
		t.Fatalf("body = %q, want auth error JSON", rec.Body.String())
	}
}

func TestRegisterCreatesAccountAndRevealsPasswordOnce(t *testing.T) {
	repo := newMemoryRepository()
	handler := newTestHandler(t, repo)

	req := httptest.NewRequest(http.MethodPost, "http://service.test/register", strings.NewReader("local_part=adam&domain=example.test"))
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
	var created *models.Inbox
	for _, inbox := range repo.inboxes {
		created = inbox
	}
	if created.Address != "adam@example.test" {
		t.Fatalf("Address = %q, want %q", created.Address, "adam@example.test")
	}
	if created.LocalPart != "adam" {
		t.Fatalf("LocalPart = %q, want %q", created.LocalPart, "adam")
	}
	if created.PasswordHash == "" {
		t.Fatal("created inbox has no password hash")
	}
	if !created.ExpiresAt.Equal(neverExpires) {
		t.Fatalf("ExpiresAt = %v, want neverExpires", created.ExpiresAt)
	}
	// Registration must not log the user in: no session cookie is set.
	for _, c := range rec.Result().Cookies() {
		if c.Name == "session" {
			t.Fatal("register set a session cookie; should require explicit login")
		}
	}
	// The plaintext password is revealed once in the response body and must not
	// equal the stored hash.
	body := rec.Body.String()
	if strings.Contains(body, created.PasswordHash) {
		t.Fatal("response body leaked the password hash")
	}
	if !strings.Contains(body, created.Address) {
		t.Fatalf("response body missing created address: %q", body)
	}
}

func TestRegisterRateLimited(t *testing.T) {
	repo := newMemoryRepository()
	handler := newTestHandlerWithOptions(t, repo, Options{
		DefaultDomain:      "example.test",
		SessionTTL:         7 * 24 * time.Hour,
		BcryptCost:         bcrypt.MinCost,
		CreateInboxLimiter: ratelimit.NewFixedWindowLimiter(1, time.Hour),
	})

	for i, want := range []int{http.StatusOK, http.StatusTooManyRequests} {
		req := httptest.NewRequest(http.MethodPost, "http://service.test/register", strings.NewReader(fmt.Sprintf("local_part=adam%d&domain=example.test", i)))
		req.Header.Set("Origin", "http://service.test")
		req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
		rec := httptest.NewRecorder()
		handler.ServeHTTP(rec, req)
		if rec.Code != want {
			t.Fatalf("attempt %d status = %d, want %d", i+1, rec.Code, want)
		}
	}
}

func TestValidateLocalPart(t *testing.T) {
	t.Run("valid normalizes and lowercases", func(t *testing.T) {
		got, err := validateLocalPart("  Adam01  ")
		if err != nil {
			t.Fatalf("validateLocalPart() error = %v", err)
		}
		if got != "adam01" {
			t.Fatalf("validateLocalPart() = %q, want %q", got, "adam01")
		}
	})

	rejected := map[string]string{
		"empty":          "",
		"whitespaceOnly": "   ",
		"tooShort":       "ab",
		"tooLong":        strings.Repeat("a", localPartMaxLen+1),
		"hasAt":          "adam@x",
		"hasSpace":       "ad am",
		"hasDot":         "adam.smith",
		"hasHyphen":      "adam-smith",
		"hasSymbol":      "adam!",
	}
	for name, input := range rejected {
		t.Run(name, func(t *testing.T) {
			if _, err := validateLocalPart(input); err == nil {
				t.Fatalf("validateLocalPart(%q) = nil error, want rejection", input)
			}
		})
	}
}

func TestRegisterRejectsTakenLocalPart(t *testing.T) {
	repo := newMemoryRepository()
	repo.seedInbox("adam@example.test", "adam", "token-adam", "hash", neverExpires)
	handler := newTestHandler(t, repo)

	req := httptest.NewRequest(http.MethodPost, "http://service.test/register", strings.NewReader("local_part=Adam&domain=example.test"))
	req.Header.Set("Origin", "http://service.test")
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusConflict {
		t.Fatalf("status = %d, want %d", rec.Code, http.StatusConflict)
	}
	if len(repo.inboxes) != 1 {
		t.Fatalf("inboxes = %d, want 1 (no new inbox created)", len(repo.inboxes))
	}
	body := rec.Body.String()
	if !strings.Contains(body, "already taken") {
		t.Fatalf("response body missing conflict message: %q", body)
	}
	// The submitted name repopulates the form (lowercased by validation).
	if !strings.Contains(body, `value="adam"`) {
		t.Fatalf("response body did not repopulate local_part: %q", body)
	}
}

func TestRegisterRejectsInvalidLocalPart(t *testing.T) {
	for _, tc := range []struct {
		name      string
		localPart string
	}{
		{"empty", ""},
		{"symbols", "bad+name"},
		{"space", "bad name"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			repo := newMemoryRepository()
			handler := newTestHandler(t, repo)

			form := fmt.Sprintf("local_part=%s&domain=example.test", url.QueryEscape(tc.localPart))
			req := httptest.NewRequest(http.MethodPost, "http://service.test/register", strings.NewReader(form))
			req.Header.Set("Origin", "http://service.test")
			req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
			rec := httptest.NewRecorder()
			handler.ServeHTTP(rec, req)

			if rec.Code != http.StatusBadRequest {
				t.Fatalf("status = %d, want %d", rec.Code, http.StatusBadRequest)
			}
			if len(repo.inboxes) != 0 {
				t.Fatalf("inboxes = %d, want 0", len(repo.inboxes))
			}
		})
	}
}

func TestLoginSucceedsWithCorrectPassword(t *testing.T) {
	repo := newMemoryRepository()
	hash, err := auth.HashPassword("correct horse", bcrypt.MinCost)
	if err != nil {
		t.Fatalf("HashPassword() error = %v", err)
	}
	repo.seedInbox("user@example.test", "user", "token-user", hash, neverExpires)
	handler := newTestHandler(t, repo)

	req := httptest.NewRequest(http.MethodPost, "http://service.test/login", strings.NewReader("address=user@example.test&password=correct+horse"))
	req.Header.Set("Origin", "http://service.test")
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusSeeOther {
		t.Fatalf("status = %d, want %d", rec.Code, http.StatusSeeOther)
	}
	if got := rec.Header().Get("Location"); got != "/app" {
		t.Fatalf("Location = %q, want /app", got)
	}
	var sessionCookie *http.Cookie
	for _, c := range rec.Result().Cookies() {
		if c.Name == "session" {
			sessionCookie = c
		}
	}
	if sessionCookie == nil || sessionCookie.Value == "" {
		t.Fatal("login did not set a session cookie")
	}
	if len(repo.sessions) != 1 {
		t.Fatalf("sessions = %d, want 1", len(repo.sessions))
	}
}

func TestLoginFailsGenericallyForWrongPasswordOrUnknownAddress(t *testing.T) {
	hash, err := auth.HashPassword("correct horse", bcrypt.MinCost)
	if err != nil {
		t.Fatalf("HashPassword() error = %v", err)
	}

	cases := []struct{ name, form string }{
		{"wrong password", "address=user@example.test&password=wrong"},
		{"unknown address", "address=ghost@example.test&password=correct+horse"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			repo := newMemoryRepository()
			repo.seedInbox("user@example.test", "user", "token-user", hash, neverExpires)
			handler := newTestHandler(t, repo)

			req := httptest.NewRequest(http.MethodPost, "http://service.test/login", strings.NewReader(tc.form))
			req.Header.Set("Origin", "http://service.test")
			req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
			rec := httptest.NewRecorder()
			handler.ServeHTTP(rec, req)

			if rec.Code != http.StatusUnauthorized {
				t.Fatalf("status = %d, want %d", rec.Code, http.StatusUnauthorized)
			}
			if !strings.Contains(rec.Body.String(), "Invalid email address or password.") {
				t.Fatalf("body missing generic error: %q", rec.Body.String())
			}
			for _, c := range rec.Result().Cookies() {
				if c.Name == "session" && c.Value != "" {
					t.Fatal("failed login set a session cookie")
				}
			}
		})
	}
}

func TestSessionRefreshExtendsExpiryAndReissuesCookie(t *testing.T) {
	repo := newMemoryRepository()
	inbox, cookie := loginInbox(repo, "refresh@example.test")
	// Start the session close to expiry so the refresh is observable.
	repo.sessions[cookie.Value].ExpiresAt = time.Now().Add(time.Minute).UTC()
	handler := newTestHandler(t, repo)

	req := httptest.NewRequest(http.MethodGet, "/app", nil)
	req.AddCookie(cookie)
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d", rec.Code, http.StatusOK)
	}
	if got := repo.sessions[cookie.Value].ExpiresAt; got.Before(time.Now().Add(6 * 24 * time.Hour)) {
		t.Fatalf("session ExpiresAt = %v, want ~7 days out", got)
	}
	var reissued *http.Cookie
	for _, c := range rec.Result().Cookies() {
		if c.Name == "session" {
			reissued = c
		}
	}
	if reissued == nil || reissued.MaxAge < 6*24*60*60 {
		t.Fatalf("reissued cookie = %#v, want ~7-day MaxAge", reissued)
	}
	_ = inbox
}

func TestLogoutClearsSession(t *testing.T) {
	repo := newMemoryRepository()
	_, cookie := loginInbox(repo, "bye@example.test")
	handler := newTestHandler(t, repo)

	req := httptest.NewRequest(http.MethodPost, "http://service.test/logout", nil)
	req.AddCookie(cookie)
	req.Header.Set("Origin", "http://service.test")
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusSeeOther {
		t.Fatalf("status = %d, want %d", rec.Code, http.StatusSeeOther)
	}
	if _, ok := repo.sessions[cookie.Value]; ok {
		t.Fatal("session still present after logout")
	}
	for _, c := range rec.Result().Cookies() {
		if c.Name == "session" && c.MaxAge >= 0 {
			t.Fatalf("logout cookie MaxAge = %d, want negative", c.MaxAge)
		}
	}
}

func TestIndexRendersStoredEmails(t *testing.T) {
	repo := newMemoryRepository()
	inbox, cookie := loginInbox(repo, "inbox@example.test")
	repo.seedEmail(inbox.ID, "Welcome", false)
	handler := newTestHandler(t, repo)

	req := httptest.NewRequest(http.MethodGet, "/app", nil)
	req.AddCookie(cookie)
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)

	body := rec.Body.String()
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d", rec.Code, http.StatusOK)
	}
	if !strings.Contains(body, "Welcome") {
		t.Fatalf("body = %q, want email subject", body)
	}
	if !strings.Contains(body, inbox.Address) {
		t.Fatalf("body = %q, want inbox address", body)
	}
}

func TestViewEmailMarksRead(t *testing.T) {
	repo := newMemoryRepository()
	inbox, cookie := loginInbox(repo, "inbox@example.test")
	email := repo.seedEmail(inbox.ID, "Unread", false)
	handler := newTestHandler(t, repo)

	req := httptest.NewRequest(http.MethodGet, "/app/email/"+itoa(email.ID), nil)
	req.AddCookie(cookie)
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d", rec.Code, http.StatusOK)
	}
	if !repo.emails[email.ID].IsRead {
		t.Fatal("email was not marked as read")
	}
}

func TestAttachmentDownloadEnforcesOwnership(t *testing.T) {
	repo := newMemoryRepository()
	_, ownerCookie := loginInbox(repo, "owner@example.test")
	other, _ := loginInbox(repo, "other@example.test")
	otherEmail := repo.seedEmail(other.ID, "Other", false)
	attachment := repo.seedAttachment(otherEmail.ID, "other.txt", []byte("secret"))
	handler := newTestHandler(t, repo)

	req := httptest.NewRequest(http.MethodGet, "/app/email/"+itoa(otherEmail.ID)+"/attachment/"+itoa(attachment.ID), nil)
	req.AddCookie(ownerCookie)
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusNotFound {
		t.Fatalf("status = %d, want %d", rec.Code, http.StatusNotFound)
	}
}

func TestDeleteEmailRemovesOwnedMail(t *testing.T) {
	repo := newMemoryRepository()
	inbox, cookie := loginInbox(repo, "inbox@example.test")
	email := repo.seedEmail(inbox.ID, "Delete me", false)
	handler := newTestHandler(t, repo)

	req := httptest.NewRequest(http.MethodPost, "http://service.test/app/email/"+itoa(email.ID)+"/delete", nil)
	req.AddCookie(cookie)
	req.Header.Set("Origin", "http://service.test")
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusSeeOther {
		t.Fatalf("status = %d, want %d", rec.Code, http.StatusSeeOther)
	}
	if _, ok := repo.emails[email.ID]; ok {
		t.Fatal("email still present after delete")
	}
}

func TestAPIEmailsReturnsExpectedShape(t *testing.T) {
	repo := newMemoryRepository()
	inbox, cookie := loginInbox(repo, "api@example.test")
	email := repo.seedEmail(inbox.ID, "API Subject", true)
	handler := newTestHandler(t, repo)

	req := httptest.NewRequest(http.MethodGet, "/app/api/emails", nil)
	req.AddCookie(cookie)
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d", rec.Code, http.StatusOK)
	}

	var response apiEmailsResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &response); err != nil {
		t.Fatalf("json.Unmarshal() error = %v", err)
	}
	if response.Address != inbox.Address {
		t.Fatalf("Address = %q, want %q", response.Address, inbox.Address)
	}
	if response.UnreadCount != 1 {
		t.Fatalf("UnreadCount = %d, want 1", response.UnreadCount)
	}
	if len(response.Emails) != 1 || response.Emails[0].ID != email.ID {
		t.Fatalf("Emails = %+v, want seeded email", response.Emails)
	}
	if !response.Emails[0].HasAttachments {
		t.Fatal("HasAttachments = false, want true")
	}
	assertSecurityHeaders(t, rec.Result())
}

func TestHealthReturns503WhenRepositoryPingFails(t *testing.T) {
	repo := newMemoryRepository()
	repo.pingErr = errors.New("database unavailable")
	handler := newTestHandler(t, repo)

	req := httptest.NewRequest(http.MethodGet, "/health", nil)
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusServiceUnavailable {
		t.Fatalf("status = %d, want %d", rec.Code, http.StatusServiceUnavailable)
	}
}

func TestSecurityHeadersAppliedToPagesDownloadsAndStaticAssets(t *testing.T) {
	repo := newMemoryRepository()
	inbox, cookie := loginInbox(repo, "headers@example.test")
	email := repo.seedEmail(inbox.ID, "Header Check", true)
	attachment := repo.seedAttachment(email.ID, "hello.txt", []byte("payload"))
	handler := newTestHandler(t, repo)

	tests := []struct {
		name string
		req  *http.Request
		want int
	}{
		{
			name: "index",
			req: func() *http.Request {
				req := httptest.NewRequest(http.MethodGet, "/app", nil)
				req.AddCookie(cookie)
				return req
			}(),
			want: http.StatusOK,
		},
		{
			name: "api",
			req: func() *http.Request {
				req := httptest.NewRequest(http.MethodGet, "/app/api/emails", nil)
				req.AddCookie(cookie)
				return req
			}(),
			want: http.StatusOK,
		},
		{
			name: "attachment",
			req: func() *http.Request {
				req := httptest.NewRequest(http.MethodGet, "/app/email/"+itoa(email.ID)+"/attachment/"+itoa(attachment.ID), nil)
				req.AddCookie(cookie)
				return req
			}(),
			want: http.StatusOK,
		},
		{
			name: "health",
			req:  httptest.NewRequest(http.MethodGet, "/health", nil),
			want: http.StatusOK,
		},
		{
			name: "static",
			req:  httptest.NewRequest(http.MethodGet, "/static/style.css", nil),
			want: http.StatusOK,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			rec := httptest.NewRecorder()
			handler.ServeHTTP(rec, tt.req)

			if rec.Code != tt.want {
				t.Fatalf("status = %d, want %d", rec.Code, tt.want)
			}

			result := rec.Result()
			assertSecurityHeaders(t, result)
			if tt.name == "static" {
				if got := result.Header.Get("Cache-Control"); got != staticCacheControl {
					t.Fatalf("Cache-Control = %q, want %q", got, staticCacheControl)
				}
			}
		})
	}
}

func TestCrossOriginPostsAreRejected(t *testing.T) {
	repo := newMemoryRepository()
	inbox, cookie := loginInbox(repo, "cross@example.test")
	email := repo.seedEmail(inbox.ID, "Cross Origin", false)
	handler := newTestHandler(t, repo)

	tests := []struct {
		name string
		path string
	}{
		{name: "login", path: "http://service.test/login"},
		{name: "delete email", path: "http://service.test/app/email/" + itoa(email.ID) + "/delete"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			req := httptest.NewRequest(http.MethodPost, tt.path, nil)
			req.AddCookie(cookie)
			req.Header.Set("Origin", "http://attacker.test")

			rec := httptest.NewRecorder()
			handler.ServeHTTP(rec, req)

			if rec.Code != http.StatusForbidden {
				t.Fatalf("status = %d, want %d", rec.Code, http.StatusForbidden)
			}
		})
	}
}

// TestSecFetchSiteGovernsOriginCheck verifies that the Fetch Metadata header is
// the authoritative same-origin signal: a same-origin form navigation succeeds
// even when the browser sends an opaque "Origin: null" (the reported /admin/setup
// bug), while a cross-site Sec-Fetch-Site is rejected regardless of Origin.
func TestSecFetchSiteGovernsOriginCheck(t *testing.T) {
	tests := []struct {
		name          string
		origin        string
		secFetchSite  string
		wantForbidden bool
	}{
		{name: "same-origin with null origin", origin: "null", secFetchSite: "same-origin", wantForbidden: false},
		{name: "none is user-initiated", origin: "null", secFetchSite: "none", wantForbidden: false},
		{name: "cross-site rejected despite matching origin", origin: "http://service.test", secFetchSite: "cross-site", wantForbidden: true},
		{name: "same-site rejected", origin: "null", secFetchSite: "same-site", wantForbidden: true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			repo := newMemoryRepository()
			handler := newTestHandler(t, repo)

			req := httptest.NewRequest(http.MethodPost, "http://service.test/login", strings.NewReader("address=user@example.test&password=whatever"))
			req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
			req.Header.Set("Origin", tt.origin)
			req.Header.Set("Sec-Fetch-Site", tt.secFetchSite)

			rec := httptest.NewRecorder()
			handler.ServeHTTP(rec, req)

			forbidden := rec.Code == http.StatusForbidden
			if forbidden != tt.wantForbidden {
				t.Fatalf("status = %d, wantForbidden = %v", rec.Code, tt.wantForbidden)
			}
		})
	}
}

func TestPostRequestBodyLimitRejectsOversizedPayloads(t *testing.T) {
	repo := newMemoryRepository()
	handler := newTestHandler(t, repo)

	req := httptest.NewRequest(http.MethodPost, "http://service.test/login", bytes.NewReader(bytes.Repeat([]byte("a"), int(maxPostBodyBytes+1))))
	req.Header.Set("Origin", "http://service.test")
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusRequestEntityTooLarge {
		t.Fatalf("status = %d, want %d", rec.Code, http.StatusRequestEntityTooLarge)
	}
}

func TestViewEmailSanitizesHTML(t *testing.T) {
	repo := newMemoryRepository()
	inbox, cookie := loginInbox(repo, "html@example.test")
	email := &models.Email{
		InboxID:    inbox.ID,
		Sender:     "sender@example.net",
		Recipient:  inbox.Address,
		Subject:    "HTML",
		BodyHTML:   `<p>Hello</p><script>alert(1)</script><a href="javascript:alert(1)" onclick="evil()">bad</a><img src="x" onerror="evil()">`,
		ReceivedAt: time.Now().UTC(),
	}
	if err := repo.SaveEmail(context.Background(), email); err != nil {
		t.Fatalf("SaveEmail() error = %v", err)
	}

	handler := newTestHandler(t, repo)
	req := httptest.NewRequest(http.MethodGet, "/app/email/"+itoa(email.ID), nil)
	req.AddCookie(cookie)
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d", rec.Code, http.StatusOK)
	}

	body := rec.Body.String()
	for _, unwanted := range []string{"<script", "javascript:", "onclick=", "onerror="} {
		if strings.Contains(body, unwanted) {
			t.Fatalf("body contains %q: %q", unwanted, body)
		}
	}
	if !strings.Contains(body, "<p>Hello</p>") {
		t.Fatalf("body = %q, want sanitized HTML content", body)
	}
}

func newTestHandler(t *testing.T, repo *memoryRepository) http.Handler {
	t.Helper()

	return newTestHandlerWithOptions(t, repo, Options{
		DefaultDomain: "example.test",
		SessionTTL:    7 * 24 * time.Hour,
		BcryptCost:    bcrypt.MinCost,
	})
}

func newTestHandlerWithOptions(t *testing.T, repo *memoryRepository, options Options) http.Handler {
	t.Helper()

	handler, err := RegisterRoutes(repo, slog.New(slog.NewTextHandler(io.Discard, nil)), templateDir(t), options)
	if err != nil {
		t.Fatalf("RegisterRoutes() error = %v", err)
	}

	return handler
}

func templateDir(t *testing.T) string {
	t.Helper()

	_, file, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("runtime.Caller() failed")
	}

	return filepath.Join(filepath.Dir(file), "..", "..", "templates")
}

type memoryRepository struct {
	nextInboxID      int64
	nextEmailID      int64
	nextAttachmentID int64
	nextDomainID     int64
	inboxes          map[int64]*models.Inbox
	inboxTokens      map[string]int64
	inboxAddresses   map[string]int64
	emails           map[int64]*models.Email
	attachments      map[int64]*models.Attachment
	domains          []*models.Domain
	sessions         map[string]*models.Session
	settings         map[string]string
	pingErr          error
}

func newMemoryRepository() *memoryRepository {
	m := &memoryRepository{
		nextInboxID:      1,
		nextEmailID:      1,
		nextAttachmentID: 1,
		nextDomainID:     1,
		inboxes:          map[int64]*models.Inbox{},
		inboxTokens:      map[string]int64{},
		inboxAddresses:   map[string]int64{},
		emails:           map[int64]*models.Email{},
		attachments:      map[int64]*models.Attachment{},
		sessions:         map[string]*models.Session{},
		settings:         map[string]string{},
	}
	// Seed the default test domain so inbox addresses resolve to example.test.
	_, _ = m.CreateDomain(context.Background(), "example.test")
	return m
}

func (m *memoryRepository) CreateInbox(_ context.Context, inbox *models.Inbox) error {
	if _, ok := m.inboxTokens[inbox.Token]; ok {
		return repository.ErrConflict
	}
	key := strings.ToLower(inbox.Address)
	if _, ok := m.inboxAddresses[key]; ok {
		return repository.ErrConflict
	}

	copy := *inbox
	copy.ID = m.nextInboxID
	copy.CreatedAt = time.Now().UTC()
	copy.LastAccessedAt = copy.CreatedAt
	copy.ExpiresAt = copy.ExpiresAt.UTC()
	m.nextInboxID++
	m.inboxes[copy.ID] = &copy
	m.inboxTokens[copy.Token] = copy.ID
	m.inboxAddresses[key] = copy.ID
	*inbox = copy
	return nil
}

func (m *memoryRepository) GetInboxByToken(_ context.Context, token string) (*models.Inbox, error) {
	id, ok := m.inboxTokens[token]
	if !ok {
		return nil, repository.ErrNotFound
	}
	inbox := m.inboxes[id]
	if inbox.ExpiresAt.Before(time.Now()) {
		return nil, repository.ErrNotFound
	}
	copy := *inbox
	return &copy, nil
}

func (m *memoryRepository) GetInboxByAddress(_ context.Context, address string) (*models.Inbox, error) {
	id, ok := m.inboxAddresses[strings.ToLower(address)]
	if !ok {
		return nil, repository.ErrNotFound
	}
	copy := *m.inboxes[id]
	return &copy, nil
}

func (m *memoryRepository) UpdateLastAccessed(_ context.Context, inboxID int64) error {
	inbox, ok := m.inboxes[inboxID]
	if !ok {
		return repository.ErrNotFound
	}
	inbox.LastAccessedAt = time.Now().UTC()
	return nil
}

func (m *memoryRepository) DeleteExpiredInboxes(context.Context) (int64, error) { return 0, nil }

func (m *memoryRepository) SaveEmail(_ context.Context, email *models.Email) error {
	copy := *email
	copy.ID = m.nextEmailID
	if copy.ReceivedAt.IsZero() {
		copy.ReceivedAt = time.Now().UTC()
	}
	m.nextEmailID++
	m.emails[copy.ID] = &copy
	*email = copy
	return nil
}

func (m *memoryRepository) SaveInboundMessage(ctx context.Context, email *models.Email, attachments []*models.Attachment) error {
	if err := m.SaveEmail(ctx, email); err != nil {
		return err
	}
	for _, attachment := range attachments {
		attachment.EmailID = email.ID
		if err := m.SaveAttachment(ctx, attachment); err != nil {
			return err
		}
	}
	return nil
}

func (m *memoryRepository) GetEmailsByInboxID(_ context.Context, inboxID int64, limit, offset int) ([]*models.Email, error) {
	var emails []*models.Email
	for _, email := range m.emails {
		if email.InboxID == inboxID {
			copy := *email
			emails = append(emails, &copy)
		}
	}
	slices.SortFunc(emails, func(a, b *models.Email) int {
		if a.ReceivedAt.Equal(b.ReceivedAt) {
			switch {
			case a.ID > b.ID:
				return -1
			case a.ID < b.ID:
				return 1
			default:
				return 0
			}
		}
		if a.ReceivedAt.After(b.ReceivedAt) {
			return -1
		}
		return 1
	})
	if offset >= len(emails) {
		return []*models.Email{}, nil
	}
	end := len(emails)
	if limit > 0 && offset+limit < end {
		end = offset + limit
	}
	return emails[offset:end], nil
}

func (m *memoryRepository) GetEmailByID(_ context.Context, inboxID, emailID int64) (*models.Email, error) {
	email, ok := m.emails[emailID]
	if !ok || email.InboxID != inboxID {
		return nil, repository.ErrNotFound
	}
	copy := *email
	return &copy, nil
}

func (m *memoryRepository) MarkEmailAsRead(_ context.Context, emailID int64) error {
	email, ok := m.emails[emailID]
	if !ok {
		return repository.ErrNotFound
	}
	email.IsRead = true
	return nil
}

func (m *memoryRepository) GetUnreadCount(_ context.Context, inboxID int64) (int, error) {
	count := 0
	for _, email := range m.emails {
		if email.InboxID == inboxID && !email.IsRead {
			count++
		}
	}
	return count, nil
}

func (m *memoryRepository) DeleteEmail(_ context.Context, inboxID, emailID int64) error {
	email, ok := m.emails[emailID]
	if !ok || email.InboxID != inboxID {
		return repository.ErrNotFound
	}
	delete(m.emails, emailID)
	for id, attachment := range m.attachments {
		if attachment.EmailID == emailID {
			delete(m.attachments, id)
		}
	}
	return nil
}

func (m *memoryRepository) SaveAttachment(_ context.Context, attachment *models.Attachment) error {
	if _, ok := m.emails[attachment.EmailID]; !ok {
		return errors.New("missing email")
	}
	copy := *attachment
	copy.ID = m.nextAttachmentID
	m.nextAttachmentID++
	m.attachments[copy.ID] = &copy
	*attachment = copy
	return nil
}

func (m *memoryRepository) GetAttachmentsByEmailID(_ context.Context, emailID int64) ([]*models.Attachment, error) {
	var attachments []*models.Attachment
	for _, attachment := range m.attachments {
		if attachment.EmailID == emailID {
			copy := *attachment
			attachments = append(attachments, &copy)
		}
	}
	slices.SortFunc(attachments, func(a, b *models.Attachment) int {
		switch {
		case a.ID < b.ID:
			return -1
		case a.ID > b.ID:
			return 1
		default:
			return 0
		}
	})
	return attachments, nil
}

func (m *memoryRepository) GetAttachment(_ context.Context, attachmentID int64) (*models.Attachment, error) {
	attachment, ok := m.attachments[attachmentID]
	if !ok {
		return nil, repository.ErrNotFound
	}
	copy := *attachment
	return &copy, nil
}

func (m *memoryRepository) CreateDomain(_ context.Context, name string) (*models.Domain, error) {
	name = strings.ToLower(strings.TrimSpace(name))
	for _, d := range m.domains {
		if d.Name == name {
			return nil, repository.ErrConflict
		}
	}
	domain := &models.Domain{
		ID:        m.nextDomainID,
		Name:      name,
		Enabled:   true,
		CreatedAt: time.Now().UTC(),
	}
	m.nextDomainID++
	m.domains = append(m.domains, domain)
	return domain, nil
}

func (m *memoryRepository) ListDomains(_ context.Context, enabledOnly bool) ([]*models.Domain, error) {
	var out []*models.Domain
	for _, d := range m.domains {
		if enabledOnly && !d.Enabled {
			continue
		}
		out = append(out, d)
	}
	return out, nil
}

func (m *memoryRepository) GetDomain(_ context.Context, name string) (*models.Domain, error) {
	name = strings.ToLower(strings.TrimSpace(name))
	for _, d := range m.domains {
		if d.Name == name {
			return d, nil
		}
	}
	return nil, repository.ErrNotFound
}

func (m *memoryRepository) SetDomainEnabled(_ context.Context, name string, enabled bool) error {
	name = strings.ToLower(strings.TrimSpace(name))
	for _, d := range m.domains {
		if d.Name == name {
			d.Enabled = enabled
			return nil
		}
	}
	return repository.ErrNotFound
}

func (m *memoryRepository) IsDomainEnabled(_ context.Context, name string) (bool, error) {
	name = strings.ToLower(strings.TrimSpace(name))
	for _, d := range m.domains {
		if d.Name == name {
			return d.Enabled, nil
		}
	}
	return false, nil
}

func (m *memoryRepository) CreateSession(_ context.Context, session *models.Session) error {
	if _, ok := m.sessions[session.Token]; ok {
		return repository.ErrConflict
	}
	copy := *session
	copy.CreatedAt = time.Now().UTC()
	copy.ExpiresAt = copy.ExpiresAt.UTC()
	m.sessions[copy.Token] = &copy
	*session = copy
	return nil
}

func (m *memoryRepository) GetSession(_ context.Context, token string) (*models.Session, *models.Inbox, error) {
	session, ok := m.sessions[token]
	if !ok || !session.ExpiresAt.After(time.Now()) {
		return nil, nil, repository.ErrNotFound
	}
	inbox, ok := m.inboxes[session.InboxID]
	if !ok {
		return nil, nil, repository.ErrNotFound
	}
	sessionCopy := *session
	inboxCopy := *inbox
	return &sessionCopy, &inboxCopy, nil
}

func (m *memoryRepository) DeleteSession(_ context.Context, token string) error {
	if _, ok := m.sessions[token]; !ok {
		return repository.ErrNotFound
	}
	delete(m.sessions, token)
	return nil
}

func (m *memoryRepository) TouchSession(_ context.Context, token string, expiresAt time.Time) error {
	session, ok := m.sessions[token]
	if !ok {
		return repository.ErrNotFound
	}
	session.ExpiresAt = expiresAt.UTC()
	return nil
}

func (m *memoryRepository) DeleteExpiredSessions(context.Context) (int64, error) {
	var deleted int64
	now := time.Now()
	for token, session := range m.sessions {
		if !session.ExpiresAt.After(now) {
			delete(m.sessions, token)
			deleted++
		}
	}
	return deleted, nil
}

func (m *memoryRepository) GetSetting(_ context.Context, key string) (string, error) {
	value, ok := m.settings[key]
	if !ok {
		return "", repository.ErrNotFound
	}
	return value, nil
}

func (m *memoryRepository) SetSetting(_ context.Context, key, value string) error {
	m.settings[key] = value
	return nil
}

func (m *memoryRepository) Ping(context.Context) error { return m.pingErr }

func (m *memoryRepository) Close() error { return nil }

func (m *memoryRepository) seedInbox(address, localPart, token, passwordHash string, expiresAt time.Time) *models.Inbox {
	inbox := &models.Inbox{
		Address:      address,
		LocalPart:    localPart,
		Token:        token,
		PasswordHash: passwordHash,
		ExpiresAt:    expiresAt,
	}
	_ = m.CreateInbox(context.Background(), inbox)
	return inbox
}

func (m *memoryRepository) seedSession(token string, inboxID int64, expiresAt time.Time) *models.Session {
	session := &models.Session{
		Token:     token,
		InboxID:   inboxID,
		ExpiresAt: expiresAt,
	}
	_ = m.CreateSession(context.Background(), session)
	return session
}

func (m *memoryRepository) seedEmail(inboxID int64, subject string, hasAttachments bool) *models.Email {
	email := &models.Email{
		InboxID:        inboxID,
		Sender:         "sender@example.net",
		Recipient:      "recipient@example.test",
		Subject:        subject,
		BodyText:       "Hello",
		HasAttachments: hasAttachments,
		ReceivedAt:     time.Now().UTC(),
	}
	_ = m.SaveEmail(context.Background(), email)
	return email
}

func (m *memoryRepository) seedAttachment(emailID int64, filename string, content []byte) *models.Attachment {
	attachment := &models.Attachment{
		EmailID:     emailID,
		Filename:    filename,
		ContentType: "text/plain",
		SizeBytes:   int64(len(content)),
		Content:     content,
	}
	_ = m.SaveAttachment(context.Background(), attachment)
	return attachment
}

func assertSecurityHeaders(t *testing.T, response *http.Response) {
	t.Helper()

	if got := response.Header.Get("X-Content-Type-Options"); got != "nosniff" {
		t.Fatalf("X-Content-Type-Options = %q, want %q", got, "nosniff")
	}
	if got := response.Header.Get("X-Frame-Options"); got != "DENY" {
		t.Fatalf("X-Frame-Options = %q, want %q", got, "DENY")
	}
	if got := response.Header.Get("Referrer-Policy"); got != "no-referrer" {
		t.Fatalf("Referrer-Policy = %q, want %q", got, "no-referrer")
	}
	if got := response.Header.Get("Content-Security-Policy"); got != contentSecurityPolicy {
		t.Fatalf("Content-Security-Policy = %q, want %q", got, contentSecurityPolicy)
	}
}

func itoa(value int64) string {
	return strconv.FormatInt(value, 10)
}
