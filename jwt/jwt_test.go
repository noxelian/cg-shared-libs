package jwt

import (
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestNewManager_Success(t *testing.T) {
	cfg := Config{
		SecretKey:       "test-secret-key-exactly-32-chars",
		AccessTokenTTL:  15 * time.Minute,
		RefreshTokenTTL: 720 * time.Hour,
		Issuer:          "test-issuer",
	}

	manager, err := NewManager(cfg)

	require.NoError(t, err)
	assert.NotNil(t, manager)
}

func TestNewManager_EmptySecretKey(t *testing.T) {
	cfg := Config{
		SecretKey: "",
	}

	manager, err := NewManager(cfg)

	assert.Error(t, err)
	assert.Nil(t, manager)
	assert.Contains(t, err.Error(), "secret key is required")
}

func TestGenerateTokenPair_Success(t *testing.T) {
	manager := createTestManager(t)

	pair, err := manager.GenerateTokenPair(123, "+77001234567", "device-001")

	require.NoError(t, err)
	assert.NotEmpty(t, pair.AccessToken)
	assert.NotEmpty(t, pair.RefreshToken)
	assert.True(t, pair.ExpiresAt.After(time.Now()))
}

func TestGenerateTokenPair_DifferentTokens(t *testing.T) {
	manager := createTestManager(t)

	pair, err := manager.GenerateTokenPair(123, "+77001234567", "device-001")

	require.NoError(t, err)
	// Access and refresh tokens should be different
	assert.NotEqual(t, pair.AccessToken, pair.RefreshToken)
}

func TestGenerateAccessToken_Success(t *testing.T) {
	manager := createTestManager(t)

	token, expiresAt, err := manager.GenerateAccessToken(456, "+77009876543", "device-002")

	require.NoError(t, err)
	assert.NotEmpty(t, token)
	assert.True(t, expiresAt.After(time.Now()))
	assert.True(t, expiresAt.Before(time.Now().Add(16*time.Minute))) // Within TTL
}

func TestParse_ValidToken(t *testing.T) {
	manager := createTestManager(t)

	// Generate token
	pair, err := manager.GenerateTokenPair(123, "+77001234567", "device-001")
	require.NoError(t, err)

	// Parse token
	claims, err := manager.Parse(pair.AccessToken)

	require.NoError(t, err)
	assert.Equal(t, int64(123), claims.UserID)
	assert.Equal(t, "+77001234567", claims.Phone)
	assert.Equal(t, "device-001", claims.DeviceID)
	assert.Equal(t, "test-issuer", claims.Issuer)
	assert.Equal(t, TokenTypeAccess, claims.TokenType)
}

func TestParse_ExpiredToken(t *testing.T) {
	// Create manager with very short TTL
	cfg := Config{
		SecretKey:       "test-secret-key-exactly-32-chars",
		AccessTokenTTL:  1 * time.Millisecond,
		RefreshTokenTTL: 1 * time.Millisecond,
		Issuer:          "test-issuer",
	}
	manager, err := NewManager(cfg)
	require.NoError(t, err)

	// Generate token
	pair, err := manager.GenerateTokenPair(123, "+77001234567", "device-001")
	require.NoError(t, err)

	// Wait for token to expire
	time.Sleep(10 * time.Millisecond)

	// Parse expired token
	_, err = manager.Parse(pair.AccessToken)

	assert.ErrorIs(t, err, ErrTokenExpired)
}

func TestParse_InvalidToken(t *testing.T) {
	manager := createTestManager(t)

	_, err := manager.Parse("invalid.token.here")

	assert.Error(t, err)
}

func TestParse_MalformedToken(t *testing.T) {
	manager := createTestManager(t)

	tests := []struct {
		name  string
		token string
	}{
		{"empty", ""},
		{"random_string", "random-string"},
		{"partial_jwt", "eyJhbGciOiJIUzI1NiIsInR5cCI6IkpXVCJ9"},
		{"two_parts", "eyJhbGciOiJIUzI1NiJ9.eyJ1c2VyX2lkIjoxMjN9"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := manager.Parse(tt.token)
			assert.Error(t, err)
		})
	}
}

func TestParse_WrongSignature(t *testing.T) {
	manager1 := createTestManager(t)

	// Create manager with different secret
	cfg := Config{
		SecretKey:       "different-secret-key-exactly-32c",
		AccessTokenTTL:  15 * time.Minute,
		RefreshTokenTTL: 720 * time.Hour,
		Issuer:          "test-issuer",
	}
	manager2, err := NewManager(cfg)
	require.NoError(t, err)

	// Generate token with manager1
	pair, err := manager1.GenerateTokenPair(123, "+77001234567", "device-001")
	require.NoError(t, err)

	// Try to parse with manager2 (different secret)
	_, err = manager2.Parse(pair.AccessToken)

	assert.Error(t, err)
}

