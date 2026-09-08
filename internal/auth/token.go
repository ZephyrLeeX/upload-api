package auth

import (
	"crypto/sha256"
	"crypto/subtle"
	"strings"
)

// ValidateBearer validates a strict Bearer authorization header in constant time.
func ValidateBearer(header, expected string) bool {
	const prefix = "Bearer "
	if expected == "" || !strings.HasPrefix(header, prefix) {
		return false
	}
	token := strings.TrimPrefix(header, prefix)
	if token == "" {
		return false
	}
	actualHash := sha256.Sum256([]byte(token))
	expectedHash := sha256.Sum256([]byte(expected))
	return subtle.ConstantTimeCompare(actualHash[:], expectedHash[:]) == 1
}
