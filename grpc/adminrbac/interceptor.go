// Package adminrbac provides a gRPC server-side unary interceptor that
// validates platform admin/support role access for admin-only RPCs.
//
// The interceptor reads the x-platform-role metadata header that the BFF
// stamps onto outgoing gRPC calls after resolving the caller's platform
// roles from cg-users. Any method registered as admin-only requires the
// caller to carry at least one of the configured allowed roles.
//
// Usage:
//
//	interceptor := adminrbac.NewInterceptor(adminrbac.Config{
//	    AdminMethods: []string{
//	        "/users.AdminUserService/AdminListCars",
//	    },
//	    AllowedRoles: []string{"admin", "support"},
//	})
//	srv, err := sharedGRPC.NewServerWithInterceptors(cfg,
//	    []grpc.UnaryServerInterceptor{authInterceptor, interceptor.Unary()},
//	    nil,
//	)
package adminrbac

import (
	"context"
	"strings"

	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/metadata"
	"google.golang.org/grpc/status"
)

const platformRoleHeader = "x-platform-role"

// Config holds interceptor configuration.
type Config struct {
	// AdminMethods is the set of fully-qualified gRPC method names that
	// require an admin or support platform role. Supports prefix matching
	// with a trailing "*", e.g. "/users.AdminUserService/*".
	AdminMethods []string

	// AllowedRoles lists the platform role values that grant access.
	// Defaults to ["admin", "support"] when empty.
	AllowedRoles []string
}

// Interceptor enforces admin platform-role access for registered methods.
type Interceptor struct {
	adminMethods []string
	allowedRoles map[string]struct{}
}

// NewInterceptor constructs an Interceptor.
func NewInterceptor(cfg Config) *Interceptor {
	roles := cfg.AllowedRoles
	if len(roles) == 0 {
		roles = []string{"admin", "support"}
	}
	allowed := make(map[string]struct{}, len(roles))
	for _, r := range roles {
		allowed[r] = struct{}{}
	}
	return &Interceptor{
		adminMethods: cfg.AdminMethods,
		allowedRoles: allowed,
	}
}

// Unary returns the grpc.UnaryServerInterceptor.
func (i *Interceptor) Unary() grpc.UnaryServerInterceptor {
	return func(
		ctx context.Context,
		req any,
		info *grpc.UnaryServerInfo,
		handler grpc.UnaryHandler,
	) (any, error) {
		if !i.isAdminMethod(info.FullMethod) {
			return handler(ctx, req)
		}

		roles := extractPlatformRoles(ctx)
		if len(roles) == 0 {
			return nil, status.Error(codes.Unauthenticated, "missing authorization token")
		}

		for _, r := range roles {
			if _, ok := i.allowedRoles[r]; ok {
				return handler(ctx, req)
			}
		}

		return nil, status.Error(codes.PermissionDenied, "admin or support role required")
	}
}

// isAdminMethod returns true when fullMethod matches any registered admin
// method. Supports exact match and prefix wildcard (trailing "/*").
func (i *Interceptor) isAdminMethod(fullMethod string) bool {
	for _, m := range i.adminMethods {
		if strings.HasSuffix(m, "/*") {
			prefix := strings.TrimSuffix(m, "/*")
			if strings.HasPrefix(fullMethod, prefix+"/") {
				return true
			}
		}
		if m == fullMethod {
			return true
		}
	}
	return false
}

// IsPlatformAdmin returns true when the incoming gRPC context carries an
// x-platform-role metadata value of "admin" or "support". Use this helper
// inside gRPC handlers to allow platform admins to impersonate any user
// without being bound by per-user ownership checks.
//
// Example usage in a handler:
//
//	if authInfo.UserID != 0 && authInfo.UserID != buyerUserID && !adminrbac.IsPlatformAdmin(ctx) {
//	    return nil, status.Error(codes.PermissionDenied, "cannot access another user's cart")
//	}
func IsPlatformAdmin(ctx context.Context) bool {
	for _, r := range extractPlatformRoles(ctx) {
		if r == "admin" || r == "support" {
			return true
		}
	}
	return false
}

// extractPlatformRoles reads all values of the x-platform-role metadata key
// from the incoming gRPC context.
func extractPlatformRoles(ctx context.Context) []string {
	md, ok := metadata.FromIncomingContext(ctx)
	if !ok {
		return nil
	}
	return md.Get(platformRoleHeader)
}
