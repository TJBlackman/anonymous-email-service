package web

import (
	"crypto/subtle"
	"errors"
	"net/http"
	"sync"
	"time"

	"anonymous-email-service/internal/auth"
	"anonymous-email-service/internal/domainutil"
	"anonymous-email-service/internal/models"
	"anonymous-email-service/internal/repository"
)

// adminSessionTTL is how long an admin login stays valid. Admin sessions are
// in-memory only (a single operator credential), so they also reset on restart.
const adminSessionTTL = time.Hour

type adminData struct {
	commonData
	Domains []*models.Domain
	Error   string
}

type adminLoginData struct {
	commonData
	Error string
}

// adminSessionStore holds active admin session tokens and their expiries. It is
// in-memory because the admin credential comes from the environment, not the
// database — there is no persistent admin record to attach sessions to.
type adminSessionStore struct {
	mu       sync.Mutex
	sessions map[string]time.Time
}

func newAdminSessionStore() *adminSessionStore {
	return &adminSessionStore{sessions: map[string]time.Time{}}
}

func (s *adminSessionStore) create(token string, expiresAt time.Time) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.sessions[token] = expiresAt
}

// validate reports whether the token is active, and if so slides its expiry
// forward by adminSessionTTL.
func (s *adminSessionStore) validate(token string) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	expiresAt, ok := s.sessions[token]
	if !ok {
		return false
	}
	if !expiresAt.After(time.Now()) {
		delete(s.sessions, token)
		return false
	}
	s.sessions[token] = time.Now().Add(adminSessionTTL)
	return true
}

func (s *adminSessionStore) delete(token string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	delete(s.sessions, token)
}

// adminCookieName is the admin session cookie name, distinct from the mailbox
// session cookie so the two auth realms never share credentials.
func adminCookieName(secure bool) string {
	if secure {
		return "__Host-admin_session"
	}
	return "admin_session"
}

// adminEnabled reports whether admin credentials were configured. When they are
// not, the entire /admin area is disabled rather than left open.
func (a *app) adminEnabled() bool {
	return a.options.AdminUsername != "" && a.options.AdminPasswordHash != ""
}

func (a *app) setAdminCookie(w http.ResponseWriter, token string) {
	http.SetCookie(w, &http.Cookie{
		Name:     a.adminCookieName,
		Value:    token,
		Path:     "/",
		HttpOnly: true,
		SameSite: http.SameSiteLaxMode,
		Secure:   a.options.CookieSecure,
		MaxAge:   int(adminSessionTTL.Seconds()),
	})
}

func (a *app) clearAdminCookie(w http.ResponseWriter) {
	http.SetCookie(w, &http.Cookie{
		Name:     a.adminCookieName,
		Value:    "",
		Path:     "/",
		HttpOnly: true,
		SameSite: http.SameSiteLaxMode,
		Secure:   a.options.CookieSecure,
		MaxAge:   -1,
	})
}

// adminAuthMiddleware guards the /admin management routes. With no configured
// credentials it returns 503; otherwise it requires a valid admin session cookie
// and redirects to /admin/login when absent.
func (a *app) adminAuthMiddleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !a.adminEnabled() {
			http.Error(w, "admin area is not configured", http.StatusServiceUnavailable)
			return
		}
		cookie, err := r.Cookie(a.adminCookieName)
		if err != nil || cookie.Value == "" || !a.adminSessions.validate(cookie.Value) {
			http.Redirect(w, r, "/admin/login", http.StatusSeeOther)
			return
		}
		next.ServeHTTP(w, r.WithContext(r.Context()))
	})
}

func (a *app) HandleAdminLoginForm(w http.ResponseWriter, r *http.Request) {
	if !a.adminEnabled() {
		http.Error(w, "admin area is not configured", http.StatusServiceUnavailable)
		return
	}
	a.renderAdminLogin(w, "", http.StatusOK)
}

func (a *app) HandleAdminLogin(w http.ResponseWriter, r *http.Request) {
	if !a.adminEnabled() {
		http.Error(w, "admin area is not configured", http.StatusServiceUnavailable)
		return
	}
	if err := r.ParseForm(); err != nil {
		a.renderAdminLogin(w, "Invalid form submission.", http.StatusBadRequest)
		return
	}

	username := r.PostFormValue("username")
	password := r.PostFormValue("password")

	usernameOK := subtle.ConstantTimeCompare([]byte(username), []byte(a.options.AdminUsername)) == 1
	passwordOK := auth.VerifyPassword(a.options.AdminPasswordHash, password)
	if !usernameOK || !passwordOK {
		a.renderAdminLogin(w, "Invalid username or password.", http.StatusUnauthorized)
		return
	}

	token := auth.NewSessionToken()
	a.adminSessions.create(token, time.Now().Add(adminSessionTTL))
	a.setAdminCookie(w, token)
	http.Redirect(w, r, "/admin", http.StatusSeeOther)
}

func (a *app) HandleAdminLogout(w http.ResponseWriter, r *http.Request) {
	if cookie, err := r.Cookie(a.adminCookieName); err == nil && cookie.Value != "" {
		a.adminSessions.delete(cookie.Value)
	}
	a.clearAdminCookie(w)
	http.Redirect(w, r, "/admin/login", http.StatusSeeOther)
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
		commonData: commonData{Title: "Domain Administration"},
		Domains:    domains,
		Error:      errMsg,
	}); err != nil {
		a.logError("render admin failed", err)
	}
}

func (a *app) renderAdminLogin(w http.ResponseWriter, errMsg string, status int) {
	if status != http.StatusOK {
		w.WriteHeader(status)
	}
	if err := a.templates.adminLogin.ExecuteTemplate(w, "base", adminLoginData{
		commonData: commonData{Title: "Admin Login"},
		Error:      errMsg,
	}); err != nil {
		a.logError("render admin login failed", err)
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
