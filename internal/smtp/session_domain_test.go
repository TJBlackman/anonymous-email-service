package smtp

import (
	"context"
	"errors"
	"testing"
	"time"

	"anonymous-email-service/internal/models"
	gosmtp "github.com/emersion/go-smtp"
)

func TestSessionRcptAcceptsAnyEnabledDomain(t *testing.T) {
	repo := &stubRepository{
		enabledDomains: map[string]bool{"second.test": true},
		inboxByAddress: map[string]*models.Inbox{
			"user@second.test": {ID: 7, Address: "user@second.test"},
		},
	}
	session := &Session{
		backend: &Backend{
			domains: newDomainCache(domainCacheTTL),
			repo:    repo,
		},
	}

	if err := session.Rcpt("user@second.test", nil); err != nil {
		t.Fatalf("Rcpt() returned error: %v", err)
	}
	if session.inbox == nil || session.inbox.ID != 7 {
		t.Fatalf("inbox = %+v, want loaded inbox", session.inbox)
	}
}

func TestSessionRcptRejectsDisabledDomain(t *testing.T) {
	repo := &stubRepository{
		enabledDomains: map[string]bool{"disabled.test": false},
	}
	session := &Session{
		backend: &Backend{
			domains: newDomainCache(domainCacheTTL),
			repo:    repo,
		},
	}

	err := session.Rcpt("user@disabled.test", nil)
	var smtpErr *gosmtp.SMTPError
	if !errors.As(err, &smtpErr) {
		t.Fatalf("expected SMTPError, got %T (%v)", err, err)
	}
	if smtpErr.Code != 550 || smtpErr.EnhancedCode != (gosmtp.EnhancedCode{5, 7, 1}) {
		t.Fatalf("got %d %v, want 550 5.7.1", smtpErr.Code, smtpErr.EnhancedCode)
	}
}

func TestDomainCacheServesWithinTTLThenExpires(t *testing.T) {
	cache := newDomainCache(30 * time.Second)
	current := time.Unix(1000, 0)
	cache.now = func() time.Time { return current }

	cache.set("example.test", true)

	if enabled, ok := cache.get("example.test"); !ok || !enabled {
		t.Fatalf("get within TTL = (%v, %v), want (true, true)", enabled, ok)
	}

	// Advance past the TTL; the entry should be considered stale.
	current = current.Add(31 * time.Second)
	if _, ok := cache.get("example.test"); ok {
		t.Fatal("get after TTL expiry returned ok=true, want stale miss")
	}
}

func TestBackendDomainAllowedCachesLookups(t *testing.T) {
	repo := &countingDomainRepo{stubRepository: stubRepository{enabledDomains: map[string]bool{"example.test": true}}}
	backend := &Backend{domains: newDomainCache(30 * time.Second), repo: repo}

	for i := 0; i < 3; i++ {
		allowed, err := backend.domainAllowed(t.Context(), "example.test")
		if err != nil || !allowed {
			t.Fatalf("domainAllowed() = (%v, %v), want (true, nil)", allowed, err)
		}
	}

	if repo.isDomainEnabledCalls != 1 {
		t.Fatalf("IsDomainEnabled calls = %d, want 1 (cached)", repo.isDomainEnabledCalls)
	}
}

// countingDomainRepo counts IsDomainEnabled calls to prove caching.
type countingDomainRepo struct {
	stubRepository
	isDomainEnabledCalls int
}

func (r *countingDomainRepo) IsDomainEnabled(ctx context.Context, name string) (bool, error) {
	r.isDomainEnabledCalls++
	return r.stubRepository.IsDomainEnabled(ctx, name)
}
