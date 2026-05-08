package adminrbac_test

import (
	"context"
	"testing"

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

func newInterceptor(extraRoles ...string) grpc.UnaryServerInterceptor {
	cfg := adminrbac.Config{
		AdminMethods: []string{
			adminMethod,
			"/chat.AdminChatService/*",
		},
	}
	if len(extraRoles) > 0 {
		cfg.AllowedRoles = extraRoles
	}
	return adminrbac.NewInterceptor(cfg).Unary()
}

func ctxWithRole(roles ...string) context.Context {
	pairs := make([]string, 0, len(roles)*2)
	for _, r := range roles {
		pairs = append(pairs, "x-platform-role", r)
	}
	md := metadata.Pairs(pairs...)
	return metadata.NewIncomingContext(context.Background(), md)
}

func noopHandler(_ context.Context, req any) (any, error) {
	return "ok", nil
}

func runInterceptor(interceptor grpc.UnaryServerInterceptor, ctx context.Context, method string) (any, error) {
	info := &grpc.UnaryServerInfo{FullMethod: method}
	return interceptor(ctx, nil, info, noopHandler)
}

func TestAdminRBAC_ValidAdminRole_Passes(t *testing.T) {
	interceptor := newInterceptor()
	resp, err := runInterceptor(interceptor, ctxWithRole("admin"), adminMethod)
	require.NoError(t, err)
	assert.Equal(t, "ok", resp)
}

func TestAdminRBAC_ValidSupportRole_Passes(t *testing.T) {
	interceptor := newInterceptor()
	resp, err := runInterceptor(interceptor, ctxWithRole("support"), adminMethod)
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

func TestAdminRBAC_UserRole_PermissionDenied(t *testing.T) {
	interceptor := newInterceptor()
	_, err := runInterceptor(interceptor, ctxWithRole("mechanic"), adminMethod)
	require.Error(t, err)
	assert.Equal(t, codes.PermissionDenied, status.Code(err))
}

func TestAdminRBAC_ServiceTokenRole_PermissionDenied(t *testing.T) {
	// Service tokens carry no x-platform-role header — they get Unauthenticated.
	interceptor := newInterceptor()
	_, err := runInterceptor(interceptor, ctxWithRole("receptionist"), adminMethod)
	require.Error(t, err)
	assert.Equal(t, codes.PermissionDenied, status.Code(err))
}

func TestAdminRBAC_WildcardMethod_Passes(t *testing.T) {
	interceptor := newInterceptor()
	resp, err := runInterceptor(interceptor, ctxWithRole("admin"), "/chat.AdminChatService/AdminGetUserChats")
	require.NoError(t, err)
	assert.Equal(t, "ok", resp)
}

func TestAdminRBAC_WildcardMethod_UserRole_PermissionDenied(t *testing.T) {
	interceptor := newInterceptor()
	_, err := runInterceptor(interceptor, ctxWithRole("mechanic"), "/chat.AdminChatService/AdminGetUserChats")
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
			ctx:    ctxWithRole("admin"),
			method: adminMethod,
			wantOK: true,
		},
		{
			name:   "support on exact method",
			ctx:    ctxWithRole("support"),
			method: adminMethod,
			wantOK: true,
		},
		{
			name:   "admin on wildcard method",
			ctx:    ctxWithRole("admin"),
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
			ctx:      ctxWithRole("sales_manager"),
			method:   adminMethod,
			wantCode: codes.PermissionDenied,
		},
		{
			name:     "wrong role on wildcard method",
			ctx:      ctxWithRole("parts_manager"),
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
				ctx = ctxWithRole(tc.roles...)
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
		resp, err := runInterceptor(interceptor, ctxWithRole("platform_admin"), adminMethod)
		require.NoError(t, err)
		assert.Equal(t, "ok", resp)
	})

	t.Run("default admin denied with custom config", func(t *testing.T) {
		_, err := runInterceptor(interceptor, ctxWithRole("admin"), adminMethod)
		require.Error(t, err)
		assert.Equal(t, codes.PermissionDenied, status.Code(err))
	})
}
