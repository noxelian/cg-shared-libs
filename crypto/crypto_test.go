package crypto

import (
	"crypto/rand"
	"encoding/hex"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func generateTestKey(t *testing.T) []byte {
	t.Helper()
	key := make([]byte, 32)
	_, err := rand.Read(key)
	require.NoError(t, err)
	return key
}

func TestEncryptDecrypt(t *testing.T) {
	key := generateTestKey(t)
	enc, err := NewEncryptor(key)
	require.NoError(t, err)

	tests := []struct {
		name      string
		plaintext string
	}{
		{name: "simple string", plaintext: "hello world"},
		{name: "email", plaintext: "user@example.com"},
		{name: "phone number", plaintext: "+7 999 123-45-67"},
		{name: "unicode text", plaintext: "Привет мир 你好世界"},
		{name: "empty string", plaintext: ""},
		{name: "long text", plaintext: strings.Repeat("a", 10000)},
		{name: "special characters", plaintext: "!@#$%^&*()_+-=[]{}|;':\",./<>?"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			encrypted, err := enc.Encrypt(tt.plaintext)
			require.NoError(t, err)

			if tt.plaintext != "" {
				assert.True(t, strings.HasPrefix(encrypted, encPrefix),
					"encrypted value should have enc: prefix")
				assert.NotEqual(t, tt.plaintext, encrypted,
					"encrypted value should differ from plaintext")
			}

			decrypted, err := enc.Decrypt(encrypted)
			require.NoError(t, err)
			assert.Equal(t, tt.plaintext, decrypted)
		})
	}
}

func TestEncryptProducesDifferentCiphertext(t *testing.T) {
	key := generateTestKey(t)
	enc, err := NewEncryptor(key)
	require.NoError(t, err)

	plaintext := "same input every time"
	seen := make(map[string]bool)

	for i := 0; i < 100; i++ {
		encrypted, err := enc.Encrypt(plaintext)
		require.NoError(t, err)

		assert.False(t, seen[encrypted],
			"encryption should produce unique ciphertexts due to random nonces")
		seen[encrypted] = true
	}
}

func TestDecryptInvalidData(t *testing.T) {
	key := generateTestKey(t)
	enc, err := NewEncryptor(key)
	require.NoError(t, err)

	tests := []struct {
		name       string
		ciphertext string
		wantErr    error
	}{
		{
			name:       "invalid base64",
			ciphertext: "enc:not-valid-base64!!!",
			wantErr:    ErrInvalidCiphertext,
		},
		{
			name:       "too short after decode",
			ciphertext: "enc:" + "AQID", // 3 bytes, less than nonce size
			wantErr:    ErrInvalidCiphertext,
		},
		{
			name:       "corrupted ciphertext",
			ciphertext: "enc:AQIDBAUHCA0ODxAREhMUFRYXGBkaGxwdHh8=",
			wantErr:    ErrDecryptionFailed,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := enc.Decrypt(tt.ciphertext)
			require.Error(t, err)
			assert.ErrorIs(t, err, tt.wantErr)
		})
	}
}

func TestDecryptPlaintextPassthrough(t *testing.T) {
	key := generateTestKey(t)
	enc, err := NewEncryptor(key)
	require.NoError(t, err)

	// Values without "enc:" prefix are treated as plaintext
	plaintext := "just a regular string"
	result, err := enc.Decrypt(plaintext)
	require.NoError(t, err)
	assert.Equal(t, plaintext, result)
}

func TestNoopEncryptor(t *testing.T) {
	enc := NewNoop()

	assert.False(t, enc.IsEnabled())

	t.Run("encrypt passthrough", func(t *testing.T) {
		input := "sensitive data"
		result, err := enc.Encrypt(input)
		require.NoError(t, err)
		assert.Equal(t, input, result, "noop encrypt should return input unchanged")
	})

	t.Run("decrypt passthrough", func(t *testing.T) {
		input := "enc:some-encrypted-looking-data"
		result, err := enc.Decrypt(input)
		require.NoError(t, err)
		assert.Equal(t, input, result, "noop decrypt should return input unchanged")
	})

	t.Run("nullable passthrough", func(t *testing.T) {
		input := "hello"
		result, err := enc.EncryptNullable(&input)
		require.NoError(t, err)
		require.NotNil(t, result)
		assert.Equal(t, input, *result)
	})
}

