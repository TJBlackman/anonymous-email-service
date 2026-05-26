package web

import (
	"errors"
	"fmt"
	"net/http"
	"strings"
	"time"

	"anonymous-email-service/internal/auth"
	"anonymous-email-service/internal/clientip"
	"anonymous-email-service/internal/models"
	"anonymous-email-service/internal/repository"
)

type landingData struct {
	commonData
}

type loginData struct {
	commonData
	Error string
}

type registerData struct {
	commonData
	Domains   []*models.Domain
	LocalPart string
	Error     string
}

type registerSuccessData struct {
	commonData
	Address  string
	Password string
}

// HandleLanding renders the public landing page. A logged-in visitor is sent
// straight to their inbox.
func (a *app) HandleLanding(w http.ResponseWriter, r *http.Request) {
	if a.currentInbox(r) != nil {
		http.Redirect(w, r, "/app", http.StatusSeeOther)
		return
	}
	if err := a.templates.landing.ExecuteTemplate(w, "base", landingData{
		commonData: commonData{Title: "Anonymous Email Service"},
	}); err != nil {
		a.logError("render landing failed", err)
		http.Error(w, "template rendering failed", http.StatusInternalServerError)
	}
}

func (a *app) HandleLoginForm(w http.ResponseWriter, r *http.Request) {
	if a.currentInbox(r) != nil {
		http.Redirect(w, r, "/app", http.StatusSeeOther)
		return
	}
	a.renderLogin(w, "", http.StatusOK)
}

func (a *app) HandleLogin(w http.ResponseWriter, r *http.Request) {
	if err := r.ParseForm(); err != nil {
		a.renderLogin(w, "Invalid form submission.", http.StatusBadRequest)
		return
	}

	address := strings.ToLower(strings.TrimSpace(r.PostFormValue("address")))
	password := r.PostFormValue("password")

	inbox, err := a.repo.GetInboxByAddress(r.Context(), address)
	if err != nil && !errors.Is(err, repository.ErrNotFound) {
		a.logError("login lookup failed", err)
		a.renderLogin(w, "Something went wrong. Please try again.", http.StatusInternalServerError)
		return
	}

	// Verify against the found hash, or a dummy compare on a miss, so the
	// response time and message do not reveal whether the address exists.
	if inbox == nil {
		auth.VerifyPassword(dummyBcryptHash, password)
		a.renderLogin(w, "Invalid email address or password.", http.StatusUnauthorized)
		return
	}
	if !auth.VerifyPassword(inbox.PasswordHash, password) {
		a.renderLogin(w, "Invalid email address or password.", http.StatusUnauthorized)
		return
	}

	if err := a.startSession(w, r, inbox.ID); err != nil {
		a.logError("create session failed", err)
		a.renderLogin(w, "Something went wrong. Please try again.", http.StatusInternalServerError)
		return
	}
	http.Redirect(w, r, "/app", http.StatusSeeOther)
}

func (a *app) HandleLogout(w http.ResponseWriter, r *http.Request) {
	if cookie, err := r.Cookie(a.sessionCookieName); err == nil && cookie.Value != "" {
		if err := a.repo.DeleteSession(r.Context(), cookie.Value); err != nil && !errors.Is(err, repository.ErrNotFound) {
			a.logError("delete session failed", err)
		}
	}
	a.clearSessionCookie(w)
	http.Redirect(w, r, "/", http.StatusSeeOther)
}

func (a *app) HandleRegisterForm(w http.ResponseWriter, r *http.Request) {
	if a.currentInbox(r) != nil {
		http.Redirect(w, r, "/app", http.StatusSeeOther)
		return
	}
	a.renderRegister(w, r, "", "", http.StatusOK)
}

