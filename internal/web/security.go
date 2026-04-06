package web

import (
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"

	"github.com/microcosm-cc/bluemonday"
)

const (
	maxPostBodyBytes      int64 = 64 << 10
	staticCacheControl          = "public, max-age=86400"
	contentSecurityPolicy       = "default-src 'self'; img-src 'self' data:; style-src 'self'; script-src 'none'; object-src 'none'; base-uri 'none'; frame-ancestors 'none'; form-action 'self'"
)

func (a *app) wrapHTTP(next http.Handler) http.Handler {
	return a.securityHeadersMiddleware(a.postSecurityMiddleware(next))
}

func (a *app) staticHandler(fs http.FileSystem) http.Handler {
	handler := http.StripPrefix("/static/", http.FileServer(fs))
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Cache-Control", staticCacheControl)
		handler.ServeHTTP(w, r)
	})
}

func (a *app) securityHeadersMiddleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("X-Content-Type-Options", "nosniff")
		w.Header().Set("X-Frame-Options", "DENY")
		w.Header().Set("Referrer-Policy", "no-referrer")
		w.Header().Set("Content-Security-Policy", contentSecurityPolicy)
		next.ServeHTTP(w, r)
	})
}

func (a *app) postSecurityMiddleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			next.ServeHTTP(w, r)
			return
		}

		if r.ContentLength > maxPostBodyBytes {
			http.Error(w, "request body too large", http.StatusRequestEntityTooLarge)
			return
		}
		r.Body = http.MaxBytesReader(w, r.Body, maxPostBodyBytes)

		if !isSameOriginRequest(r) {
			http.Error(w, "cross-origin POST rejected", http.StatusForbidden)
			return
		}

		next.ServeHTTP(w, r)
	})
}

func isSameOriginRequest(r *http.Request) bool {
	if value := strings.TrimSpace(r.Header.Get("Origin")); value != "" {
		return sameOriginURL(r, value)
	}

	if value := strings.TrimSpace(r.Header.Get("Referer")); value != "" {
		return sameOriginURL(r, value)
	}

	return false
}

func sameOriginURL(r *http.Request, value string) bool {
	if value == "null" {
		return false
	}

	parsed, err := url.Parse(value)
	if err != nil {
		return false
	}
	if parsed.Host == "" || !strings.EqualFold(parsed.Host, r.Host) {
		return false
	}

	expectedScheme := "http"
	if r.TLS != nil {
		expectedScheme = "https"
	}
	return strings.EqualFold(parsed.Scheme, expectedScheme)
}

func newEmailHTMLPolicy() *bluemonday.Policy {
	policy := bluemonday.UGCPolicy()
	policy.RequireNoFollowOnLinks(true)
	policy.RequireNoReferrerOnLinks(true)
	policy.AddTargetBlankToFullyQualifiedLinks(true)
	return policy
}

func retryAfterSeconds(retryAfter time.Duration) string {
	seconds := int(retryAfter.Round(time.Second).Seconds())
	if seconds < 1 {
		seconds = 1
	}
	return strconv.Itoa(seconds)
}
