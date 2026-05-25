package web

import (
	"context"
	"html/template"
	"log/slog"
	"net/http"
	"path/filepath"
	"time"

	"anonymous-email-service/internal/ratelimit"
	"anonymous-email-service/internal/repository"
	"github.com/microcosm-cc/bluemonday"
)

type Options struct {
	DefaultDomain      string
	InboxTTL           time.Duration
	CookieSecure       bool
	CreateInboxLimiter *ratelimit.FixedWindowLimiter
}

// neverExpires is the ExpiresAt sentinel used when InboxTTL is 0 (mailbox never
// auto-cleaned). It is far enough in the future that "expires_at > now" lookups
// keep passing and the cleanup worker's "expires_at <= now" delete never matches.
var neverExpires = time.Date(9999, 1, 1, 0, 0, 0, 0, time.UTC)

// inboxExpiry returns the ExpiresAt for a newly created inbox given the
// configured TTL. A zero TTL means the inbox never expires.
func (o Options) inboxExpiry(now time.Time) time.Time {
	if o.InboxTTL <= 0 {
		return neverExpires
	}
	return now.UTC().Add(o.InboxTTL)
}

// cookieMaxAge returns the inbox cookie MaxAge in seconds. A zero TTL yields a
// long-lived cookie so the inbox stays accessible across browser restarts,
// matching the "never cleaned up" intent.
func (o Options) cookieMaxAge() int {
	if o.InboxTTL <= 0 {
		return int((10 * 365 * 24 * time.Hour).Seconds())
	}
	return int(o.InboxTTL.Seconds())
}

type templateSet struct {
	index *template.Template
	email *template.Template
	admin *template.Template
}

type app struct {
	repo            repository.Repository
	logger          *slog.Logger
	templates       templateSet
	options         Options
	cookieName      string
	emailHTMLPolicy *bluemonday.Policy
}

func RegisterRoutes(repo repository.Repository, logger *slog.Logger, templateDir string, options Options) (http.Handler, error) {
	templates, err := parseTemplates(templateDir)
	if err != nil {
		return nil, err
	}
	staticDir := resolveStaticDir(templateDir)

	app := &app{
		repo:            repo,
		logger:          logger,
		templates:       templates,
		options:         options,
		cookieName:      cookieName(options.CookieSecure),
		emailHTMLPolicy: newEmailHTMLPolicy(),
	}

	root := http.NewServeMux()
	root.HandleFunc("GET /health", app.HandleHealth)
	root.Handle("GET /static/", app.staticHandler(http.Dir(staticDir)))

	adminMux := http.NewServeMux()
	adminMux.HandleFunc("GET /admin", app.HandleAdminIndex)
	adminMux.HandleFunc("POST /admin/domains", app.HandleAdminAddDomain)
	adminMux.HandleFunc("POST /admin/domains/{name}/toggle", app.HandleAdminToggleDomain)
	// Registered outside SessionMiddleware: admin management is not tied to an
	// inbox session. adminGuard is the single auth chokepoint (see admin.go).
	root.Handle("/admin", adminGuard(adminMux))
	root.Handle("/admin/", adminGuard(adminMux))

	inboxMux := http.NewServeMux()
	inboxMux.HandleFunc("GET /", app.HandleIndex)
	inboxMux.HandleFunc("GET /email/{id}", app.HandleViewEmail)
	inboxMux.HandleFunc("GET /email/{id}/attachment/{aid}", app.HandleAttachment)
	inboxMux.HandleFunc("POST /email/{id}/delete", app.HandleDeleteEmail)
	inboxMux.HandleFunc("POST /inbox/new", app.HandleNewInbox)
	inboxMux.HandleFunc("GET /api/emails", app.HandleAPIEmails)

	root.Handle("/", SessionMiddleware(app)(inboxMux))
	return app.wrapHTTP(root), nil
}

// defaultDomain returns the name of the first (oldest) enabled domain, falling
// back to the bootstrap seed from Options. Returns "" when no domain is
// available, in which case inbox creation cannot proceed.
func (a *app) defaultDomain(ctx context.Context) (string, error) {
	domains, err := a.repo.ListDomains(ctx, true)
	if err != nil {
		return "", err
	}
	if len(domains) > 0 {
		return domains[0].Name, nil
	}
	return a.options.DefaultDomain, nil
}

func resolveStaticDir(templateDir string) string {
	if templateDir == "" || templateDir == "templates" {
		return "static"
	}
	return filepath.Join(filepath.Dir(templateDir), "static")
}

func parseTemplates(templateDir string) (templateSet, error) {
	if templateDir == "" {
		templateDir = "templates"
	}
	base := filepath.Join(templateDir, "base.gohtml")
	index := filepath.Join(templateDir, "index.gohtml")
	email := filepath.Join(templateDir, "email.gohtml")
	admin := filepath.Join(templateDir, "admin.gohtml")

	indexTmpl, err := template.ParseFiles(base, index)
	if err != nil {
		return templateSet{}, err
	}
	emailTmpl, err := template.ParseFiles(base, email)
	if err != nil {
		return templateSet{}, err
	}
	adminTmpl, err := template.ParseFiles(base, admin)
	if err != nil {
		return templateSet{}, err
	}

	return templateSet{
		index: indexTmpl,
		email: emailTmpl,
		admin: adminTmpl,
	}, nil
}
