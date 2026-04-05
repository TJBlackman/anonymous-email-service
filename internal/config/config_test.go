package config

import (
	"log/slog"
	"strings"
	"testing"
	"time"
)

func TestLoadRequiresDomain(t *testing.T) {
	t.Setenv("DOMAIN", "")

	_, err := Load()
	if err == nil {
		t.Fatal("expected error when DOMAIN is missing")
	}
	if !strings.Contains(err.Error(), "DOMAIN is required") {
		t.Fatalf("error = %q, want DOMAIN requirement", err)
	}
}

func TestLoadRejectsBlankDomain(t *testing.T) {
	t.Setenv("DOMAIN", "   ")

	_, err := Load()
	if err == nil {
		t.Fatal("expected error when DOMAIN is blank")
	}
	if !strings.Contains(err.Error(), "DOMAIN is required") {
		t.Fatalf("error = %q, want DOMAIN requirement", err)
	}
}

func TestLoadAppliesDefaults(t *testing.T) {
	t.Setenv("DOMAIN", "Mail.Test")

	cfg, err := Load()
	if err != nil {
		t.Fatalf("Load() returned error: %v", err)
	}

	if cfg.Domain != "mail.test" {
		t.Fatalf("Domain = %q, want %q", cfg.Domain, "mail.test")
	}
	if cfg.SMTPListenAddr != defaultSMTPListenAddr {
		t.Fatalf("SMTPListenAddr = %q, want %q", cfg.SMTPListenAddr, defaultSMTPListenAddr)
	}
	if cfg.HTTPListenAddr != defaultHTTPListenAddr {
		t.Fatalf("HTTPListenAddr = %q, want %q", cfg.HTTPListenAddr, defaultHTTPListenAddr)
	}
	if cfg.DatabasePath != defaultDatabasePath {
		t.Fatalf("DatabasePath = %q, want %q", cfg.DatabasePath, defaultDatabasePath)
	}
	if cfg.InboxTTL != time.Duration(defaultInboxTTLHours)*time.Hour {
		t.Fatalf("InboxTTL = %v, want %v", cfg.InboxTTL, time.Duration(defaultInboxTTLHours)*time.Hour)
	}
	if cfg.MaxEmailSize != int64(defaultMaxEmailSizeMB)*1024*1024 {
		t.Fatalf("MaxEmailSize = %d, want %d", cfg.MaxEmailSize, int64(defaultMaxEmailSizeMB)*1024*1024)
	}
	if cfg.MaxAttachmentSize != int64(defaultMaxAttachSizeMB)*1024*1024 {
		t.Fatalf("MaxAttachmentSize = %d, want %d", cfg.MaxAttachmentSize, int64(defaultMaxAttachSizeMB)*1024*1024)
	}
	if cfg.CleanupInterval != time.Duration(defaultCleanupMins)*time.Minute {
		t.Fatalf("CleanupInterval = %v, want %v", cfg.CleanupInterval, time.Duration(defaultCleanupMins)*time.Minute)
	}
	if cfg.LogLevel != defaultLogLevel {
		t.Fatalf("LogLevel = %q, want %q", cfg.LogLevel, defaultLogLevel)
	}
}

func TestLoadTrimsAndNormalizesValidOverrides(t *testing.T) {
	t.Setenv("DOMAIN", " ExAmPlE.TEST ")
	t.Setenv("SMTP_LISTEN_ADDR", " 127.0.0.1:2525 ")
	t.Setenv("HTTP_LISTEN_ADDR", " [::1]:8081 ")
	t.Setenv("DATABASE_PATH", " ./data/custom.db ")
	t.Setenv("INBOX_TTL_HOURS", " 48 ")
	t.Setenv("MAX_EMAIL_SIZE_MB", " 20 ")
	t.Setenv("MAX_ATTACHMENT_MB", " 4 ")
	t.Setenv("CLEANUP_INTERVAL_MIN", " 5 ")
	t.Setenv("LOG_LEVEL", " WARN ")

	cfg, err := Load()
	if err != nil {
		t.Fatalf("Load() returned error: %v", err)
	}

	if cfg.Domain != "example.test" {
		t.Fatalf("Domain = %q, want %q", cfg.Domain, "example.test")
	}
	if cfg.SMTPListenAddr != "127.0.0.1:2525" {
		t.Fatalf("SMTPListenAddr = %q, want %q", cfg.SMTPListenAddr, "127.0.0.1:2525")
	}
	if cfg.HTTPListenAddr != "[::1]:8081" {
		t.Fatalf("HTTPListenAddr = %q, want %q", cfg.HTTPListenAddr, "[::1]:8081")
	}
	if cfg.DatabasePath != "./data/custom.db" {
		t.Fatalf("DatabasePath = %q, want %q", cfg.DatabasePath, "./data/custom.db")
	}
	if cfg.InboxTTL != 48*time.Hour {
		t.Fatalf("InboxTTL = %v, want %v", cfg.InboxTTL, 48*time.Hour)
	}
	if cfg.MaxEmailSize != 20*1024*1024 {
		t.Fatalf("MaxEmailSize = %d, want %d", cfg.MaxEmailSize, 20*1024*1024)
	}
	if cfg.MaxAttachmentSize != 4*1024*1024 {
		t.Fatalf("MaxAttachmentSize = %d, want %d", cfg.MaxAttachmentSize, 4*1024*1024)
	}
	if cfg.CleanupInterval != 5*time.Minute {
		t.Fatalf("CleanupInterval = %v, want %v", cfg.CleanupInterval, 5*time.Minute)
	}
	if cfg.LogLevel != "warn" {
		t.Fatalf("LogLevel = %q, want %q", cfg.LogLevel, "warn")
	}
}

