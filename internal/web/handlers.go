package web

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"html/template"
	"mime"
	"net/http"
	"strconv"
	"strings"
	"time"

	"anonymous-email-service/internal/clientip"
	"anonymous-email-service/internal/models"
	"anonymous-email-service/internal/repository"
)

type indexData struct {
	Title         string
	Inbox         *models.Inbox
	UnreadCount   int
	Emails        []*models.Email
	Domains       []*models.Domain
	CurrentDomain string
	NeverExpires  bool
}

type emailData struct {
	Title            string
	Inbox            *models.Inbox
	Email            *models.Email
	Attachments      []*models.Attachment
	RenderedBodyHTML template.HTML
	RenderedBodyText string
}

type apiEmail struct {
	ID             int64     `json:"id"`
	Sender         string    `json:"sender"`
	SenderName     string    `json:"sender_name"`
	Subject        string    `json:"subject"`
	HasAttachments bool      `json:"has_attachments"`
	IsRead         bool      `json:"is_read"`
	ReceivedAt     time.Time `json:"received_at"`
}

type apiEmailsResponse struct {
	Address     string     `json:"address"`
	ExpiresAt   time.Time  `json:"expires_at"`
	UnreadCount int        `json:"unread_count"`
	Emails      []apiEmail `json:"emails"`
}

func (a *app) HandleIndex(w http.ResponseWriter, r *http.Request) {
	inbox := GetInboxFromContext(r.Context())
	if inbox == nil {
		http.Error(w, "missing inbox session", http.StatusInternalServerError)
		return
	}

	emails, err := a.repo.GetEmailsByInboxID(r.Context(), inbox.ID, 50, 0)
	if err != nil {
		a.logError("load inbox emails failed", err)
		http.Error(w, "failed to load emails", http.StatusInternalServerError)
		return
	}

	unreadCount, err := a.repo.GetUnreadCount(r.Context(), inbox.ID)
	if err != nil {
		a.logError("load unread count failed", err)
		http.Error(w, "failed to load inbox", http.StatusInternalServerError)
		return
	}

	domains, err := a.repo.ListDomains(r.Context(), true)
	if err != nil {
		a.logError("load domains failed", err)
		http.Error(w, "failed to load inbox", http.StatusInternalServerError)
		return
	}

	if err := a.templates.index.ExecuteTemplate(w, "base", indexData{
		Title:         "Anonymous Inbox",
		Inbox:         inbox,
		UnreadCount:   unreadCount,
		Emails:        emails,
		Domains:       domains,
		CurrentDomain: domainOf(inbox.Address),
		NeverExpires:  inbox.ExpiresAt.Equal(neverExpires),
	}); err != nil {
		a.logError("render index failed", err)
		http.Error(w, "template rendering failed", http.StatusInternalServerError)
	}
}

func (a *app) HandleViewEmail(w http.ResponseWriter, r *http.Request) {
	inbox := GetInboxFromContext(r.Context())
	if inbox == nil {
		http.Error(w, "missing inbox session", http.StatusInternalServerError)
		return
	}

	emailID, ok := pathID(w, r, "id")
	if !ok {
		return
	}

	email, err := a.repo.GetEmailByID(r.Context(), inbox.ID, emailID)
	if err != nil {
		if errors.Is(err, repository.ErrNotFound) {
			http.NotFound(w, r)
			return
		}
		a.logError("load email failed", err)
		http.Error(w, "failed to load email", http.StatusInternalServerError)
		return
	}

	if !email.IsRead {
		if err := a.repo.MarkEmailAsRead(r.Context(), email.ID); err != nil && !errors.Is(err, repository.ErrNotFound) {
			a.logError("mark email as read failed", err)
			http.Error(w, "failed to update email", http.StatusInternalServerError)
			return
		}
		email.IsRead = true
	}

	attachments, err := a.repo.GetAttachmentsByEmailID(r.Context(), email.ID)
	if err != nil {
		a.logError("load attachments failed", err)
		http.Error(w, "failed to load email", http.StatusInternalServerError)
		return
	}

	bodyHTML, bodyText := a.renderEmailBody(email)
	if err := a.templates.email.ExecuteTemplate(w, "base", emailData{
		Title:            email.Subject,
		Inbox:            inbox,
		Email:            email,
		Attachments:      attachments,
		RenderedBodyHTML: bodyHTML,
		RenderedBodyText: bodyText,
	}); err != nil {
		a.logError("render email failed", err)
		http.Error(w, "template rendering failed", http.StatusInternalServerError)
	}
}

