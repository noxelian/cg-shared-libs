package serviceauth_test

import (
	"context"
	"errors"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	sharedGRPC "github.com/4ubak/cg-shared-libs/grpc"
	"github.com/4ubak/cg-shared-libs/grpc/serviceauth"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/metadata"
	"google.golang.org/grpc/status"
)

// --- stub validator ---

type stubValidator struct {
	claims *sharedGRPC.JWTClaims
	err    error
}

func (v *stubValidator) ValidateAccessToken(_ string) (*sharedGRPC.JWTClaims, error) {
	return v.claims, v.err
}

// --- helpers ---

const restrictedMethod = "/orders.OrderService/InternalGetOrder"
const publicMethod = "/orders.OrderService/ListPlans"

func newInterceptor(v sharedGRPC.JWTValidator) grpc.UnaryServerInterceptor {
	return serviceauth.NewInterceptor(v, serviceauth.Config{
		ServiceMethods: []string{restrictedMethod},
	}).Unary()
}

func ctxWithBearerToken(token string) context.Context {
	md := metadata.Pairs("authorization", "Bearer "+token)
	return metadata.NewIncomingContext(context.Background(), md)
}

func noopHandler(_ context.Context, req any) (any, error) {
	return "ok", nil
}

func runInterceptor(interceptor grpc.UnaryServerInterceptor, ctx context.Context, method string) (any, error) {
	info := &grpc.UnaryServerInfo{FullMethod: method}
	return interceptor(ctx, nil, info, noopHandler)
}

// --- tests ---

func TestServiceAuthInterceptor_ValidServiceToken_Passes(t *testing.T) {
	v := &stubValidator{claims: &sharedGRPC.JWTClaims{UserID: 0, Phone: "service:cg-jobs"}}
	interceptor := newInterceptor(v)

	resp, err := runInterceptor(interceptor, ctxWithBearerToken("valid-service-token"), restrictedMethod)
	require.NoError(t, err)
	assert.Equal(t, "ok", resp)
}

func TestServiceAuthInterceptor_PublicMethod_PassesWithoutToken(t *testing.T) {
	v := &stubValidator{err: errors.New("should not be called")}
	interceptor := newInterceptor(v)

	// No token in context — should pass because method is not restricted
	resp, err := runInterceptor(interceptor, context.Background(), publicMethod)
	require.NoError(t, err)
	assert.Equal(t, "ok", resp)
}

func TestServiceAuthInterceptor_NoToken_Unauthenticated(t *testing.T) {
	v := &stubValidator{claims: &sharedGRPC.JWTClaims{UserID: 0, Phone: "service:cg-jobs"}}
	interceptor := newInterceptor(v)

	_, err := runInterceptor(interceptor, context.Background(), restrictedMethod)
	require.Error(t, err)
	assert.Equal(t, codes.Unauthenticated, status.Code(err))
}

func TestServiceAuthInterceptor_InvalidToken_Unauthenticated(t *testing.T) {
	v := &stubValidator{err: errors.New("token expired")}
	interceptor := newInterceptor(v)

	_, err := runInterceptor(interceptor, ctxWithBearerToken("bad-token"), restrictedMethod)
	require.Error(t, err)
	assert.Equal(t, codes.Unauthenticated, status.Code(err))
}

func TestServiceAuthInterceptor_UserToken_PermissionDenied(t *testing.T) {
	// A real user token — Phone does not start with "service:"
	v := &stubValidator{claims: &sharedGRPC.JWTClaims{UserID: 42, Phone: "+77001234567"}}
	interceptor := newInterceptor(v)

	_, err := runInterceptor(interceptor, ctxWithBearerToken("user-token"), restrictedMethod)
	require.Error(t, err)
	assert.Equal(t, codes.PermissionDenied, status.Code(err))
}

func TestServiceAuthInterceptor_UserIDZeroUserToken_PermissionDenied(t *testing.T) {
	// userID=0 with a user-style phone — the classic bypass attempt
	v := &stubValidator{claims: &sharedGRPC.JWTClaims{UserID: 0, Phone: "+77009999999"}}
	interceptor := newInterceptor(v)

	_, err := runInterceptor(interceptor, ctxWithBearerToken("bypass-token"), restrictedMethod)
	require.Error(t, err)
	assert.Equal(t, codes.PermissionDenied, status.Code(err))
}

func TestServiceAuthInterceptor_BearerPrefixCaseInsensitive(t *testing.T) {
	v := &stubValidator{claims: &sharedGRPC.JWTClaims{UserID: 0, Phone: "service:cg-bff"}}
	interceptor := newInterceptor(v)

	// lowercase "bearer "
	md := metadata.Pairs("authorization", "bearer valid-token")
	ctx := metadata.NewIncomingContext(context.Background(), md)

	resp, err := runInterceptor(interceptor, ctx, restrictedMethod)
	require.NoError(t, err)
	assert.Equal(t, "ok", resp)
}
