package grpc

import (
	"context"
	"testing"

	sharedjwt "github.com/4ubak/cg-shared-libs/jwt"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	grpcgo "google.golang.org/grpc"
	"google.golang.org/grpc/metadata"
)

type orgClaimsValidator struct{}

func (orgClaimsValidator) ValidateAccessToken(string) (*sharedjwt.Claims, error) {
	return &sharedjwt.Claims{
		UserID:        42,
		OrgIDs:        []string{"org-a", "org-b"},
		PlatformRoles: []string{"admin", "support"},
	}, nil
}

func TestJWTValidatorAdapter_PreservesOrgMemberships(t *testing.T) {
	claims, err := NewJWTValidatorAdapter(orgClaimsValidator{}).ValidateAccessToken("token")
	require.NoError(t, err)
	assert.Equal(t, []string{"org-a", "org-b"}, claims.OrgIDs)
	assert.Equal(t, []string{"admin", "support"}, claims.PlatformRoles)
}

func TestAuthInterceptor_CopiesOrgMembershipsIntoAuthInfo(t *testing.T) {
	ctx := metadata.NewIncomingContext(context.Background(), metadata.Pairs("authorization", "Bearer token"))
	interceptor := AuthInterceptor(NewJWTValidatorAdapter(orgClaimsValidator{}), AuthInterceptorConfig{})

	_, err := interceptor(ctx, nil, &grpcgo.UnaryServerInfo{FullMethod: "/test.Service/Method"}, func(ctx context.Context, _ any) (any, error) {
		auth, ok := GetAuthInfo(ctx)
		require.True(t, ok)
		assert.Equal(t, []string{"org-a", "org-b"}, auth.OrgIDs)
		assert.Equal(t, []string{"admin", "support"}, auth.PlatformRoles)
		return nil, nil
	})
	require.NoError(t, err)
}

func TestContextWithPlatformRoles_RequiresExistingAuthInfoAndCopiesRoles(t *testing.T) {
	roles := []string{"admin"}
	unauthenticated := ContextWithPlatformRoles(context.Background(), roles)
	_, ok := GetAuthInfo(unauthenticated)
	assert.False(t, ok)

	original := &AuthInfo{UserID: 42, OrgIDs: []string{"org-a"}}
	ctx := ContextWithAuthInfo(context.Background(), original)
	ctx = ContextWithPlatformRoles(ctx, roles)
	roles[0] = "attacker-controlled-mutation"

	auth, ok := GetAuthInfo(ctx)
	require.True(t, ok)
	assert.Equal(t, []string{"admin"}, auth.PlatformRoles)
	assert.Empty(t, original.PlatformRoles)
}
