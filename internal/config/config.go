package config

import (
	"fmt"
	"log/slog"
	"os"
	"strconv"
	"strings"
	"time"
)

type Config struct {
	Domain            string
	SMTPListenAddr    string
	HTTPListenAddr    string
	DatabasePath      string
	InboxTTL          time.Duration
	MaxEmailSize      int64
	MaxAttachmentSize int64
	CleanupInterval   time.Duration
	LogLevel          string
}

func Load() (*Config, error) {
	cfg := &Config{
		Domain:            strings.TrimSpace(os.Getenv("DOMAIN")),
		SMTPListenAddr:    getEnvOrDefault("SMTP_LISTEN_ADDR", ":25"),
		HTTPListenAddr:    getEnvOrDefault("HTTP_LISTEN_ADDR", ":8080"),
		DatabasePath:      getEnvOrDefault("DATABASE_PATH", "./data/mail.db"),
		InboxTTL:          time.Duration(getEnvIntOrDefault("INBOX_TTL_HOURS", 24)) * time.Hour,
		MaxEmailSize:      int64(getEnvIntOrDefault("MAX_EMAIL_SIZE_MB", 10)) * 1024 * 1024,
		MaxAttachmentSize: int64(getEnvIntOrDefault("MAX_ATTACHMENT_MB", 5)) * 1024 * 1024,
		CleanupInterval:   time.Duration(getEnvIntOrDefault("CLEANUP_INTERVAL_MIN", 15)) * time.Minute,
		LogLevel:          getEnvOrDefault("LOG_LEVEL", "info"),
	}

	if cfg.Domain == "" {
		return nil, fmt.Errorf("DOMAIN environment variable is required")
	}

	return cfg, nil
}

func LogLevel(level string) slog.Level {
	switch strings.ToLower(strings.TrimSpace(level)) {
	case "debug":
		return slog.LevelDebug
	case "warn":
		return slog.LevelWarn
	case "error":
		return slog.LevelError
	default:
		return slog.LevelInfo
	}
}

func getEnvOrDefault(key, fallback string) string {
	value := strings.TrimSpace(os.Getenv(key))
	if value == "" {
		return fallback
	}
	return value
}

func getEnvIntOrDefault(key string, fallback int) int {
	value := strings.TrimSpace(os.Getenv(key))
	if value == "" {
		return fallback
	}

	parsed, err := strconv.Atoi(value)
	if err != nil {
		return fallback
	}

	return parsed
}
