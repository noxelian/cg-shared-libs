package jwt

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestManager_OrgMembershipsSurviveMintAndRefresh(t *testing.T) {
	manager := createTestManager(t)

	pair, err := manager.GenerateTokenPairWithContext(42, "+77001234567", "device-1", AppContext{
		App:    "partner",
		OrgIDs: []string{"org-a", "org-b"},
	})
	require.NoError(t, err)

	accessClaims, err := manager.ValidateAccessToken(pair.AccessToken)
	require.NoError(t, err)
	assert.Equal(t, []string{"org-a", "org-b"}, accessClaims.OrgIDs)

	refreshed, err := manager.Refresh(pair.RefreshToken)
	require.NoError(t, err)
	refreshedClaims, err := manager.ValidateAccessToken(refreshed.AccessToken)
	require.NoError(t, err)
	assert.Equal(t, []string{"org-a", "org-b"}, refreshedClaims.OrgIDs)
}

func TestSigner_OrgMembershipsAreSigned(t *testing.T) {
	signer := newTestSigner(t, "org-memberships-test")

	pair, err := signer.GenerateTokenPairWithContext(42, "+77001234567", "device-1", AppContext{
		App:    "partner",
		OrgIDs: []string{"org-a", "org-b"},
	})
	require.NoError(t, err)

	claims, err := signer.parseSelf(pair.AccessToken)
	require.NoError(t, err)
	assert.Equal(t, []string{"org-a", "org-b"}, claims.OrgIDs)
}

func TestManager_EmptyOrgMembershipsRemainAuthoritative(t *testing.T) {
	manager := createTestManager(t)

	pair, err := manager.GenerateTokenPairWithContext(42, "+77001234567", "device-1", AppContext{
		App:    "client",
		OrgIDs: []string{},
	})
	require.NoError(t, err)

	claims, err := manager.ValidateAccessToken(pair.AccessToken)
	require.NoError(t, err)
	require.NotNil(t, claims.OrgIDs)
	assert.Empty(t, claims.OrgIDs)
}
