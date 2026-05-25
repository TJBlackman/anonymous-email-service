package web

import (
	"errors"
	"net/http"

	"anonymous-email-service/internal/domainutil"
	"anonymous-email-service/internal/models"
	"anonymous-email-service/internal/repository"
)

type adminData struct {
	Title string
	// Inbox is always nil; it exists so base.gohtml's {{if .Inbox}} guard can
	// evaluate the field on adminData without an execution error.
	Inbox   *models.Inbox
	Domains []*models.Domain
	Error   string
}

// adminGuard is the single authentication chokepoint for all /admin routes.
//
// TODO(security): LAUNCH BLOCKER — there is no authentication here. Anyone who
// can reach the HTTP port can add or disable domains. Add real auth (HTTP basic
// auth, a shared secret, or an IP allowlist) in THIS function before exposing
// the service publicly. Keeping it as the only chokepoint means handlers below
// never need to change.
func adminGuard(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		next.ServeHTTP(w, r)
	})
}

func (a *app) HandleAdminIndex(w http.ResponseWriter, r *http.Request) {
	a.renderAdmin(w, r, "", http.StatusOK)
}

func (a *app) HandleAdminAddDomain(w http.ResponseWriter, r *http.Request) {
	if err := r.ParseForm(); err != nil {
		a.renderAdmin(w, r, "Invalid form submission.", http.StatusBadRequest)
		return
	}

	name, err := domainutil.Validate(r.PostFormValue("name"))
	if err != nil {
		a.renderAdmin(w, r, capitalize(err.Error())+".", http.StatusBadRequest)
		return
	}

	if _, err := a.repo.CreateDomain(r.Context(), name); err != nil {
		if errors.Is(err, repository.ErrConflict) {
			a.renderAdmin(w, r, "Domain "+name+" already exists.", http.StatusConflict)
			return
		}
		a.logError("create domain failed", err)
		a.renderAdmin(w, r, "Failed to add domain.", http.StatusInternalServerError)
		return
	}

	http.Redirect(w, r, "/admin", http.StatusSeeOther)
}

func (a *app) HandleAdminToggleDomain(w http.ResponseWriter, r *http.Request) {
	name := r.PathValue("name")
	domain, err := a.repo.GetDomain(r.Context(), name)
	if err != nil {
		if errors.Is(err, repository.ErrNotFound) {
			http.NotFound(w, r)
			return
		}
		a.logError("get domain failed", err)
		http.Error(w, "failed to update domain", http.StatusInternalServerError)
		return
	}

	if err := a.repo.SetDomainEnabled(r.Context(), domain.Name, !domain.Enabled); err != nil {
		a.logError("set domain enabled failed", err)
		http.Error(w, "failed to update domain", http.StatusInternalServerError)
		return
	}

	http.Redirect(w, r, "/admin", http.StatusSeeOther)
}

// renderAdmin loads all domains and renders the admin page with an optional
// inline error message and HTTP status code.
func (a *app) renderAdmin(w http.ResponseWriter, r *http.Request, errMsg string, status int) {
	domains, err := a.repo.ListDomains(r.Context(), false)
	if err != nil {
		a.logError("list domains failed", err)
		http.Error(w, "failed to load admin", http.StatusInternalServerError)
		return
	}

	if status != http.StatusOK {
		w.WriteHeader(status)
	}
	if err := a.templates.admin.ExecuteTemplate(w, "base", adminData{
		Title:   "Domain Administration",
		Domains: domains,
		Error:   errMsg,
	}); err != nil {
		a.logError("render admin failed", err)
	}
}

func capitalize(s string) string {
	if s == "" {
		return s
	}
	if c := s[0]; c >= 'a' && c <= 'z' {
		return string(c-32) + s[1:]
	}
	return s
}
