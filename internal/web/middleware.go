package web

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"strings"
	"time"

	"anonymous-email-service/internal/models"
	"anonymous-email-service/internal/repository"
	"github.com/google/uuid"
)

type contextKey string

const (
	inboxContextKey contextKey = "inbox"
	inboxCookieName contextKey = "inbox_token"
)

func SessionMiddleware(app *app) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			inbox, created, err := app.resolveInbox(r)
			if err != nil {
				app.logError("resolve inbox failed", err)
				http.Error(w, "failed to initialize inbox", http.StatusInternalServerError)
				return
			}

			if created {
				app.setInboxCookie(w, inbox.Token)
			}

			ctx := withInbox(r.Context(), inbox)
			next.ServeHTTP(w, r.WithContext(ctx))
		})
	}
}

func GetInboxFromContext(ctx context.Context) *models.Inbox {
	inbox, _ := ctx.Value(inboxContextKey).(*models.Inbox)
	return inbox
}

func withInbox(ctx context.Context, inbox *models.Inbox) context.Context {
	return context.WithValue(ctx, inboxContextKey, inbox)
}

func (a *app) resolveInbox(r *http.Request) (*models.Inbox, bool, error) {
	cookie, err := r.Cookie(string(inboxCookieName))
	if err == nil && cookie.Value != "" {
		inbox, lookupErr := a.repo.GetInboxByToken(r.Context(), cookie.Value)
		if lookupErr == nil {
			if err := a.repo.UpdateLastAccessed(r.Context(), inbox.ID); err != nil && !errors.Is(err, repository.ErrNotFound) {
				return nil, false, err
			}
			return inbox, false, nil
		}
		if !errors.Is(lookupErr, repository.ErrNotFound) {
			return nil, false, lookupErr
		}
	}

	inbox, err := a.createInbox(r.Context())
	if err != nil {
		return nil, false, err
	}
	return inbox, true, nil
}

func (a *app) createInbox(ctx context.Context) (*models.Inbox, error) {
	for range 5 {
		localPart := generateLocalPart()
		inbox := &models.Inbox{
			Address:   fmt.Sprintf("%s@%s", localPart, a.options.Domain),
			LocalPart: localPart,
			Token:     uuid.NewString(),
			ExpiresAt: time.Now().UTC().Add(a.options.InboxTTL),
		}

		err := a.repo.CreateInbox(ctx, inbox)
		if err == nil {
			return inbox, nil
		}
		if !errors.Is(err, repository.ErrConflict) {
			return nil, err
		}
	}

	return nil, fmt.Errorf("create inbox: exhausted retries")
}

func generateLocalPart() string {
	return strings.ReplaceAll(strings.ToLower(uuid.NewString()), "-", "")[:12]
}
