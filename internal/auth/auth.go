// Package auth holds password hashing, credential generation, and session-token
// helpers shared across the HTTP layer. Keeping the crypto here keeps the web
// package focused on request handling.
package auth

import (
	"crypto/rand"
	"fmt"

	"github.com/google/uuid"
	"golang.org/x/crypto/bcrypt"
)

// GeneratedPasswordLength is the length of the one-time password shown to a user
// when they create a mailbox. It is well under bcrypt's 72-byte input limit.
const GeneratedPasswordLength = 32

// passwordAlphabet excludes visually ambiguous characters (0/O, 1/l/I) so a
// user copying the one-time password by hand is less likely to transcribe it
// wrong. All characters are single bytes, so the generated password is exactly
// GeneratedPasswordLength bytes.
const passwordAlphabet = "abcdefghijkmnpqrstuvwxyzABCDEFGHJKLMNPQRSTUVWXYZ23456789"

// GeneratePassword returns a cryptographically random GeneratedPasswordLength-character
// password drawn from passwordAlphabet.
func GeneratePassword() (string, error) {
	bytes := make([]byte, GeneratedPasswordLength)
	if _, err := rand.Read(bytes); err != nil {
		return "", fmt.Errorf("generate password: %w", err)
	}

	out := make([]byte, GeneratedPasswordLength)
	n := byte(len(passwordAlphabet))
	for i, b := range bytes {
		out[i] = passwordAlphabet[b%n]
	}
	return string(out), nil
}

// HashPassword returns the bcrypt hash of a plaintext password at the given cost.
func HashPassword(plain string, cost int) (string, error) {
	hash, err := bcrypt.GenerateFromPassword([]byte(plain), cost)
	if err != nil {
		return "", fmt.Errorf("hash password: %w", err)
	}
	return string(hash), nil
}

// VerifyPassword reports whether plain matches the bcrypt hash. The comparison
// is constant-time within bcrypt.
func VerifyPassword(hash, plain string) bool {
	return bcrypt.CompareHashAndPassword([]byte(hash), []byte(plain)) == nil
}

// NewSessionToken returns an opaque, unguessable session token.
func NewSessionToken() string {
	return uuid.NewString()
}
