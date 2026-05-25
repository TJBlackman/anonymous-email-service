// Package domainutil provides shared validation and normalization for email
// domains used by both startup configuration and the admin domain management UI.
package domainutil

import (
	"fmt"
	"strings"
	"unicode"
)

// Validate trims and lowercases the input, enforces the domain rules, and
// returns the normalized domain. The error messages are intentionally generic
// (no field name) so callers can wrap them with their own context, e.g.
// fmt.Errorf("DOMAIN: %w", err).
func Validate(raw string) (string, error) {
	domain := strings.ToLower(strings.TrimSpace(raw))
	switch {
	case domain == "":
		return "", fmt.Errorf("domain is required")
	case strings.Contains(domain, "@"):
		return "", fmt.Errorf("domain must not contain @")
	case strings.IndexFunc(domain, unicode.IsSpace) >= 0:
		return "", fmt.Errorf("domain must not contain whitespace")
	case strings.HasPrefix(domain, ".") || strings.HasSuffix(domain, ".") || strings.Contains(domain, ".."):
		return "", fmt.Errorf("domain must not start or end with '.' or contain consecutive dots")
	}

	return domain, nil
}
