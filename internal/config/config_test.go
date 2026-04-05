package config

import (
	"log/slog"
	"testing"
	"time"
)

func TestLoadRequiresDomain(t *testing.T) {
	t.Setenv("DOMAIN", "")

	_, err := Load()
	if err == nil {
		t.Fatal("expected error when DOMAIN is missing")
	}
}

func TestLoadRejectsBlankDomain(t *testing.T) {
	t.Setenv("DOMAIN", "   ")

	_, err := Load()
	if err == nil {
		t.Fatal("expected error when DOMAIN is blank")
	}
}

func TestLoadAppliesDefaults(t *testing.T) {
	t.Setenv("DOMAIN", "mail.test")
	t.Setenv("SMTP_LISTEN_ADDR", "")
	t.Setenv("HTTP_LISTEN_ADDR", "")
	t.Setenv("DATABASE_PATH", "")
	t.Setenv("INBOX_TTL_HOURS", "")
	t.Setenv("MAX_EMAIL_SIZE_MB", "")
	t.Setenv("MAX_ATTACHMENT_MB", "")
	t.Setenv("CLEANUP_INTERVAL_MIN", "")
	t.Setenv("LOG_LEVEL", "")

	cfg, err := Load()
	if err != nil {
		t.Fatalf("Load() returned error: %v", err)
	}

	if cfg.Domain != "mail.test" {
		t.Fatalf("Domain = %q, want %q", cfg.Domain, "mail.test")
	}
	if cfg.SMTPListenAddr != ":25" {
		t.Fatalf("SMTPListenAddr = %q, want %q", cfg.SMTPListenAddr, ":25")
	}
	if cfg.HTTPListenAddr != ":8080" {
		t.Fatalf("HTTPListenAddr = %q, want %q", cfg.HTTPListenAddr, ":8080")
	}
	if cfg.DatabasePath != "./data/mail.db" {
		t.Fatalf("DatabasePath = %q, want %q", cfg.DatabasePath, "./data/mail.db")
	}
	if cfg.InboxTTL != 24*time.Hour {
		t.Fatalf("InboxTTL = %v, want %v", cfg.InboxTTL, 24*time.Hour)
	}
	if cfg.MaxEmailSize != 10*1024*1024 {
		t.Fatalf("MaxEmailSize = %d, want %d", cfg.MaxEmailSize, 10*1024*1024)
	}
	if cfg.MaxAttachmentSize != 5*1024*1024 {
		t.Fatalf("MaxAttachmentSize = %d, want %d", cfg.MaxAttachmentSize, 5*1024*1024)
	}
	if cfg.CleanupInterval != 15*time.Minute {
		t.Fatalf("CleanupInterval = %v, want %v", cfg.CleanupInterval, 15*time.Minute)
	}
	if cfg.LogLevel != "info" {
		t.Fatalf("LogLevel = %q, want %q", cfg.LogLevel, "info")
	}
}

func TestLoadFallsBackForInvalidIntegers(t *testing.T) {
	t.Setenv("DOMAIN", "mail.test")
	t.Setenv("INBOX_TTL_HOURS", "invalid")
	t.Setenv("MAX_EMAIL_SIZE_MB", "invalid")
	t.Setenv("MAX_ATTACHMENT_MB", "invalid")
	t.Setenv("CLEANUP_INTERVAL_MIN", "invalid")

	cfg, err := Load()
	if err != nil {
		t.Fatalf("Load() returned error: %v", err)
	}

	if cfg.InboxTTL != 24*time.Hour {
		t.Fatalf("InboxTTL = %v, want %v", cfg.InboxTTL, 24*time.Hour)
	}
	if cfg.MaxEmailSize != 10*1024*1024 {
		t.Fatalf("MaxEmailSize = %d, want %d", cfg.MaxEmailSize, 10*1024*1024)
	}
	if cfg.MaxAttachmentSize != 5*1024*1024 {
		t.Fatalf("MaxAttachmentSize = %d, want %d", cfg.MaxAttachmentSize, 5*1024*1024)
	}
	if cfg.CleanupInterval != 15*time.Minute {
		t.Fatalf("CleanupInterval = %v, want %v", cfg.CleanupInterval, 15*time.Minute)
	}
}

func TestLogLevel(t *testing.T) {
	tests := []struct {
		name  string
		input string
		want  slog.Level
	}{
		{name: "debug", input: "debug", want: slog.LevelDebug},
		{name: "warn", input: "warn", want: slog.LevelWarn},
		{name: "error", input: "error", want: slog.LevelError},
		{name: "unknown", input: "other", want: slog.LevelInfo},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := LogLevel(tt.input); got != tt.want {
				t.Fatalf("LogLevel(%q) = %v, want %v", tt.input, got, tt.want)
			}
		})
	}
}
