package web

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"runtime"
	"slices"
	"strconv"
	"strings"
	"testing"
	"time"

	"anonymous-email-service/internal/models"
	"anonymous-email-service/internal/ratelimit"
	"anonymous-email-service/internal/repository"
)

func TestHealthBypassesSessionMiddleware(t *testing.T) {
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

func TestFirstVisitCreatesInboxAndCookie(t *testing.T) {
	repo := newMemoryRepository()
	handler := newTestHandler(t, repo)

	req := httptest.NewRequest(http.MethodGet, "/", nil)
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d", rec.Code, http.StatusOK)
	}
	if len(repo.inboxes) != 1 {
		t.Fatalf("inboxes = %d, want 1", len(repo.inboxes))
	}
	cookies := rec.Result().Cookies()
	if len(cookies) == 0 || cookies[0].Name != "inbox_token" {
		t.Fatalf("cookies = %#v, want inbox_token", cookies)
	}
}

func TestValidCookieReusesInbox(t *testing.T) {
	repo := newMemoryRepository()
	inbox := repo.seedInbox("existing@example.test", "existing", "token-existing", time.Now().Add(24*time.Hour))
	handler := newTestHandler(t, repo)

	req := httptest.NewRequest(http.MethodGet, "/", nil)
	req.AddCookie(&http.Cookie{Name: "inbox_token", Value: inbox.Token})
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d", rec.Code, http.StatusOK)
	}
	if len(repo.inboxes) != 1 {
		t.Fatalf("inboxes = %d, want 1", len(repo.inboxes))
	}
	if got := rec.Header().Get("Set-Cookie"); got != "" {
		t.Fatalf("Set-Cookie = %q, want empty", got)
	}
}

func TestInvalidCookieCreatesReplacementInbox(t *testing.T) {
	repo := newMemoryRepository()
	handler := newTestHandler(t, repo)

	req := httptest.NewRequest(http.MethodGet, "/", nil)
	req.AddCookie(&http.Cookie{Name: "inbox_token", Value: "missing-token"})
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d", rec.Code, http.StatusOK)
	}
	if len(repo.inboxes) != 1 {
		t.Fatalf("inboxes = %d, want 1", len(repo.inboxes))
	}
	if got := rec.Header().Get("Set-Cookie"); got == "" {
		t.Fatal("expected replacement Set-Cookie header")
	}
}

func TestIndexRendersStoredEmails(t *testing.T) {
	repo := newMemoryRepository()
	inbox := repo.seedInbox("inbox@example.test", "inbox", "token-inbox", time.Now().Add(24*time.Hour))
	repo.seedEmail(inbox.ID, "Welcome", false)
	handler := newTestHandler(t, repo)

	req := httptest.NewRequest(http.MethodGet, "/", nil)
	req.AddCookie(&http.Cookie{Name: "inbox_token", Value: inbox.Token})
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
	inbox := repo.seedInbox("inbox@example.test", "inbox", "token-inbox", time.Now().Add(24*time.Hour))
	email := repo.seedEmail(inbox.ID, "Unread", false)
	handler := newTestHandler(t, repo)

	req := httptest.NewRequest(http.MethodGet, "/email/"+itoa(email.ID), nil)
	req.AddCookie(&http.Cookie{Name: "inbox_token", Value: inbox.Token})
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
	owner := repo.seedInbox("owner@example.test", "owner", "token-owner", time.Now().Add(24*time.Hour))
	other := repo.seedInbox("other@example.test", "other", "token-other", time.Now().Add(24*time.Hour))
	otherEmail := repo.seedEmail(other.ID, "Other", false)
	attachment := repo.seedAttachment(otherEmail.ID, "other.txt", []byte("secret"))
	handler := newTestHandler(t, repo)

	req := httptest.NewRequest(http.MethodGet, "/email/"+itoa(otherEmail.ID)+"/attachment/"+itoa(attachment.ID), nil)
	req.AddCookie(&http.Cookie{Name: "inbox_token", Value: owner.Token})
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusNotFound {
		t.Fatalf("status = %d, want %d", rec.Code, http.StatusNotFound)
	}
}

