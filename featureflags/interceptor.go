package featureflags

import (
	"context"

	"google.golang.org/grpc"
)

type managerCtxKey struct{}

// UnaryServerInterceptor returns a gRPC unary server interceptor that injects
// the feature flag Manager into the request context. Downstream handlers can
// retrieve it with FromContext.
func UnaryServerInterceptor(mgr *Manager) grpc.UnaryServerInterceptor {
	return func(
		ctx context.Context,
		req any,
		info *grpc.UnaryServerInfo,
		handler grpc.UnaryHandler,
	) (any, error) {
		ctx = context.WithValue(ctx, managerCtxKey{}, mgr)
		return handler(ctx, req)
	}
}

// FromContext retrieves the feature flag Manager from the context.
// Returns nil if no Manager is present.
func FromContext(ctx context.Context) *Manager {
	mgr, _ := ctx.Value(managerCtxKey{}).(*Manager)
	return mgr
}
