package config

import (
	"errors"
	"fmt"
	"log/slog"
	"net"
	"os"
	"strconv"
	"strings"
	"time"
	"unicode"
)

type Config struct {
	Domain                    string
	SMTPListenAddr            string
	HTTPListenAddr            string
	DatabasePath              string
	InboxTTL                  time.Duration
	MaxEmailSize              int64
	MaxAttachmentSize         int64
	CleanupInterval           time.Duration
	CookieSecure              bool
	InboxCreateLimitPerHour   int
	SMTPConnectionLimitPerMin int
	LogLevel                  string
}

const (
	defaultSMTPListenAddr            = ":25"
	defaultHTTPListenAddr            = ":8080"
	defaultDatabasePath              = "./data/mail.db"
	defaultInboxTTLHours             = 24
	defaultMaxEmailSizeMB            = 10
	defaultMaxAttachSizeMB           = 5
	defaultCleanupMins               = 15
	defaultCookieSecure              = false
	defaultInboxCreateLimitPerHour   = 10
	defaultSMTPConnectionLimitPerMin = 30
	defaultLogLevel                  = "info"
)

var allowedLogLevels = map[string]struct{}{
	"debug": {},
	"info":  {},
	"warn":  {},
	"error": {},
}

func Load() (*Config, error) {
	domain, domainErr := loadDomain()
	smtpListenAddr, smtpErr := loadListenAddr("SMTP_LISTEN_ADDR", defaultSMTPListenAddr)
	httpListenAddr, httpErr := loadListenAddr("HTTP_LISTEN_ADDR", defaultHTTPListenAddr)
	databasePath, databasePathErr := loadDatabasePath()
	inboxTTLHours, inboxTTLErr := loadPositiveInt("INBOX_TTL_HOURS", defaultInboxTTLHours)
	maxEmailSizeMB, maxEmailSizeErr := loadPositiveInt("MAX_EMAIL_SIZE_MB", defaultMaxEmailSizeMB)
	maxAttachmentSizeMB, maxAttachmentSizeErr := loadPositiveInt("MAX_ATTACHMENT_MB", defaultMaxAttachSizeMB)
	cleanupIntervalMins, cleanupIntervalErr := loadPositiveInt("CLEANUP_INTERVAL_MIN", defaultCleanupMins)
	cookieSecure, cookieSecureErr := loadBool("COOKIE_SECURE", defaultCookieSecure)
	inboxCreateLimitPerHour, inboxCreateLimitErr := loadPositiveInt("INBOX_CREATE_LIMIT_PER_HOUR", defaultInboxCreateLimitPerHour)
	smtpConnectionLimitPerMin, smtpConnectionLimitErr := loadPositiveInt("SMTP_CONNECTION_LIMIT_PER_MIN", defaultSMTPConnectionLimitPerMin)
	logLevel, logLevelErr := loadLogLevel()

	errs := collectErrors(
		domainErr,
		smtpErr,
		httpErr,
		databasePathErr,
		inboxTTLErr,
		maxEmailSizeErr,
		maxAttachmentSizeErr,
		cleanupIntervalErr,
		cookieSecureErr,
		inboxCreateLimitErr,
		smtpConnectionLimitErr,
		logLevelErr,
	)

	if maxEmailSizeErr == nil && maxAttachmentSizeErr == nil && maxAttachmentSizeMB > maxEmailSizeMB {
		errs = append(errs, fmt.Errorf("MAX_ATTACHMENT_MB cannot exceed MAX_EMAIL_SIZE_MB"))
	}

	if len(errs) > 0 {
		return nil, errors.Join(errs...)
	}

	return &Config{
		Domain:                    domain,
		SMTPListenAddr:            smtpListenAddr,
		HTTPListenAddr:            httpListenAddr,
		DatabasePath:              databasePath,
		InboxTTL:                  time.Duration(inboxTTLHours) * time.Hour,
		MaxEmailSize:              int64(maxEmailSizeMB) * 1024 * 1024,
		MaxAttachmentSize:         int64(maxAttachmentSizeMB) * 1024 * 1024,
		CleanupInterval:           time.Duration(cleanupIntervalMins) * time.Minute,
		CookieSecure:              cookieSecure,
		InboxCreateLimitPerHour:   inboxCreateLimitPerHour,
		SMTPConnectionLimitPerMin: smtpConnectionLimitPerMin,
		LogLevel:                  logLevel,
	}, nil
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

func loadDomain() (string, error) {
	domain := strings.ToLower(strings.TrimSpace(os.Getenv("DOMAIN")))
	switch {
	case domain == "":
		return "", fmt.Errorf("DOMAIN is required")
	case strings.Contains(domain, "@"):
		return "", fmt.Errorf("DOMAIN must not contain @")
	case strings.IndexFunc(domain, unicode.IsSpace) >= 0:
		return "", fmt.Errorf("DOMAIN must not contain whitespace")
	case strings.HasPrefix(domain, ".") || strings.HasSuffix(domain, ".") || strings.Contains(domain, ".."):
		return "", fmt.Errorf("DOMAIN must not start or end with '.' or contain consecutive dots")
	}

	return domain, nil
}

func loadListenAddr(key, fallback string) (string, error) {
	value, _ := loadTrimmedString(key, fallback, true)
	if _, port, err := net.SplitHostPort(value); err != nil || strings.TrimSpace(port) == "" {
		return "", fmt.Errorf("%s must be a valid host:port address", key)
	}
	return value, nil
}

func loadDatabasePath() (string, error) {
	value, usedDefault := loadTrimmedString("DATABASE_PATH", defaultDatabasePath, false)
	if !usedDefault && value == "" {
		return "", fmt.Errorf("DATABASE_PATH must not be blank")
	}
	return value, nil
}

func loadLogLevel() (string, error) {
	value, _ := loadTrimmedString("LOG_LEVEL", defaultLogLevel, true)
	value = strings.ToLower(value)
	if _, ok := allowedLogLevels[value]; !ok {
		return "", fmt.Errorf("LOG_LEVEL must be one of: debug, info, warn, error")
	}
	return value, nil
}

func loadPositiveInt(key string, fallback int) (int, error) {
	value, usedDefault := loadTrimmedString(key, strconv.Itoa(fallback), true)
	if !usedDefault && value == "" {
		return 0, fmt.Errorf("%s must be a positive integer", key)
	}

	parsed, err := strconv.Atoi(value)
	if err != nil || parsed <= 0 {
		return 0, fmt.Errorf("%s must be a positive integer", key)
	}

	return parsed, nil
}

func loadBool(key string, fallback bool) (bool, error) {
	value, usedDefault := loadTrimmedString(key, strconv.FormatBool(fallback), true)
	if !usedDefault && value == "" {
		return false, fmt.Errorf("%s must be a boolean", key)
	}

	parsed, err := strconv.ParseBool(strings.ToLower(value))
	if err != nil {
		return false, fmt.Errorf("%s must be a boolean", key)
	}

	return parsed, nil
}

func loadTrimmedString(key, fallback string, allowBlankAsDefault bool) (string, bool) {
	value, ok := os.LookupEnv(key)
	if !ok {
		return fallback, true
	}

	trimmed := strings.TrimSpace(value)
	if trimmed == "" {
		if allowBlankAsDefault {
			return fallback, true
		}
		return "", false
	}

	return trimmed, false
}

func collectErrors(errs ...error) []error {
	var collected []error
	for _, err := range errs {
		if err != nil {
			collected = append(collected, err)
		}
	}
	return collected
}
