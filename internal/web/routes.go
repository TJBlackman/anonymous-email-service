package web

import (
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
	Domain             string
	InboxTTL           time.Duration
	CookieSecure       bool
	CreateInboxLimiter *ratelimit.FixedWindowLimiter
}

type templateSet struct {
	index *template.Template
	email *template.Template
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

	indexTmpl, err := template.ParseFiles(base, index)
	if err != nil {
		return templateSet{}, err
	}
	emailTmpl, err := template.ParseFiles(base, email)
	if err != nil {
		return templateSet{}, err
	}

	return templateSet{
		index: indexTmpl,
		email: emailTmpl,
	}, nil
}
