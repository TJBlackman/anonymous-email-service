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

	"anonymous-email-service/internal/domainutil"
	"golang.org/x/crypto/bcrypt"
)

type Config struct {
	Domain                    string
	SMTPHostname              string
	SMTPListenAddr            string
	HTTPListenAddr            string
	DatabasePath              string
	InboxTTL                  time.Duration
	MaxEmailSize              int64
	MaxAttachmentSize         int64
	CleanupInterval           time.Duration
	CookieSecure              bool
	SessionTTL                time.Duration
	BcryptCost                int
	AdminUsername             string
	AdminPassword             string
	InboxCreateLimitPerHour   int
	SMTPConnectionLimitPerMin int
	LogLevel                  string
}

const (
	defaultSMTPHostname              = "localhost"
	defaultSMTPListenAddr            = ":25"
	defaultHTTPListenAddr            = ":8080"
	defaultDatabasePath              = "./data/mail.db"
	defaultInboxTTLDays              = 60
	defaultMaxEmailSizeMB            = 10
	defaultMaxAttachSizeMB           = 5
	defaultCleanupMins               = 15
	defaultCookieSecure              = false
	defaultSessionTTLDays            = 7
	defaultBcryptCost                = bcrypt.DefaultCost
	defaultInboxCreateLimitPerHour   = 2
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
	inboxTTLDays, inboxTTLErr := loadNonNegativeInt("INBOX_TTL_DAYS", defaultInboxTTLDays)
	maxEmailSizeMB, maxEmailSizeErr := loadPositiveInt("MAX_EMAIL_SIZE_MB", defaultMaxEmailSizeMB)
	maxAttachmentSizeMB, maxAttachmentSizeErr := loadPositiveInt("MAX_ATTACHMENT_MB", defaultMaxAttachSizeMB)
	cleanupIntervalMins, cleanupIntervalErr := loadPositiveInt("CLEANUP_INTERVAL_MIN", defaultCleanupMins)
	cookieSecure, cookieSecureErr := loadBool("COOKIE_SECURE", defaultCookieSecure)
	sessionTTLDays, sessionTTLErr := loadPositiveInt("SESSION_TTL_DAYS", defaultSessionTTLDays)
	bcryptCost, bcryptCostErr := loadBcryptCost()
	adminUsername, _ := loadTrimmedString("ADMIN_USERNAME", "", false)
	adminPassword := os.Getenv("ADMIN_PASSWORD")
	inboxCreateLimitPerHour, inboxCreateLimitErr := loadNonNegativeInt("INBOX_CREATE_LIMIT_PER_HOUR", defaultInboxCreateLimitPerHour)
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
		sessionTTLErr,
		bcryptCostErr,
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
		SMTPHostname:              resolveSMTPHostname(domain),
		SMTPListenAddr:            smtpListenAddr,
		HTTPListenAddr:            httpListenAddr,
		DatabasePath:              databasePath,
		InboxTTL:                  time.Duration(inboxTTLDays) * 24 * time.Hour,
		MaxEmailSize:              int64(maxEmailSizeMB) * 1024 * 1024,
		MaxAttachmentSize:         int64(maxAttachmentSizeMB) * 1024 * 1024,
		CleanupInterval:           time.Duration(cleanupIntervalMins) * time.Minute,
		CookieSecure:              cookieSecure,
		SessionTTL:                time.Duration(sessionTTLDays) * 24 * time.Hour,
		BcryptCost:                bcryptCost,
		AdminUsername:             adminUsername,
		AdminPassword:             adminPassword,
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

// loadDomain reads the optional DOMAIN bootstrap seed. When set, the value must
// pass the shared domain validation; when unset/blank it returns an empty seed,
// since domains are now managed at runtime via the admin UI.
func loadDomain() (string, error) {
	raw := strings.TrimSpace(os.Getenv("DOMAIN"))
	if raw == "" {
		return "", nil
	}

	domain, err := domainutil.Validate(raw)
	if err != nil {
		// domainutil messages start with "domain ..."; reformat with the env
		// var name so error output reads "DOMAIN must not contain @" etc.
		return "", fmt.Errorf("DOMAIN%s", strings.TrimPrefix(err.Error(), "domain"))
	}
	return domain, nil
}

// resolveSMTPHostname picks the EHLO banner hostname: SMTP_HOSTNAME if set,
// otherwise the DOMAIN seed if present, otherwise a localhost fallback. This is
// only the greeting string and is not used for recipient validation.
func resolveSMTPHostname(domain string) string {
	if value := strings.TrimSpace(os.Getenv("SMTP_HOSTNAME")); value != "" {
		return strings.ToLower(value)
	}
	if domain != "" {
		return domain
	}
	return defaultSMTPHostname
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

// loadBcryptCost reads BCRYPT_COST, defaulting to bcrypt.DefaultCost. The value
// must fall within bcrypt's supported cost range.
func loadBcryptCost() (int, error) {
	value, usedDefault := loadTrimmedString("BCRYPT_COST", strconv.Itoa(defaultBcryptCost), true)
	if !usedDefault && value == "" {
		return 0, fmt.Errorf("BCRYPT_COST must be an integer between %d and %d", bcrypt.MinCost, bcrypt.MaxCost)
	}

	parsed, err := strconv.Atoi(value)
	if err != nil || parsed < bcrypt.MinCost || parsed > bcrypt.MaxCost {
		return 0, fmt.Errorf("BCRYPT_COST must be an integer between %d and %d", bcrypt.MinCost, bcrypt.MaxCost)
	}

	return parsed, nil
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

// loadNonNegativeInt parses a setting where 0 is a meaningful "disabled"
// sentinel (e.g. INBOX_TTL_DAYS=0 means never expire, INBOX_CREATE_LIMIT_PER_HOUR=0
// means unlimited). Negative values and non-integers are still rejected.
func loadNonNegativeInt(key string, fallback int) (int, error) {
	value, usedDefault := loadTrimmedString(key, strconv.Itoa(fallback), true)
	if !usedDefault && value == "" {
		return 0, fmt.Errorf("%s must be a non-negative integer", key)
	}

	parsed, err := strconv.Atoi(value)
	if err != nil || parsed < 0 {
		return 0, fmt.Errorf("%s must be a non-negative integer", key)
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
