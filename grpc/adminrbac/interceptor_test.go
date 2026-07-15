package adminrbac_test

import (
	"context"
	"testing"

	sharedGRPC "github.com/4ubak/cg-shared-libs/grpc"
	"github.com/4ubak/cg-shared-libs/grpc/adminrbac"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/metadata"
	"google.golang.org/grpc/status"
)

const adminMethod = "/users.AdminUserService/AdminListCars"
const publicMethod = "/users.UserService/GetProfile"

func newInterceptor() grpc.UnaryServerInterceptor {
	cfg := adminrbac.Config{
		AdminMethods: []string{
			adminMethod,
			"/chat.AdminChatService/*",
		},
	}
	return adminrbac.NewInterceptor(cfg).Unary()
}

func ctxWithVerifiedRoles(roles ...string) context.Context {
	return sharedGRPC.ContextWithAuthInfo(context.Background(), &sharedGRPC.AuthInfo{
		UserID:        42,
		PlatformRoles: roles,
	})
}

func ctxWithRawRoles(roles ...string) context.Context {
	pairs := make([]string, 0, len(roles)*2)
	for _, r := range roles {
		pairs = append(pairs, "x-platform-role", r)
	}
	md := metadata.Pairs(pairs...)
	return metadata.NewIncomingContext(context.Background(), md)
}

func noopHandler(_ context.Context, req any) (any, error) { //nolint:revive // gRPC requires context as the first argument.
	return "ok", nil
}

func runInterceptor(interceptor grpc.UnaryServerInterceptor, ctx context.Context, method string) (any, error) { //nolint:revive // Test helper mirrors interceptor argument order.
	info := &grpc.UnaryServerInfo{FullMethod: method}
	return interceptor(ctx, nil, info, noopHandler)
}

func TestAdminRBAC_ValidAdminRole_Passes(t *testing.T) {
	interceptor := newInterceptor()
	resp, err := runInterceptor(interceptor, ctxWithVerifiedRoles("admin"), adminMethod)
	require.NoError(t, err)
	assert.Equal(t, "ok", resp)
}

func TestAdminRBAC_ValidSupportRole_Passes(t *testing.T) {
	interceptor := newInterceptor()
	resp, err := runInterceptor(interceptor, ctxWithVerifiedRoles("support"), adminMethod)
	require.NoError(t, err)
	assert.Equal(t, "ok", resp)
}

func TestAdminRBAC_PublicMethod_PassesWithoutRole(t *testing.T) {
	interceptor := newInterceptor()
	resp, err := runInterceptor(interceptor, context.Background(), publicMethod)
	require.NoError(t, err)
	assert.Equal(t, "ok", resp)
}

func TestAdminRBAC_NoRoleOnAdminMethod_Unauthenticated(t *testing.T) {
	interceptor := newInterceptor()
	_, err := runInterceptor(interceptor, context.Background(), adminMethod)
	require.Error(t, err)
	assert.Equal(t, codes.Unauthenticated, status.Code(err))
}

func TestAdminRBAC_AuthenticatedWithoutRole_PermissionDenied(t *testing.T) {
	interceptor := newInterceptor()
	_, err := runInterceptor(interceptor, ctxWithVerifiedRoles(), adminMethod)
	require.Error(t, err)
	assert.Equal(t, codes.PermissionDenied, status.Code(err))
}

func TestAdminRBAC_UserRole_PermissionDenied(t *testing.T) {
	interceptor := newInterceptor()
	_, err := runInterceptor(interceptor, ctxWithVerifiedRoles("mechanic"), adminMethod)
	require.Error(t, err)
	assert.Equal(t, codes.PermissionDenied, status.Code(err))
}

func TestAdminRBAC_ServiceTokenRole_PermissionDenied(t *testing.T) {
	interceptor := newInterceptor()
	_, err := runInterceptor(interceptor, ctxWithVerifiedRoles("receptionist"), adminMethod)
	require.Error(t, err)
	assert.Equal(t, codes.PermissionDenied, status.Code(err))
}

func TestAdminRBAC_WildcardMethod_Passes(t *testing.T) {
	interceptor := newInterceptor()
	resp, err := runInterceptor(interceptor, ctxWithVerifiedRoles("admin"), "/chat.AdminChatService/AdminGetUserChats")
	require.NoError(t, err)
	assert.Equal(t, "ok", resp)
}

func TestAdminRBAC_WildcardMethod_UserRole_PermissionDenied(t *testing.T) {
	interceptor := newInterceptor()
	_, err := runInterceptor(interceptor, ctxWithVerifiedRoles("mechanic"), "/chat.AdminChatService/AdminGetUserChats")
	require.Error(t, err)
	assert.Equal(t, codes.PermissionDenied, status.Code(err))
}

