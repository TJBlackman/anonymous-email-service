package web

import (
	"errors"
	"html/template"
	"log/slog"
	"net/http"

	"anonymous-email-service/internal/repository"
)

type app struct {
	repo      repository.Repository
	logger    *slog.Logger
	templates *template.Template
}

type indexData struct {
	Title       string
	Placeholder string
}

func (a *app) HandleIndex(w http.ResponseWriter, r *http.Request) {
	data := indexData{
		Title:       "Anonymous Inbox",
		Placeholder: "Project scaffold initialized. Inbox and email delivery behavior will be added in later phases.",
	}

	if err := a.templates.ExecuteTemplate(w, "base", data); err != nil {
		a.logger.Error("render index failed", "error", err)
		http.Error(w, "template rendering failed", http.StatusInternalServerError)
	}
}

func (a *app) HandleViewEmail(w http.ResponseWriter, r *http.Request) {
	http.Error(w, "email view is not implemented", http.StatusNotImplemented)
}

func (a *app) HandleAttachment(w http.ResponseWriter, r *http.Request) {
	http.Error(w, "attachment download is not implemented", http.StatusNotImplemented)
}

func (a *app) HandleDeleteEmail(w http.ResponseWriter, r *http.Request) {
	http.Error(w, "email deletion is not implemented", http.StatusNotImplemented)
}

func (a *app) HandleNewInbox(w http.ResponseWriter, r *http.Request) {
	http.Error(w, "new inbox generation is not implemented", http.StatusNotImplemented)
}

func (a *app) HandleAPIEmails(w http.ResponseWriter, r *http.Request) {
	http.Error(w, "email API is not implemented", http.StatusNotImplemented)
}

func (a *app) HandleHealth(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(http.StatusOK)
	_, _ = w.Write([]byte(`{"status":"ok"}`))
}

func parseTemplates(pattern string) (*template.Template, error) {
	tmpl, err := template.ParseGlob(pattern)
	if err != nil {
		return nil, err
	}
	if tmpl.Lookup("base") == nil {
		return nil, errors.New("base template is missing")
	}
	return tmpl, nil
}
