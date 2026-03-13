package crypto

import (
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"strings"
)

const (
	// encPrefix marks a value as encrypted so we can distinguish
	// encrypted ciphertext from plaintext during gradual migration.
	encPrefix = "enc:"

	// keyLen is the required AES-256 key length in bytes.
	keyLen = 32

	// nonceLen is the standard GCM nonce length in bytes.
	nonceLen = 12
)

var (
	ErrInvalidKeyLength = errors.New("crypto: key must be exactly 32 bytes for AES-256")
	ErrDecryptionFailed = errors.New("crypto: decryption failed")
	ErrInvalidCiphertext = errors.New("crypto: invalid ciphertext format")
)

// Config holds encryption settings loaded from YAML / environment variables.
type Config struct {
	Enabled bool   `yaml:"enabled" env:"ENCRYPTION_ENABLED"`
	KeyHex  string `yaml:"key_hex" env:"ENCRYPTION_KEY"` // 64 hex chars = 32 bytes
}

// Encryptor provides AES-256-GCM field-level encryption for personal data.
// It is safe for concurrent use: GCM is thread-safe when each call
// uses a unique nonce (which we generate via crypto/rand).
type Encryptor struct {
	gcm     cipher.AEAD
	hashKey []byte // derived key for HMAC-SHA-256 lookups
	enabled bool
}

// NewEncryptor creates an Encryptor from a raw 32-byte key.
func NewEncryptor(key []byte) (*Encryptor, error) {
	if len(key) != keyLen {
		return nil, ErrInvalidKeyLength
	}

	block, err := aes.NewCipher(key)
	if err != nil {
		return nil, fmt.Errorf("crypto: failed to create AES cipher: %w", err)
	}

	gcm, err := cipher.NewGCM(block)
	if err != nil {
		return nil, fmt.Errorf("crypto: failed to create GCM: %w", err)
	}

	// Derive a separate HMAC key from the encryption key so we never
	// reuse the same key material for two different cryptographic purposes.
	// hashKey = SHA-256("hmac-key:" + hex(encryptionKey))
	hashInput := "hmac-key:" + hex.EncodeToString(key)
	derived := sha256.Sum256([]byte(hashInput))

	return &Encryptor{
		gcm:     gcm,
		hashKey: derived[:],
		enabled: true,
	}, nil
}

// NewEncryptorFromConfig creates an Encryptor from a Config struct.
// If encryption is disabled, a no-op Encryptor is returned.
func NewEncryptorFromConfig(cfg Config) (*Encryptor, error) {
	if !cfg.Enabled {
		return NewNoop(), nil
	}

	key, err := hex.DecodeString(cfg.KeyHex)
	if err != nil {
		return nil, fmt.Errorf("crypto: invalid hex key: %w", err)
	}

	return NewEncryptor(key)
}

// NewNoop returns a no-op Encryptor that passes values through unchanged.
func NewNoop() *Encryptor {
	return &Encryptor{
		gcm:     nil,
		enabled: false,
	}
}

// IsEnabled returns true if encryption is active.
func (e *Encryptor) IsEnabled() bool {
	return e.enabled
}

// Encrypt encrypts a plaintext string using AES-256-GCM.
// The returned value has the format: "enc:" + base64(nonce + ciphertext).
// If encryption is disabled, the input is returned as-is.
func (e *Encryptor) Encrypt(plaintext string) (string, error) {
	if !e.enabled {
		return plaintext, nil
	}

	if plaintext == "" {
		return plaintext, nil
	}

	nonce := make([]byte, nonceLen)
	if _, err := io.ReadFull(rand.Reader, nonce); err != nil {
		return "", fmt.Errorf("crypto: failed to generate nonce: %w", err)
	}

	ciphertext := e.gcm.Seal(nonce, nonce, []byte(plaintext), nil)
	encoded := base64.StdEncoding.EncodeToString(ciphertext)

	return encPrefix + encoded, nil
}

// Decrypt decrypts an encrypted string produced by Encrypt.
// If the value does not start with "enc:", it is treated as plaintext
// and returned as-is (backward-compatible with unencrypted data).
// If encryption is disabled, the input is returned as-is.
func (e *Encryptor) Decrypt(ciphertext string) (string, error) {
	if !e.enabled {
		return ciphertext, nil
	}

	if ciphertext == "" {
		return ciphertext, nil
	}

	if !strings.HasPrefix(ciphertext, encPrefix) {
		return ciphertext, nil
	}

	encoded := strings.TrimPrefix(ciphertext, encPrefix)

	data, err := base64.StdEncoding.DecodeString(encoded)
	if err != nil {
		return "", fmt.Errorf("%w: base64 decode failed: %v", ErrInvalidCiphertext, err)
	}

	if len(data) < nonceLen {
		return "", fmt.Errorf("%w: ciphertext too short", ErrInvalidCiphertext)
	}

	nonce := data[:nonceLen]
	sealed := data[nonceLen:]

	plaintext, err := e.gcm.Open(nil, nonce, sealed, nil)
	if err != nil {
		return "", fmt.Errorf("%w: %v", ErrDecryptionFailed, err)
	}

	return string(plaintext), nil
}

// EncryptNullable encrypts a nullable string pointer.
// Returns nil if the input is nil.
func (e *Encryptor) EncryptNullable(s *string) (*string, error) {
	if s == nil {
		return nil, nil
	}

	encrypted, err := e.Encrypt(*s)
	if err != nil {
		return nil, err
	}

	return &encrypted, nil
}

// DecryptNullable decrypts a nullable string pointer.
// Returns nil if the input is nil.
func (e *Encryptor) DecryptNullable(s *string) (*string, error) {
	if s == nil {
		return nil, nil
	}

	decrypted, err := e.Decrypt(*s)
	if err != nil {
		return nil, err
	}

	return &decrypted, nil
}
