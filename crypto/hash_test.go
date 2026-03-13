package crypto

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestHashDeterministic(t *testing.T) {
	key := generateTestKey(t)
	enc, err := NewEncryptor(key)
	require.NoError(t, err)

	input := "+77771234567"

	h1 := enc.Hash(input)
	h2 := enc.Hash(input)

	assert.Equal(t, h1, h2, "same input must always produce the same hash")
}

func TestHashDifferentInputs(t *testing.T) {
	key := generateTestKey(t)
	enc, err := NewEncryptor(key)
	require.NoError(t, err)

	h1 := enc.Hash("+77771234567")
	h2 := enc.Hash("+77779999999")

	assert.NotEqual(t, h1, h2, "different inputs must produce different hashes")
}

func TestHashLength(t *testing.T) {
	key := generateTestKey(t)
	enc, err := NewEncryptor(key)
	require.NoError(t, err)

	h := enc.Hash("+77771234567")

	assert.Len(t, h, 64, "HMAC-SHA-256 hex output must be 64 characters")
}

func TestHashDifferentKeysProduceDifferentHashes(t *testing.T) {
	key1 := generateTestKey(t)
	key2 := generateTestKey(t)

	enc1, err := NewEncryptor(key1)
	require.NoError(t, err)

	enc2, err := NewEncryptor(key2)
	require.NoError(t, err)

	input := "+77771234567"

	assert.NotEqual(t, enc1.Hash(input), enc2.Hash(input),
		"different encryption keys must produce different hashes")
}

func TestHashNoopReturnsEmpty(t *testing.T) {
	enc := NewNoop()

	h := enc.Hash("+77771234567")

	assert.Equal(t, "", h, "noop encryptor must return empty string")
}

func TestHashNullableNilReturnsNil(t *testing.T) {
	key := generateTestKey(t)
	enc, err := NewEncryptor(key)
	require.NoError(t, err)

	result := enc.HashNullable(nil)

	assert.Nil(t, result, "HashNullable with nil input must return nil")
}

func TestHashNullableNonNil(t *testing.T) {
	key := generateTestKey(t)
	enc, err := NewEncryptor(key)
	require.NoError(t, err)

	input := "+77771234567"
	result := enc.HashNullable(&input)

	require.NotNil(t, result)
	assert.Len(t, *result, 64)
	assert.Equal(t, enc.Hash(input), *result,
		"HashNullable must return the same value as Hash")
}

func TestHashNoopNullableNonNil(t *testing.T) {
	enc := NewNoop()

	input := "+77771234567"
	result := enc.HashNullable(&input)

	require.NotNil(t, result)
	assert.Equal(t, "", *result, "noop HashNullable must return pointer to empty string")
}