func TestValidateAccessToken_Success(t *testing.T) {
	manager := createTestManager(t)

	pair, _ := manager.GenerateTokenPair(123, "+77001234567", "device-001")

	claims, err := manager.ValidateAccessToken(pair.AccessToken)

	require.NoError(t, err)
	assert.Equal(t, int64(123), claims.UserID)
}

func TestValidateRefreshToken_Success(t *testing.T) {
	manager := createTestManager(t)

	pair, _ := manager.GenerateTokenPair(123, "+77001234567", "device-001")

	claims, err := manager.ValidateRefreshToken(pair.RefreshToken)

	require.NoError(t, err)
	assert.Equal(t, int64(123), claims.UserID)
}

func TestRefresh_Success(t *testing.T) {
	manager := createTestManager(t)

	// Generate initial token pair
	originalPair, err := manager.GenerateTokenPair(123, "+77001234567", "device-001")
	require.NoError(t, err)

	// Wait a bit to ensure different IssuedAt time
	time.Sleep(10 * time.Millisecond)

	// Refresh
	newPair, err := manager.Refresh(originalPair.RefreshToken)

	require.NoError(t, err)
	assert.NotEmpty(t, newPair.AccessToken)
	assert.NotEmpty(t, newPair.RefreshToken)

	// Verify new tokens are valid
	claims, err := manager.Parse(newPair.AccessToken)
	require.NoError(t, err)
	assert.Equal(t, int64(123), claims.UserID)
	assert.Equal(t, "+77001234567", claims.Phone)
}

func TestRefresh_InvalidToken(t *testing.T) {
	manager := createTestManager(t)

	_, err := manager.Refresh("invalid-refresh-token")

	assert.Error(t, err)
}

func TestRefresh_ExpiredToken(t *testing.T) {
	// Create manager with very short refresh TTL
	cfg := Config{
		SecretKey:       "test-secret-key-exactly-32-chars",
		AccessTokenTTL:  1 * time.Millisecond,
		RefreshTokenTTL: 1 * time.Millisecond,
		Issuer:          "test-issuer",
	}
	manager, err := NewManager(cfg)
	require.NoError(t, err)

	// Generate token pair
	pair, err := manager.GenerateTokenPair(123, "+77001234567", "device-001")
	require.NoError(t, err)

	// Wait for tokens to expire
	time.Sleep(10 * time.Millisecond)

	// Try to refresh
	_, err = manager.Refresh(pair.RefreshToken)

	assert.ErrorIs(t, err, ErrTokenExpired)
}

// Table-driven tests

func TestGenerateTokenPair_TableDriven(t *testing.T) {
	tests := []struct {
		name     string
		userID   int64
		phone    string
		deviceID string
	}{
		{
			name:     "normal_user",
			userID:   123,
			phone:    "+77001234567",
			deviceID: "device-001",
		},
		{
			name:     "zero_user_id",
			userID:   0,
			phone:    "+77001234567",
			deviceID: "device-001",
		},
		{
			name:     "empty_phone",
			userID:   123,
			phone:    "",
			deviceID: "device-001",
		},
		{
			name:     "empty_device_id",
			userID:   123,
			phone:    "+77001234567",
			deviceID: "",
		},
		{
			name:     "large_user_id",
			userID:   9223372036854775807, // max int64
			phone:    "+77001234567",
			deviceID: "device-001",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			manager := createTestManager(t)

			pair, err := manager.GenerateTokenPair(tt.userID, tt.phone, tt.deviceID)

			require.NoError(t, err)
			assert.NotEmpty(t, pair.AccessToken)

			// Verify claims
			claims, err := manager.Parse(pair.AccessToken)
			require.NoError(t, err)
			assert.Equal(t, tt.userID, claims.UserID)
			assert.Equal(t, tt.phone, claims.Phone)
			assert.Equal(t, tt.deviceID, claims.DeviceID)
		})
	}
}

func TestTokenExpiration_TableDriven(t *testing.T) {
	tests := []struct {
		name            string
		accessTokenTTL  time.Duration
		refreshTokenTTL time.Duration
	}{
		{
			name:            "short_access_long_refresh",
			accessTokenTTL:  5 * time.Second,
			refreshTokenTTL: 1 * time.Hour,
		},
		{
			name:            "equal_ttls",
			accessTokenTTL:  15 * time.Minute,
			refreshTokenTTL: 15 * time.Minute,
		},
		{
			name:            "long_access_short_refresh",
			accessTokenTTL:  1 * time.Hour,
			refreshTokenTTL: 5 * time.Second,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			cfg := Config{
				SecretKey:       "test-secret-key-exactly-32-chars",
				AccessTokenTTL:  tt.accessTokenTTL,
				RefreshTokenTTL: tt.refreshTokenTTL,
				Issuer:          "test-issuer",
			}
			manager, err := NewManager(cfg)
			require.NoError(t, err)

			pair, err := manager.GenerateTokenPair(123, "+77001234567", "device-001")

			require.NoError(t, err)
			// ExpiresAt should match access token TTL
			expectedExpiry := time.Now().Add(tt.accessTokenTTL)
			assert.WithinDuration(t, expectedExpiry, pair.ExpiresAt, 1*time.Second)
		})
	}
}