func TestAdminRBAC_TableDriven(t *testing.T) {
	interceptor := newInterceptor()

	tests := []struct {
		name     string
		ctx      context.Context
		method   string
		wantCode codes.Code
		wantOK   bool
	}{
		{
			name:   "admin on exact method",
			ctx:    ctxWithVerifiedRoles("admin"),
			method: adminMethod,
			wantOK: true,
		},
		{
			name:   "support on exact method",
			ctx:    ctxWithVerifiedRoles("support"),
			method: adminMethod,
			wantOK: true,
		},
		{
			name:   "admin on wildcard method",
			ctx:    ctxWithVerifiedRoles("admin"),
			method: "/chat.AdminChatService/AdminGetChatMessages",
			wantOK: true,
		},
		{
			name:   "no role on public method",
			ctx:    context.Background(),
			method: publicMethod,
			wantOK: true,
		},
		{
			name:     "no role on admin method",
			ctx:      context.Background(),
			method:   adminMethod,
			wantCode: codes.Unauthenticated,
		},
		{
			name:     "wrong role on admin method",
			ctx:      ctxWithVerifiedRoles("sales_manager"),
			method:   adminMethod,
			wantCode: codes.PermissionDenied,
		},
		{
			name:     "wrong role on wildcard method",
			ctx:      ctxWithVerifiedRoles("parts_manager"),
			method:   "/chat.AdminChatService/AdminGetUserChats",
			wantCode: codes.PermissionDenied,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			resp, err := runInterceptor(interceptor, tc.ctx, tc.method)
			if tc.wantOK {
				require.NoError(t, err)
				assert.Equal(t, "ok", resp)
			} else {
				require.Error(t, err)
				assert.Equal(t, tc.wantCode, status.Code(err))
			}
		})
	}
}

func TestIsPlatformAdmin(t *testing.T) {
	tests := []struct {
		name  string
		roles []string
		want  bool
	}{
		{"admin role", []string{"admin"}, true},
		{"support role", []string{"support"}, true},
		{"admin among many", []string{"mechanic", "admin"}, true},
		{"support among many", []string{"receptionist", "support"}, true},
		{"unrelated role", []string{"mechanic"}, false},
		{"no roles", []string{}, false},
		{"empty context", nil, false},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			var ctx context.Context
			if tc.roles == nil {
				ctx = context.Background()
			} else {
				ctx = ctxWithVerifiedRoles(tc.roles...)
			}
			assert.Equal(t, tc.want, adminrbac.IsPlatformAdmin(ctx))
		})
	}
}

func TestAdminRBAC_CustomAllowedRoles(t *testing.T) {
	// Custom config: only "platform_admin" is allowed.
	cfg := adminrbac.Config{
		AdminMethods: []string{adminMethod},
		AllowedRoles: []string{"platform_admin"},
	}
	interceptor := adminrbac.NewInterceptor(cfg).Unary()

	t.Run("custom role passes", func(t *testing.T) {
		resp, err := runInterceptor(interceptor, ctxWithVerifiedRoles("platform_admin"), adminMethod)
		require.NoError(t, err)
		assert.Equal(t, "ok", resp)
	})

	t.Run("default admin denied with custom config", func(t *testing.T) {
		_, err := runInterceptor(interceptor, ctxWithVerifiedRoles("admin"), adminMethod)
		require.Error(t, err)
		assert.Equal(t, codes.PermissionDenied, status.Code(err))
	})
}

func TestAdminRBAC_RawPlatformRoleMetadataNeverAuthorizes(t *testing.T) {
	interceptor := newInterceptor()

	t.Run("unauthenticated spoof", func(t *testing.T) {
		_, err := runInterceptor(interceptor, ctxWithRawRoles("admin"), adminMethod)
		require.Error(t, err)
		assert.Equal(t, codes.Unauthenticated, status.Code(err))
	})

	t.Run("authenticated non-admin spoof", func(t *testing.T) {
		ctx := ctxWithVerifiedRoles("mechanic")
		ctx = metadata.NewIncomingContext(ctx, metadata.Pairs("x-platform-role", "admin"))
		_, err := runInterceptor(interceptor, ctx, adminMethod)
		require.Error(t, err)
		assert.Equal(t, codes.PermissionDenied, status.Code(err))
	})

	t.Run("helper ignores spoof", func(t *testing.T) {
		assert.False(t, adminrbac.IsPlatformAdmin(ctxWithRawRoles("admin")))
	})
}
