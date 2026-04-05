package web

import (
	"context"
	"net/http"

	"anonymous-email-service/internal/models"
	"anonymous-email-service/internal/repository"
)

type contextKey string

const inboxContextKey contextKey = "inbox"

func SessionMiddleware(repo repository.Repository, domain string) func(http.Handler) http.Handler {
	_ = repo
	_ = domain

	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			next.ServeHTTP(w, r)
		})
	}
}

func withInbox(ctx context.Context, inbox *models.Inbox) context.Context {
	return context.WithValue(ctx, inboxContextKey, inbox)
}