func (a *app) HandleAttachment(w http.ResponseWriter, r *http.Request) {
	inbox := GetInboxFromContext(r.Context())
	if inbox == nil {
		http.Error(w, "missing inbox session", http.StatusInternalServerError)
		return
	}

	emailID, ok := pathID(w, r, "id")
	if !ok {
		return
	}
	attachmentID, ok := pathID(w, r, "aid")
	if !ok {
		return
	}

	email, err := a.repo.GetEmailByID(r.Context(), inbox.ID, emailID)
	if err != nil {
		if errors.Is(err, repository.ErrNotFound) {
			http.NotFound(w, r)
			return
		}
		a.logError("load attachment email failed", err)
		http.Error(w, "failed to load attachment", http.StatusInternalServerError)
		return
	}

	attachment, err := a.repo.GetAttachment(r.Context(), attachmentID)
	if err != nil {
		if errors.Is(err, repository.ErrNotFound) {
			http.NotFound(w, r)
			return
		}
		a.logError("load attachment failed", err)
		http.Error(w, "failed to load attachment", http.StatusInternalServerError)
		return
	}

	if attachment.EmailID != email.ID {
		http.NotFound(w, r)
		return
	}

	contentDisposition := mime.FormatMediaType("attachment", map[string]string{"filename": attachment.Filename})
	w.Header().Set("Content-Type", attachment.ContentType)
	w.Header().Set("Content-Length", strconv.FormatInt(attachment.SizeBytes, 10))
	w.Header().Set("Content-Disposition", contentDisposition)
	_, _ = w.Write(attachment.Content)
}

func (a *app) HandleDeleteEmail(w http.ResponseWriter, r *http.Request) {
	inbox := GetInboxFromContext(r.Context())
	if inbox == nil {
		http.Error(w, "missing inbox session", http.StatusInternalServerError)
		return
	}

	emailID, ok := pathID(w, r, "id")
	if !ok {
		return
	}

	err := a.repo.DeleteEmail(r.Context(), inbox.ID, emailID)
	if err != nil {
		if errors.Is(err, repository.ErrNotFound) {
			http.NotFound(w, r)
			return
		}
		a.logError("delete email failed", err)
		http.Error(w, "failed to delete email", http.StatusInternalServerError)
		return
	}

	http.Redirect(w, r, "/", http.StatusSeeOther)
}

func (a *app) HandleNewInbox(w http.ResponseWriter, r *http.Request) {
	if err := r.ParseForm(); err != nil {
		http.Error(w, "invalid form", http.StatusBadRequest)
		return
	}

	domain := strings.ToLower(strings.TrimSpace(r.PostFormValue("domain")))
	if domain != "" {
		enabled, err := a.repo.IsDomainEnabled(r.Context(), domain)
		if err != nil {
			a.logError("check domain enabled failed", err)
			http.Error(w, "failed to create inbox", http.StatusInternalServerError)
			return
		}
		if !enabled {
			http.Error(w, "selected domain is not available", http.StatusBadRequest)
			return
		}
	}

	inbox, err := a.createInbox(r.Context(), clientip.FromHTTPRequest(r), domain)
	if err != nil {
		var limitErr *rateLimitError
		if errors.As(err, &limitErr) {
			writeRateLimitResponse(w, limitErr.RetryAfter)
			return
		}
		if errors.Is(err, errNoDomainAvailable) {
			http.Error(w, "no domains are configured", http.StatusServiceUnavailable)
			return
		}
		a.logError("create new inbox failed", err)
		http.Error(w, "failed to create inbox", http.StatusInternalServerError)
		return
	}

	a.setInboxCookie(w, inbox.Token)
	http.Redirect(w, r, "/", http.StatusSeeOther)
}

