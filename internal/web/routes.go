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
	CookieSecure       bool
	SessionTTL         time.Duration
	BcryptCost         int
	AdminUsername      string
	AdminPasswordHash  string
	CreateInboxLimiter *ratelimit.FixedWindowLimiter
}

// neverExpires is the ExpiresAt sentinel for inboxes. A registered mailbox is an
// account, not a throwaway, so it never auto-expires: this value is far enough
// in the future that the cleanup worker's "expires_at <= now" delete never
// matches it.
var neverExpires = time.Date(9999, 1, 1, 0, 0, 0, 0, time.UTC)

type templateSet struct {
	index           *template.Template
	email           *template.Template
	admin           *template.Template
	landing         *template.Template
	login           *template.Template
	register        *template.Template
	registerSuccess *template.Template
	adminLogin      *template.Template
}

type app struct {
	repo              repository.Repository
	logger            *slog.Logger
	templates         templateSet
	options           Options
	sessionCookieName string
	adminCookieName   string
	adminSessions     *adminSessionStore
	emailHTMLPolicy   *bluemonday.Policy
}

func RegisterRoutes(repo repository.Repository, logger *slog.Logger, templateDir string, options Options) (http.Handler, error) {
	templates, err := parseTemplates(templateDir)
	if err != nil {
		return nil, err
	}
	staticDir := resolveStaticDir(templateDir)

	app := &app{
		repo:              repo,
		logger:            logger,
		templates:         templates,
		options:           options,
		sessionCookieName: sessionCookieName(options.CookieSecure),
		adminCookieName:   adminCookieName(options.CookieSecure),
		adminSessions:     newAdminSessionStore(),
		emailHTMLPolicy:   newEmailHTMLPolicy(),
	}

	root := http.NewServeMux()
	root.HandleFunc("GET /health", app.HandleHealth)
	root.Handle("GET /static/", app.staticHandler(http.Dir(staticDir)))

	// Public mailbox auth pages (no session required). {$} matches only the exact
	// root path so it does not conflict with the /app and /admin subtrees.
	root.HandleFunc("GET /{$}", app.HandleLanding)
	root.HandleFunc("GET /register", app.HandleRegisterForm)
	root.HandleFunc("POST /register", app.HandleRegister)
	root.HandleFunc("GET /login", app.HandleLoginForm)
	root.HandleFunc("POST /login", app.HandleLogin)
	root.HandleFunc("POST /logout", app.HandleLogout)

	// Protected mailbox area: everything under /app requires a logged-in session.
	appMux := http.NewServeMux()
	appMux.HandleFunc("GET /app", app.HandleIndex)
	appMux.HandleFunc("GET /app/email/{id}", app.HandleViewEmail)
	appMux.HandleFunc("GET /app/email/{id}/attachment/{aid}", app.HandleAttachment)
	appMux.HandleFunc("POST /app/email/{id}/delete", app.HandleDeleteEmail)
	appMux.HandleFunc("GET /app/api/emails", app.HandleAPIEmails)
	root.Handle("/app", MailboxAuthMiddleware(app)(appMux))
	root.Handle("/app/", MailboxAuthMiddleware(app)(appMux))

	// Admin realm: a separate env-provisioned credential, unrelated to mailboxes.
	adminMux := http.NewServeMux()
	adminMux.HandleFunc("GET /admin", app.HandleAdminIndex)
	adminMux.HandleFunc("POST /admin/domains", app.HandleAdminAddDomain)
	adminMux.HandleFunc("POST /admin/domains/{name}/toggle", app.HandleAdminToggleDomain)
	root.HandleFunc("GET /admin/login", app.HandleAdminLoginForm)
	root.HandleFunc("POST /admin/login", app.HandleAdminLogin)
	root.HandleFunc("POST /admin/logout", app.HandleAdminLogout)
	root.Handle("/admin", app.adminAuthMiddleware(adminMux))
	root.Handle("/admin/", app.adminAuthMiddleware(adminMux))

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
	page := func(name string) (*template.Template, error) {
		return template.ParseFiles(base, filepath.Join(templateDir, name))
	}

	indexTmpl, err := page("index.gohtml")
	if err != nil {
		return templateSet{}, err
	}
	emailTmpl, err := page("email.gohtml")
	if err != nil {
		return templateSet{}, err
	}
	adminTmpl, err := page("admin.gohtml")
	if err != nil {
		return templateSet{}, err
	}
	landingTmpl, err := page("landing.gohtml")
	if err != nil {
		return templateSet{}, err
	}
	loginTmpl, err := page("login.gohtml")
	if err != nil {
		return templateSet{}, err
	}
	registerTmpl, err := page("register.gohtml")
	if err != nil {
		return templateSet{}, err
	}
	registerSuccessTmpl, err := page("register_success.gohtml")
	if err != nil {
		return templateSet{}, err
	}
	adminLoginTmpl, err := page("admin_login.gohtml")
	if err != nil {
		return templateSet{}, err
	}

	return templateSet{
		index:           indexTmpl,
		email:           emailTmpl,
		admin:           adminTmpl,
		landing:         landingTmpl,
		login:           loginTmpl,
		register:        registerTmpl,
		registerSuccess: registerSuccessTmpl,
		adminLogin:      adminLoginTmpl,
	}, nil
}
