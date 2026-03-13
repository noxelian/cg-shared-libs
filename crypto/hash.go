package crypto

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
)

// Hash returns a deterministic HMAC-SHA-256 hex string for value.
// It is intended for phone_hash / email_hash columns that need exact-match
// lookups without exposing the plaintext value in the index.
//
// The HMAC key is derived from the encryption key at construction time
// (SHA-256("hmac-key:" + hex(encryptionKey))), ensuring the two keys are
// never the same material.
//
// If the Encryptor is a noop (encryption disabled), Hash returns an empty
// string so callers can detect the unconfigured state and avoid storing
// misleading unhashed values in hash columns.
func (e *Encryptor) Hash(value string) string {
	if !e.enabled {
		return ""
	}

	mac := hmac.New(sha256.New, e.hashKey)
	mac.Write([]byte(value))

	return hex.EncodeToString(mac.Sum(nil))
}

// HashNullable returns a deterministic hash for a nullable string pointer.
// Returns nil when the input is nil.
func (e *Encryptor) HashNullable(value *string) *string {
	if value == nil {
		return nil
	}

	h := e.Hash(*value)

	return &h
}