func (a *app) HandleRegister(w http.ResponseWriter, r *http.Request) {
	if err := r.ParseForm(); err != nil {
		a.renderRegister(w, r, "", "Invalid form submission.", http.StatusBadRequest)
		return
	}

	domain := strings.ToLower(strings.TrimSpace(r.PostFormValue("domain")))

	localPart, err := validateLocalPart(r.PostFormValue("local_part"))
	if err != nil {
		a.renderRegister(w, r, r.PostFormValue("local_part"), err.Error(), http.StatusBadRequest)
		return
	}

	password, err := auth.GeneratePassword()
	if err != nil {
		a.logError("generate password failed", err)
		a.renderRegister(w, r, localPart, "Something went wrong. Please try again.", http.StatusInternalServerError)
		return
	}
	hash, err := auth.HashPassword(password, a.options.BcryptCost)
	if err != nil {
		a.logError("hash password failed", err)
		a.renderRegister(w, r, localPart, "Something went wrong. Please try again.", http.StatusInternalServerError)
		return
	}

	inbox, err := a.createInbox(r.Context(), clientip.FromHTTPRequest(r), domain, localPart, hash)
	if err != nil {
		var limitErr *rateLimitError
		if errors.As(err, &limitErr) {
			writeRateLimitResponse(w, limitErr.RetryAfter)
			return
		}
		if errors.Is(err, errNoDomainAvailable) {
			a.renderRegister(w, r, localPart, "No domains are available right now. Please try again later.", http.StatusServiceUnavailable)
			return
		}
		if errors.Is(err, repository.ErrConflict) {
			a.renderRegister(w, r, localPart,
				fmt.Sprintf("%s@%s is already taken. Please choose another.", localPart, domain),
				http.StatusConflict)
			return
		}
		a.logError("create inbox failed", err)
		a.renderRegister(w, r, localPart, "Something went wrong. Please try again.", http.StatusInternalServerError)
		return
	}

	// Show the generated password exactly once. It is never persisted in
	// plaintext or set in a session here — the user must log in with it.
	if err := a.templates.registerSuccess.ExecuteTemplate(w, "base", registerSuccessData{
		commonData: commonData{Title: "Email Address Created"},
		Address:    inbox.Address,
		Password:   password,
	}); err != nil {
		a.logError("render register success failed", err)
		http.Error(w, "template rendering failed", http.StatusInternalServerError)
	}
}

// startSession mints a fresh session token (defeating session fixation), stores
// it, and sets the session cookie.
func (a *app) startSession(w http.ResponseWriter, r *http.Request, inboxID int64) error {
	session := &models.Session{
		Token:     auth.NewSessionToken(),
		InboxID:   inboxID,
		ExpiresAt: time.Now().UTC().Add(a.options.SessionTTL),
	}
	if err := a.repo.CreateSession(r.Context(), session); err != nil {
		return err
	}
	a.setSessionCookie(w, session.Token)
	return nil
}

// currentInbox returns the logged-in inbox for a request based on its session
// cookie, or nil. Used by public pages to redirect already-authenticated users.
func (a *app) currentInbox(r *http.Request) *models.Inbox {
	cookie, err := r.Cookie(a.sessionCookieName)
	if err != nil || cookie.Value == "" {
		return nil
	}
	_, inbox, err := a.repo.GetSession(r.Context(), cookie.Value)
	if err != nil {
		return nil
	}
	return inbox
}

func (a *app) renderLogin(w http.ResponseWriter, errMsg string, status int) {
	if status != http.StatusOK {
		w.WriteHeader(status)
	}
	if err := a.templates.login.ExecuteTemplate(w, "base", loginData{
		commonData: commonData{Title: "Log In"},
		Error:      errMsg,
	}); err != nil {
		a.logError("render login failed", err)
	}
}

// localPartMinLen and localPartMaxLen bound the user-chosen address name. The
// max stays well under the 64-octet RFC 5321 local-part limit.
const (
	localPartMinLen = 3
	localPartMaxLen = 64
)

// validateLocalPart normalizes and validates the user-chosen part before the
// '@'. It lowercases the input and allows only ASCII letters and digits. On
// failure it returns a user-facing message suitable for rendering in the form.
func validateLocalPart(raw string) (string, error) {
	local := strings.ToLower(strings.TrimSpace(raw))
	if local == "" {
		return "", fmt.Errorf("Please choose a name for your address.")
	}
	if len(local) < localPartMinLen || len(local) > localPartMaxLen {
		return "", fmt.Errorf("Name must be %d–%d characters.", localPartMinLen, localPartMaxLen)
	}
	for _, c := range local {
		if !(c >= 'a' && c <= 'z') && !(c >= '0' && c <= '9') {
			return "", fmt.Errorf("Use letters and numbers only.")
		}
	}
	return local, nil
}

func (a *app) renderRegister(w http.ResponseWriter, r *http.Request, localPart, errMsg string, status int) {
	domains, err := a.repo.ListDomains(r.Context(), true)
	if err != nil {
		a.logError("list domains failed", err)
		http.Error(w, "failed to load registration", http.StatusInternalServerError)
		return
	}
	if status != http.StatusOK {
		w.WriteHeader(status)
	}
	if err := a.templates.register.ExecuteTemplate(w, "base", registerData{
		commonData: commonData{Title: "Create an Email Address"},
		Domains:    domains,
		LocalPart:  localPart,
		Error:      errMsg,
	}); err != nil {
		a.logError("render register failed", err)
	}
}

// dummyBcryptHash is a valid bcrypt hash of an arbitrary value used to equalize
// login timing when the submitted address does not exist.
const dummyBcryptHash = "$2a$10$N9qo8uLOickgx2ZMRZoMyeIjZAgcfl7p92ldGxad68LJZdL17lhWy"
