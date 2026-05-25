package web

import (
	"testing"
	"time"
)

func TestInboxExpiry(t *testing.T) {
	now := time.Date(2026, 1, 2, 3, 4, 5, 0, time.UTC)

	t.Run("positive ttl adds to now", func(t *testing.T) {
		opts := Options{InboxTTL: 48 * time.Hour}
		got := opts.inboxExpiry(now)
		want := now.Add(48 * time.Hour)
		if !got.Equal(want) {
			t.Fatalf("inboxExpiry = %v, want %v", got, want)
		}
	})

	t.Run("zero ttl never expires", func(t *testing.T) {
		opts := Options{InboxTTL: 0}
		got := opts.inboxExpiry(now)
		if !got.Equal(neverExpires) {
			t.Fatalf("inboxExpiry = %v, want %v", got, neverExpires)
		}
		// Sentinel must outlast any plausible "now" so lookups keep matching and
		// the cleanup worker never deletes the inbox.
		if !got.After(now.AddDate(100, 0, 0)) {
			t.Fatalf("neverExpires %v is not far enough in the future", got)
		}
	})
}

func TestCookieMaxAge(t *testing.T) {
	t.Run("positive ttl uses ttl seconds", func(t *testing.T) {
		opts := Options{InboxTTL: 2 * time.Hour}
		if got, want := opts.cookieMaxAge(), int((2 * time.Hour).Seconds()); got != want {
			t.Fatalf("cookieMaxAge = %d, want %d", got, want)
		}
	})

	t.Run("zero ttl is long lived not session", func(t *testing.T) {
		opts := Options{InboxTTL: 0}
		// MaxAge of 0 would make a session cookie; we want a long-lived one.
		if got := opts.cookieMaxAge(); got <= 0 {
			t.Fatalf("cookieMaxAge = %d, want a large positive value", got)
		}
	})
}
