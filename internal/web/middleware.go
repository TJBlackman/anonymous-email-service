package web

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"strings"
	"time"

	"anonymous-email-service/internal/clientip"
	"anonymous-email-service/internal/models"
	"anonymous-email-service/internal/repository"
	"github.com/google/uuid"
)

type contextKey string

const (
	inboxContextKey contextKey = "inbox"
)

type rateLimitError struct {
	RetryAfter time.Duration
}

func (e *rateLimitError) Error() string {
	return "rate limit exceeded"
}

// errNoDomainAvailable is returned by createInbox when there is no enabled
// domain to assign an address to (empty domains table and no bootstrap seed).
var errNoDomainAvailable = errors.New("no domain available for inbox creation")

func SessionMiddleware(app *app) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			inbox, created, err := app.resolveInbox(r)
			if err != nil {
				var limitErr *rateLimitError
				if errors.As(err, &limitErr) {
					writeRateLimitResponse(w, limitErr.RetryAfter)
					return
				}
				if errors.Is(err, errNoDomainAvailable) {
					app.logError("resolve inbox failed: no domain configured", err)
					http.Error(w, "no domains are configured; an administrator must add one at /admin", http.StatusServiceUnavailable)
					return
				}
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
	cookie, err := r.Cookie(a.cookieName)
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

	inbox, err := a.createInbox(r.Context(), clientip.FromHTTPRequest(r), "")
	if err != nil {
		return nil, false, err
	}
	return inbox, true, nil
}

// createInbox creates a new inbox on the chosen domain. An empty domain (or one
// that is not currently enabled) falls back to the default enabled domain.
// Returns errNoDomainAvailable when no enabled domain exists.
func (a *app) createInbox(ctx context.Context, clientAddr, domain string) (*models.Inbox, error) {
	if limiter := a.options.CreateInboxLimiter; limiter != nil {
		decision := limiter.Allow(clientAddr)
		if !decision.Allowed {
			return nil, &rateLimitError{RetryAfter: decision.RetryAfter}
		}
	}

	effectiveDomain, err := a.resolveCreateDomain(ctx, domain)
	if err != nil {
		return nil, err
	}
	if effectiveDomain == "" {
		return nil, errNoDomainAvailable
	}

	for range 5 {
		localPart := generateLocalPart()
		inbox := &models.Inbox{
			Address:   fmt.Sprintf("%s@%s", localPart, effectiveDomain),
			LocalPart: localPart,
			Token:     uuid.NewString(),
			ExpiresAt: a.options.inboxExpiry(time.Now()),
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

// resolveCreateDomain validates a requested domain against the enabled set,
// falling back to the default domain when the request is empty or the requested
// domain is not enabled (e.g. a stale dropdown selection).
func (a *app) resolveCreateDomain(ctx context.Context, requested string) (string, error) {
	requested = strings.ToLower(strings.TrimSpace(requested))
	if requested != "" {
		enabled, err := a.repo.IsDomainEnabled(ctx, requested)
		if err != nil {
			return "", err
		}
		if enabled {
			return requested, nil
		}
	}
	return a.defaultDomain(ctx)
}

func generateLocalPart() string {
	return strings.ReplaceAll(strings.ToLower(uuid.NewString()), "-", "")[:12]
}

func cookieName(secure bool) string {
	if secure {
		return "__Host-inbox_token"
	}
	return "inbox_token"
}

func writeRateLimitResponse(w http.ResponseWriter, retryAfter time.Duration) {
	if retryAfter > 0 {
		w.Header().Set("Retry-After", retryAfterSeconds(retryAfter))
	}
	http.Error(w, "rate limit exceeded", http.StatusTooManyRequests)
}
