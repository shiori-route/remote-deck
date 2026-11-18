// Package auth verifies the single local operator's bearer token.
package auth

import (
	"crypto/sha256"
	"crypto/subtle"
	"errors"
	"strings"
)

// Verifier keeps only a token digest in memory. Its zero value denies access.
type Verifier struct {
	digest     [sha256.Size]byte
	configured bool
}

// New accepts a 256-bit token encoded as 64 lowercase hexadecimal characters.
func New(token string) (Verifier, error) {
	if !validToken(token) {
		return Verifier{}, errors.New("invalid access token format")
	}
	return Verifier{digest: sha256.Sum256([]byte(token)), configured: true}, nil
}

// Allows accepts exactly one Authorization field. Cookies and query strings
// are deliberately not credential sources. HTTP auth schemes ignore case.
func (v Verifier) Allows(headers []string) bool {
	if !v.configured || len(headers) != 1 {
		return false
	}
	scheme, token, ok := strings.Cut(headers[0], " ")
	if !ok || !strings.EqualFold(scheme, "Bearer") {
		return false
	}
	token = strings.TrimLeft(token, " ")
	if !validToken(token) {
		return false
	}
	digest := sha256.Sum256([]byte(token))
	return subtle.ConstantTimeCompare(digest[:], v.digest[:]) == 1
}

func validToken(token string) bool {
	if len(token) != 64 {
		return false
	}
	for i := range token {
		c := token[i]
		if !(c >= '0' && c <= '9' || c >= 'a' && c <= 'f') {
			return false
		}
	}
	return true
}
