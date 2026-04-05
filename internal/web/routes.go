package web

import (
	"log/slog"
	"net/http"

	"anonymous-email-service/internal/repository"
)

func RegisterRoutes(repo repository.Repository, logger *slog.Logger, templatePattern string) (http.Handler, error) {
	templates, err := parseTemplates(templatePattern)
	if err != nil {
		return nil, err
	}

	app := &app{
		repo:      repo,
		logger:    logger,
		templates: templates,
	}

	mux := http.NewServeMux()
	mux.HandleFunc("GET /health", app.HandleHealth)
	mux.HandleFunc("GET /", app.HandleIndex)
	mux.HandleFunc("GET /email/{id}", app.HandleViewEmail)
	mux.HandleFunc("GET /email/{id}/attachment/{aid}", app.HandleAttachment)
	mux.HandleFunc("POST /email/{id}/delete", app.HandleDeleteEmail)
	mux.HandleFunc("POST /inbox/new", app.HandleNewInbox)
	mux.HandleFunc("GET /api/emails", app.HandleAPIEmails)
	mux.Handle("GET /static/", http.StripPrefix("/static/", http.FileServer(http.Dir("static"))))

	return SessionMiddleware(repo, "example.test")(mux), nil
}