func TestDeleteEmailRemovesOwnedMail(t *testing.T) {
	repo := newMemoryRepository()
	inbox := repo.seedInbox("inbox@example.test", "inbox", "token-inbox", time.Now().Add(24*time.Hour))
	email := repo.seedEmail(inbox.ID, "Delete me", false)
	handler := newTestHandler(t, repo)

	req := httptest.NewRequest(http.MethodPost, "http://service.test/email/"+itoa(email.ID)+"/delete", nil)
	req.AddCookie(&http.Cookie{Name: "inbox_token", Value: inbox.Token})
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

func TestNewInboxRotatesCookie(t *testing.T) {
	repo := newMemoryRepository()
	oldInbox := repo.seedInbox("old@example.test", "old", "token-old", time.Now().Add(24*time.Hour))
	handler := newTestHandler(t, repo)

	req := httptest.NewRequest(http.MethodPost, "http://service.test/inbox/new", nil)
	req.AddCookie(&http.Cookie{Name: "inbox_token", Value: oldInbox.Token})
	req.Header.Set("Origin", "http://service.test")
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusSeeOther {
		t.Fatalf("status = %d, want %d", rec.Code, http.StatusSeeOther)
	}
	if len(repo.inboxes) != 2 {
		t.Fatalf("inboxes = %d, want 2", len(repo.inboxes))
	}
	cookies := rec.Result().Cookies()
	if len(cookies) == 0 || cookies[0].Value == oldInbox.Token {
		t.Fatalf("cookies = %#v, want rotated inbox token", cookies)
	}
}

func TestAPIEmailsReturnsExpectedShape(t *testing.T) {
	repo := newMemoryRepository()
	inbox := repo.seedInbox("api@example.test", "api", "token-api", time.Now().Add(24*time.Hour))
	email := repo.seedEmail(inbox.ID, "API Subject", true)
	handler := newTestHandler(t, repo)

	req := httptest.NewRequest(http.MethodGet, "/api/emails", nil)
	req.AddCookie(&http.Cookie{Name: "inbox_token", Value: inbox.Token})
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
	inbox := repo.seedInbox("headers@example.test", "headers", "token-headers", time.Now().Add(24*time.Hour))
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
				req := httptest.NewRequest(http.MethodGet, "/", nil)
				req.AddCookie(&http.Cookie{Name: "inbox_token", Value: inbox.Token})
				return req
			}(),
			want: http.StatusOK,
		},
		{
			name: "api",
			req: func() *http.Request {
				req := httptest.NewRequest(http.MethodGet, "/api/emails", nil)
				req.AddCookie(&http.Cookie{Name: "inbox_token", Value: inbox.Token})
				return req
			}(),
			want: http.StatusOK,
		},
		{
			name: "attachment",
			req: func() *http.Request {
				req := httptest.NewRequest(http.MethodGet, "/email/"+itoa(email.ID)+"/attachment/"+itoa(attachment.ID), nil)
				req.AddCookie(&http.Cookie{Name: "inbox_token", Value: inbox.Token})
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
	inbox := repo.seedInbox("cross@example.test", "cross", "token-cross", time.Now().Add(24*time.Hour))
	email := repo.seedEmail(inbox.ID, "Cross Origin", false)
	handler := newTestHandler(t, repo)

	tests := []struct {
		name string
		path string
	}{
		{name: "new inbox", path: "http://service.test/inbox/new"},
		{name: "delete email", path: "http://service.test/email/" + itoa(email.ID) + "/delete"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			req := httptest.NewRequest(http.MethodPost, tt.path, nil)
			req.AddCookie(&http.Cookie{Name: "inbox_token", Value: inbox.Token})
			req.Header.Set("Origin", "http://attacker.test")

			rec := httptest.NewRecorder()
			handler.ServeHTTP(rec, req)

			if rec.Code != http.StatusForbidden {
				t.Fatalf("status = %d, want %d", rec.Code, http.StatusForbidden)
			}
		})
	}
}

func TestPostRequestBodyLimitRejectsOversizedPayloads(t *testing.T) {
	repo := newMemoryRepository()
	handler := newTestHandler(t, repo)

	req := httptest.NewRequest(http.MethodPost, "http://service.test/inbox/new", bytes.NewReader(bytes.Repeat([]byte("a"), int(maxPostBodyBytes+1))))
	req.Header.Set("Origin", "http://service.test")
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusRequestEntityTooLarge {
		t.Fatalf("status = %d, want %d", rec.Code, http.StatusRequestEntityTooLarge)
	}
}

func TestExistingCookieBypassesCreateLimitButNewInboxIsRateLimited(t *testing.T) {
	repo := newMemoryRepository()
	handler := newTestHandlerWithOptions(t, repo, Options{
		DefaultDomain:      "example.test",
		InboxTTL:           24 * time.Hour,
		CreateInboxLimiter: ratelimit.NewFixedWindowLimiter(1, time.Hour),
	})

	firstReq := httptest.NewRequest(http.MethodGet, "/", nil)
	firstRec := httptest.NewRecorder()
	handler.ServeHTTP(firstRec, firstReq)

	if firstRec.Code != http.StatusOK {
		t.Fatalf("first status = %d, want %d", firstRec.Code, http.StatusOK)
	}
	cookies := firstRec.Result().Cookies()
	if len(cookies) == 0 {
		t.Fatal("expected inbox cookie")
	}

	reuseReq := httptest.NewRequest(http.MethodGet, "/", nil)
	reuseReq.AddCookie(cookies[0])
	reuseRec := httptest.NewRecorder()
	handler.ServeHTTP(reuseRec, reuseReq)

	if reuseRec.Code != http.StatusOK {
		t.Fatalf("reuse status = %d, want %d", reuseRec.Code, http.StatusOK)
	}

	newInboxReq := httptest.NewRequest(http.MethodPost, "http://service.test/inbox/new", nil)
	newInboxReq.AddCookie(cookies[0])
	newInboxReq.Header.Set("Origin", "http://service.test")
	newInboxRec := httptest.NewRecorder()
	handler.ServeHTTP(newInboxRec, newInboxReq)

	if newInboxRec.Code != http.StatusTooManyRequests {
		t.Fatalf("new inbox status = %d, want %d", newInboxRec.Code, http.StatusTooManyRequests)
	}
}

func TestViewEmailSanitizesHTML(t *testing.T) {
	repo := newMemoryRepository()
	inbox := repo.seedInbox("html@example.test", "html", "token-html", time.Now().Add(24*time.Hour))
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
	req := httptest.NewRequest(http.MethodGet, "/email/"+itoa(email.ID), nil)
	req.AddCookie(&http.Cookie{Name: "inbox_token", Value: inbox.Token})
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
		InboxTTL:      24 * time.Hour,
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
	inbox := m.inboxes[id]
	if inbox.ExpiresAt.Before(time.Now()) {
		return nil, repository.ErrNotFound
	}
	copy := *inbox
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

func (m *memoryRepository) Ping(context.Context) error { return m.pingErr }

func (m *memoryRepository) Close() error { return nil }

func (m *memoryRepository) seedInbox(address, localPart, token string, expiresAt time.Time) *models.Inbox {
	inbox := &models.Inbox{
		Address:   address,
		LocalPart: localPart,
		Token:     token,
		ExpiresAt: expiresAt,
	}
	_ = m.CreateInbox(context.Background(), inbox)
	return inbox
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