func TestNullableFields(t *testing.T) {
	key := generateTestKey(t)
	enc, err := NewEncryptor(key)
	require.NoError(t, err)

	t.Run("nil encrypt", func(t *testing.T) {
		result, err := enc.EncryptNullable(nil)
		require.NoError(t, err)
		assert.Nil(t, result)
	})

	t.Run("nil decrypt", func(t *testing.T) {
		result, err := enc.DecryptNullable(nil)
		require.NoError(t, err)
		assert.Nil(t, result)
	})

	t.Run("non-nil round-trip", func(t *testing.T) {
		input := "personal data"
		encrypted, err := enc.EncryptNullable(&input)
		require.NoError(t, err)
		require.NotNil(t, encrypted)
		assert.NotEqual(t, input, *encrypted)

		decrypted, err := enc.DecryptNullable(encrypted)
		require.NoError(t, err)
		require.NotNil(t, decrypted)
		assert.Equal(t, input, *decrypted)
	})
}

func TestNewEncryptorInvalidKey(t *testing.T) {
	tests := []struct {
		name    string
		keyLen  int
	}{
		{name: "too short", keyLen: 16},
		{name: "too long", keyLen: 64},
		{name: "empty", keyLen: 0},
		{name: "one byte short", keyLen: 31},
		{name: "one byte long", keyLen: 33},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			key := make([]byte, tt.keyLen)
			_, err := NewEncryptor(key)
			require.Error(t, err)
			assert.ErrorIs(t, err, ErrInvalidKeyLength)
		})
	}
}

func TestNewEncryptorFromConfig(t *testing.T) {
	key := generateTestKey(t)
	keyHex := hex.EncodeToString(key)

	t.Run("enabled with valid key", func(t *testing.T) {
		cfg := Config{Enabled: true, KeyHex: keyHex}
		enc, err := NewEncryptorFromConfig(cfg)
		require.NoError(t, err)
		assert.True(t, enc.IsEnabled())

		encrypted, err := enc.Encrypt("test")
		require.NoError(t, err)
		assert.True(t, strings.HasPrefix(encrypted, encPrefix))
	})

	t.Run("disabled returns noop", func(t *testing.T) {
		cfg := Config{Enabled: false, KeyHex: ""}
		enc, err := NewEncryptorFromConfig(cfg)
		require.NoError(t, err)
		assert.False(t, enc.IsEnabled())
	})

	t.Run("invalid hex key", func(t *testing.T) {
		cfg := Config{Enabled: true, KeyHex: "not-hex"}
		_, err := NewEncryptorFromConfig(cfg)
		require.Error(t, err)
		assert.Contains(t, err.Error(), "invalid hex key")
	})

	t.Run("wrong length hex key", func(t *testing.T) {
		cfg := Config{Enabled: true, KeyHex: "aabbccdd"}
		_, err := NewEncryptorFromConfig(cfg)
		require.Error(t, err)
		assert.ErrorIs(t, err, ErrInvalidKeyLength)
	})
}

func TestCrossKeyDecryptionFails(t *testing.T) {
	key1 := generateTestKey(t)
	key2 := generateTestKey(t)

	enc1, err := NewEncryptor(key1)
	require.NoError(t, err)

	enc2, err := NewEncryptor(key2)
	require.NoError(t, err)

	encrypted, err := enc1.Encrypt("secret")
	require.NoError(t, err)

	_, err = enc2.Decrypt(encrypted)
	require.Error(t, err)
	assert.ErrorIs(t, err, ErrDecryptionFailed)
}
