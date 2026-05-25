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

// MailboxAuthMiddleware gates the /app subtree. It resolves the logged-in inbox
// from the session cookie, slides the session expiry (and cookie) forward on
// every authenticated request, and injects the inbox into the request context.
// Unauthenticated requests get a 401 JSON body for /app/api/* and a redirect to
// /login for HTML routes.
func MailboxAuthMiddleware(app *app) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			cookie, err := r.Cookie(app.sessionCookieName)
			if err != nil || cookie.Value == "" {
				app.requireMailboxAuth(w, r)
				return
			}

			session, inbox, err := app.repo.GetSession(r.Context(), cookie.Value)
			if err != nil {
				if !errors.Is(err, repository.ErrNotFound) {
					app.logError("get session failed", err)
				}
				app.clearSessionCookie(w)
				app.requireMailboxAuth(w, r)
				return
			}

			// Slide the session forward on every authenticated request.
			newExpiry := time.Now().UTC().Add(app.options.SessionTTL)
			if err := app.repo.TouchSession(r.Context(), session.Token, newExpiry); err != nil && !errors.Is(err, repository.ErrNotFound) {
				app.logError("touch session failed", err)
			}
			app.setSessionCookie(w, session.Token)
			if err := app.repo.UpdateLastAccessed(r.Context(), inbox.ID); err != nil && !errors.Is(err, repository.ErrNotFound) {
				app.logError("update last accessed failed", err)
			}

			ctx := withInbox(r.Context(), inbox)
			next.ServeHTTP(w, r.WithContext(ctx))
		})
	}
}

// requireMailboxAuth writes the unauthenticated response: 401 JSON for API
// routes under /app/api/, otherwise a redirect to the login page.
func (a *app) requireMailboxAuth(w http.ResponseWriter, r *http.Request) {
	if strings.HasPrefix(r.URL.Path, "/app/api/") {
		w.Header().Set("Content-Type", "application/json; charset=utf-8")
		w.WriteHeader(http.StatusUnauthorized)
		_, _ = w.Write([]byte(`{"error":"authentication required"}`))
		return
	}
	http.Redirect(w, r, "/login", http.StatusSeeOther)
}

func GetInboxFromContext(ctx context.Context) *models.Inbox {
	inbox, _ := ctx.Value(inboxContextKey).(*models.Inbox)
	return inbox
}

func withInbox(ctx context.Context, inbox *models.Inbox) context.Context {
	return context.WithValue(ctx, inboxContextKey, inbox)
}

// createInbox provisions a new mailbox account on the chosen domain with the
// given bcrypt password hash. An empty domain (or one that is not currently
// enabled) falls back to the default enabled domain. Registered mailboxes are
// accounts, so they are created with the never-expires sentinel. Returns
// errNoDomainAvailable when no enabled domain exists.
func (a *app) createInbox(ctx context.Context, clientAddr, domain, passwordHash string) (*models.Inbox, error) {
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
			Address:      fmt.Sprintf("%s@%s", localPart, effectiveDomain),
			LocalPart:    localPart,
			Token:        uuid.NewString(),
			PasswordHash: passwordHash,
			ExpiresAt:    neverExpires,
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

// sessionCookieName is the mailbox session cookie name. The __Host- prefix is
// used when cookies are marked Secure, which the browser enforces requires
// Secure + Path=/ + no Domain attribute.
func sessionCookieName(secure bool) string {
	if secure {
		return "__Host-session"
	}
	return "session"
}

func (a *app) setSessionCookie(w http.ResponseWriter, token string) {
	http.SetCookie(w, &http.Cookie{
		Name:     a.sessionCookieName,
		Value:    token,
		Path:     "/",
		HttpOnly: true,
		SameSite: http.SameSiteLaxMode,
		Secure:   a.options.CookieSecure,
		MaxAge:   int(a.options.SessionTTL.Seconds()),
	})
}

func (a *app) clearSessionCookie(w http.ResponseWriter) {
	http.SetCookie(w, &http.Cookie{
		Name:     a.sessionCookieName,
		Value:    "",
		Path:     "/",
		HttpOnly: true,
		SameSite: http.SameSiteLaxMode,
		Secure:   a.options.CookieSecure,
		MaxAge:   -1,
	})
}

func writeRateLimitResponse(w http.ResponseWriter, retryAfter time.Duration) {
	if retryAfter > 0 {
		w.Header().Set("Retry-After", retryAfterSeconds(retryAfter))
	}
	http.Error(w, "rate limit exceeded", http.StatusTooManyRequests)
}