func TestLoadRejectsInvalidDomain(t *testing.T) {
	tests := []struct {
		name  string
		value string
		want  string
	}{
		{name: "contains at", value: "bad@domain", want: "DOMAIN must not contain @"},
		{name: "internal whitespace", value: "bad domain", want: "DOMAIN must not contain whitespace"},
		{name: "leading dot", value: ".example.test", want: "DOMAIN must not start or end with '.' or contain consecutive dots"},
		{name: "trailing dot", value: "example.test.", want: "DOMAIN must not start or end with '.' or contain consecutive dots"},
		{name: "consecutive dots", value: "example..test", want: "DOMAIN must not start or end with '.' or contain consecutive dots"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Setenv("DOMAIN", tt.value)

			_, err := Load()
			if err == nil {
				t.Fatal("expected error")
			}
			if !strings.Contains(err.Error(), tt.want) {
				t.Fatalf("error = %q, want substring %q", err, tt.want)
			}
		})
	}
}

func TestLoadRejectsInvalidListenAddress(t *testing.T) {
	tests := []struct {
		name string
		key  string
		want string
	}{
		{name: "smtp missing port", key: "SMTP_LISTEN_ADDR", want: "SMTP_LISTEN_ADDR must be a valid host:port address"},
		{name: "http missing port", key: "HTTP_LISTEN_ADDR", want: "HTTP_LISTEN_ADDR must be a valid host:port address"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Setenv("DOMAIN", "example.test")
			t.Setenv(tt.key, "localhost")

			_, err := Load()
			if err == nil {
				t.Fatal("expected error")
			}
			if !strings.Contains(err.Error(), tt.want) {
				t.Fatalf("error = %q, want substring %q", err, tt.want)
			}
		})
	}
}

func TestLoadRejectsBlankDatabasePath(t *testing.T) {
	t.Setenv("DOMAIN", "example.test")
	t.Setenv("DATABASE_PATH", "   ")

	_, err := Load()
	if err == nil {
		t.Fatal("expected error")
	}
	if !strings.Contains(err.Error(), "DATABASE_PATH must not be blank") {
		t.Fatalf("error = %q, want database path validation", err)
	}
}

func TestLoadRejectsInvalidPositiveIntegers(t *testing.T) {
	tests := []struct {
		name  string
		key   string
		value string
	}{
		{name: "ttl non integer", key: "INBOX_TTL_HOURS", value: "invalid"},
		{name: "email size non integer", key: "MAX_EMAIL_SIZE_MB", value: "invalid"},
		{name: "attachment size non integer", key: "MAX_ATTACHMENT_MB", value: "invalid"},
		{name: "cleanup interval non integer", key: "CLEANUP_INTERVAL_MIN", value: "invalid"},
		{name: "ttl zero", key: "INBOX_TTL_HOURS", value: "0"},
		{name: "email size negative", key: "MAX_EMAIL_SIZE_MB", value: "-1"},
		{name: "attachment size zero", key: "MAX_ATTACHMENT_MB", value: "0"},
		{name: "cleanup interval negative", key: "CLEANUP_INTERVAL_MIN", value: "-5"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Setenv("DOMAIN", "example.test")
			t.Setenv(tt.key, tt.value)

			_, err := Load()
			if err == nil {
				t.Fatal("expected error")
			}
			if !strings.Contains(err.Error(), tt.key+" must be a positive integer") {
				t.Fatalf("error = %q, want positive integer validation for %s", err, tt.key)
			}
		})
	}
}

func TestLoadRejectsAttachmentLargerThanEmailLimit(t *testing.T) {
	t.Setenv("DOMAIN", "example.test")
	t.Setenv("MAX_EMAIL_SIZE_MB", "5")
	t.Setenv("MAX_ATTACHMENT_MB", "6")

	_, err := Load()
	if err == nil {
		t.Fatal("expected error")
	}
	if !strings.Contains(err.Error(), "MAX_ATTACHMENT_MB cannot exceed MAX_EMAIL_SIZE_MB") {
		t.Fatalf("error = %q, want attachment size comparison failure", err)
	}
}

func TestLoadRejectsUnknownLogLevel(t *testing.T) {
	t.Setenv("DOMAIN", "example.test")
	t.Setenv("LOG_LEVEL", "trace")

	_, err := Load()
	if err == nil {
		t.Fatal("expected error")
	}
	if !strings.Contains(err.Error(), "LOG_LEVEL must be one of: debug, info, warn, error") {
		t.Fatalf("error = %q, want log level validation", err)
	}
}

func TestLoadAggregatesValidationErrors(t *testing.T) {
	t.Setenv("DOMAIN", "bad@domain")
	t.Setenv("SMTP_LISTEN_ADDR", "localhost")
	t.Setenv("MAX_EMAIL_SIZE_MB", "1")
	t.Setenv("MAX_ATTACHMENT_MB", "2")
	t.Setenv("LOG_LEVEL", "trace")

	_, err := Load()
	if err == nil {
		t.Fatal("expected error")
	}

	for _, want := range []string{
		"DOMAIN must not contain @",
		"SMTP_LISTEN_ADDR must be a valid host:port address",
		"MAX_ATTACHMENT_MB cannot exceed MAX_EMAIL_SIZE_MB",
		"LOG_LEVEL must be one of: debug, info, warn, error",
	} {
		if !strings.Contains(err.Error(), want) {
			t.Fatalf("error = %q, want substring %q", err, want)
		}
	}
}

func TestLogLevel(t *testing.T) {
	tests := []struct {
		name  string
		input string
		want  slog.Level
	}{
		{name: "debug", input: "debug", want: slog.LevelDebug},
		{name: "info", input: " INFO ", want: slog.LevelInfo},
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
