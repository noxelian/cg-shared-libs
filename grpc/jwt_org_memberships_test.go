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
	return &sharedjwt.Claims{UserID: 42, OrgIDs: []string{"org-a", "org-b"}}, nil
}

func TestJWTValidatorAdapter_PreservesOrgMemberships(t *testing.T) {
	claims, err := NewJWTValidatorAdapter(orgClaimsValidator{}).ValidateAccessToken("token")
	require.NoError(t, err)
	assert.Equal(t, []string{"org-a", "org-b"}, claims.OrgIDs)
}

func TestAuthInterceptor_CopiesOrgMembershipsIntoAuthInfo(t *testing.T) {
	ctx := metadata.NewIncomingContext(context.Background(), metadata.Pairs("authorization", "Bearer token"))
	interceptor := AuthInterceptor(NewJWTValidatorAdapter(orgClaimsValidator{}), AuthInterceptorConfig{})

	_, err := interceptor(ctx, nil, &grpcgo.UnaryServerInfo{FullMethod: "/test.Service/Method"}, func(ctx context.Context, _ any) (any, error) {
		auth, ok := GetAuthInfo(ctx)
		require.True(t, ok)
		assert.Equal(t, []string{"org-a", "org-b"}, auth.OrgIDs)
		return nil, nil
	})
	require.NoError(t, err)
}
