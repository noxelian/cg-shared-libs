package jwt

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestManager_PlatformRolesAreAccessTokenOnly(t *testing.T) {
	manager := createTestManager(t)

	pair, err := manager.GenerateTokenPairWithContext(42, "+77001234567", "device-1", AppContext{
		App:           "partner",
		PlatformRoles: []string{"admin", "support"},
	})
	require.NoError(t, err)

	accessClaims, err := manager.ValidateAccessToken(pair.AccessToken)
	require.NoError(t, err)
	assert.Equal(t, []string{"admin", "support"}, accessClaims.PlatformRoles)
	refreshClaims, err := manager.ValidateRefreshToken(pair.RefreshToken)
	require.NoError(t, err)
	assert.Empty(t, refreshClaims.PlatformRoles)

	refreshed, err := manager.Refresh(pair.RefreshToken)
	require.NoError(t, err)
	refreshedClaims, err := manager.ValidateAccessToken(refreshed.AccessToken)
	require.NoError(t, err)
	assert.Empty(t, refreshedClaims.PlatformRoles)
}

func TestSigner_PlatformRolesAreSigned(t *testing.T) {
	signer := newTestSigner(t, "platform-roles-test")

	pair, err := signer.GenerateTokenPairWithContext(42, "+77001234567", "device-1", AppContext{
		App:           "partner",
		PlatformRoles: []string{"admin"},
	})
	require.NoError(t, err)

	claims, err := signer.parseSelf(pair.AccessToken)
	require.NoError(t, err)
	assert.Equal(t, []string{"admin"}, claims.PlatformRoles)
}