// domainOf returns the domain portion of an email address, or "" if absent.
func domainOf(address string) string {
	if idx := strings.LastIndex(address, "@"); idx >= 0 {
		return address[idx+1:]
	}
	return ""
}

func (a *app) HandleAPIEmails(w http.ResponseWriter, r *http.Request) {
	inbox := GetInboxFromContext(r.Context())
	if inbox == nil {
		http.Error(w, "missing inbox session", http.StatusInternalServerError)
		return
	}

	emails, err := a.repo.GetEmailsByInboxID(r.Context(), inbox.ID, 50, 0)
	if err != nil {
		a.logError("load api emails failed", err)
		http.Error(w, "failed to load emails", http.StatusInternalServerError)
		return
	}

	unreadCount, err := a.repo.GetUnreadCount(r.Context(), inbox.ID)
	if err != nil {
		a.logError("load api unread count failed", err)
		http.Error(w, "failed to load emails", http.StatusInternalServerError)
		return
	}

	response := apiEmailsResponse{
		Address:     inbox.Address,
		ExpiresAt:   inbox.ExpiresAt.UTC(),
		UnreadCount: unreadCount,
		Emails:      make([]apiEmail, 0, len(emails)),
	}
	for _, email := range emails {
		response.Emails = append(response.Emails, apiEmail{
			ID:             email.ID,
			Sender:         email.Sender,
			SenderName:     email.SenderName,
			Subject:        email.Subject,
			HasAttachments: email.HasAttachments,
			IsRead:         email.IsRead,
			ReceivedAt:     email.ReceivedAt.UTC(),
		})
	}

	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	if err := json.NewEncoder(w).Encode(response); err != nil {
		a.logError("encode api emails failed", err)
	}
}

func (a *app) HandleHealth(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	ctx, cancel := context.WithTimeout(r.Context(), 2*time.Second)
	defer cancel()

	if err := a.repo.Ping(ctx); err != nil {
		w.WriteHeader(http.StatusServiceUnavailable)
		_, _ = w.Write([]byte(`{"status":"degraded"}`))
		return
	}

	w.WriteHeader(http.StatusOK)
	_, _ = w.Write([]byte(`{"status":"ok"}`))
}

func (a *app) renderEmailBody(email *models.Email) (template.HTML, string) {
	if email.BodyHTML != "" {
		sanitized := strings.TrimSpace(a.emailHTMLPolicy.Sanitize(email.BodyHTML))
		if sanitized != "" {
			return template.HTML(sanitized), ""
		}
	}
	if email.BodyText != "" {
		return "", email.BodyText
	}
	return "", "(empty message)"
}

func pathID(w http.ResponseWriter, r *http.Request, name string) (int64, bool) {
	value := r.PathValue(name)
	id, err := strconv.ParseInt(value, 10, 64)
	if err != nil || id <= 0 {
		http.Error(w, fmt.Sprintf("invalid %s", name), http.StatusBadRequest)
		return 0, false
	}
	return id, true
}

func (a *app) setInboxCookie(w http.ResponseWriter, token string) {
	http.SetCookie(w, &http.Cookie{
		Name:     a.cookieName,
		Value:    token,
		Path:     "/",
		HttpOnly: true,
		SameSite: http.SameSiteLaxMode,
		Secure:   a.options.CookieSecure,
		MaxAge:   a.options.cookieMaxAge(),
	})
}

func (a *app) logError(message string, err error) {
	if a.logger != nil {
		a.logger.Error(message, "error", err)
	}
}