// Edge cases

func TestParse_TokenWithUnexpectedSigningMethod(t *testing.T) {
	manager := createTestManager(t)

	// This is a token signed with RS256 instead of HS256 (crafted for testing)
	// In real scenarios, this would be detected by the signing method check
	invalidToken := "eyJhbGciOiJSUzI1NiIsInR5cCI6IkpXVCJ9.eyJ1c2VyX2lkIjoxMjN9.invalid"

	_, err := manager.Parse(invalidToken)

	assert.Error(t, err)
}

func TestConcurrentTokenGeneration(t *testing.T) {
	manager := createTestManager(t)
	done := make(chan bool)

	// Generate tokens concurrently
	for i := 0; i < 100; i++ {
		go func(userID int64) {
			pair, err := manager.GenerateTokenPair(userID, "+77001234567", "device")
			assert.NoError(t, err)
			assert.NotEmpty(t, pair.AccessToken)
			done <- true
		}(int64(i))
	}

	// Wait for all goroutines
	for i := 0; i < 100; i++ {
		<-done
	}
}

func TestConcurrentTokenParsing(t *testing.T) {
	manager := createTestManager(t)

	// Generate a token
	pair, err := manager.GenerateTokenPair(123, "+77001234567", "device-001")
	require.NoError(t, err)

	done := make(chan bool)

	// Parse token concurrently
	for i := 0; i < 100; i++ {
		go func() {
			claims, err := manager.Parse(pair.AccessToken)
			assert.NoError(t, err)
			assert.Equal(t, int64(123), claims.UserID)
			done <- true
		}()
	}

	// Wait for all goroutines
	for i := 0; i < 100; i++ {
		<-done
	}
}

// Token type tests

func TestTokenType_AccessTokenHasCorrectType(t *testing.T) {
	manager := createTestManager(t)

	pair, err := manager.GenerateTokenPair(123, "+77001234567", "device-001")
	require.NoError(t, err)

	claims, err := manager.Parse(pair.AccessToken)
	require.NoError(t, err)
	assert.Equal(t, TokenTypeAccess, claims.TokenType)
}

func TestTokenType_RefreshTokenHasCorrectType(t *testing.T) {
	manager := createTestManager(t)

	pair, err := manager.GenerateTokenPair(123, "+77001234567", "device-001")
	require.NoError(t, err)

	claims, err := manager.Parse(pair.RefreshToken)
	require.NoError(t, err)
	assert.Equal(t, TokenTypeRefresh, claims.TokenType)
}

func TestValidateAccessToken_RejectsRefreshToken(t *testing.T) {
	manager := createTestManager(t)

	pair, err := manager.GenerateTokenPair(123, "+77001234567", "device-001")
	require.NoError(t, err)

	// Try to validate refresh token as access token
	_, err = manager.ValidateAccessToken(pair.RefreshToken)
	assert.ErrorIs(t, err, ErrWrongTokenType)
}

func TestValidateRefreshToken_RejectsAccessToken(t *testing.T) {
	manager := createTestManager(t)

	pair, err := manager.GenerateTokenPair(123, "+77001234567", "device-001")
	require.NoError(t, err)

	// Try to validate access token as refresh token
	_, err = manager.ValidateRefreshToken(pair.AccessToken)
	assert.ErrorIs(t, err, ErrWrongTokenType)
}

func TestGenerateAccessToken_HasAccessType(t *testing.T) {
	manager := createTestManager(t)

	token, _, err := manager.GenerateAccessToken(456, "+77009876543", "device-002")
	require.NoError(t, err)

	claims, err := manager.Parse(token)
	require.NoError(t, err)
	assert.Equal(t, TokenTypeAccess, claims.TokenType)

	// Should pass access validation
	_, err = manager.ValidateAccessToken(token)
	assert.NoError(t, err)
}

// Helper function

func createTestManager(t *testing.T) *Manager {
	cfg := Config{
		SecretKey:       "test-secret-key-exactly-32-chars",
		AccessTokenTTL:  15 * time.Minute,
		RefreshTokenTTL: 720 * time.Hour,
		Issuer:          "test-issuer",
	}
	manager, err := NewManager(cfg)
	require.NoError(t, err)
	return manager
}
