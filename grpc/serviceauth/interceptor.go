// Package serviceauth provides a gRPC server-side unary interceptor that
// validates service-to-service JWT tokens.
//
// Service tokens are regular JWTs issued with UserID=0 and a Phone value
// prefixed with "service:" (e.g. "service:cg-jobs"). The interceptor
// distinguishes them from user tokens and explicitly rejects user tokens
// (including userID=0 bypass attempts) on routes registered as service-only.
//
// Usage:
//
//	interceptor := serviceauth.NewInterceptor(jwtValidator, serviceauth.Config{
//	    ServiceMethods: []string{
//	        "/orders.OrderService/GetOrder",
//	    },
//	})
//	srv, err := sharedGRPC.NewServerWithInterceptors(cfg,
//	    []grpc.UnaryServerInterceptor{interceptor},
//	    nil,
//	)
package serviceauth

import (
	"context"
	"strings"

	sharedGRPC "github.com/4ubak/cg-shared-libs/grpc"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

// ServicePrefix is the phone-field prefix that identifies a service token.
const ServicePrefix = "service:"

// Config holds interceptor configuration.
type Config struct {
	// ServiceMethods is the set of fully-qualified gRPC method names
	// (e.g. "/orders.OrderService/InternalGetOrder") that require a service
	// token. All other methods pass through untouched.
	ServiceMethods []string
}

// Interceptor is the service-auth unary interceptor.
type Interceptor struct {
	validator      sharedGRPC.JWTValidator
	serviceMethods map[string]struct{}
}

// NewInterceptor constructs an Interceptor. validator must not be nil.
func NewInterceptor(validator sharedGRPC.JWTValidator, cfg Config) *Interceptor {
	methods := make(map[string]struct{}, len(cfg.ServiceMethods))
	for _, m := range cfg.ServiceMethods {
		methods[m] = struct{}{}
	}
	return &Interceptor{validator: validator, serviceMethods: methods}
}

// Unary returns the grpc.UnaryServerInterceptor. Register it via
// sharedGRPC.NewServerWithInterceptors.
func (i *Interceptor) Unary() grpc.UnaryServerInterceptor {
	return func(
		ctx context.Context,
		req any,
		info *grpc.UnaryServerInfo,
		handler grpc.UnaryHandler,
	) (any, error) {
		if _, restricted := i.serviceMethods[info.FullMethod]; !restricted {
			return handler(ctx, req)
		}

		token := sharedGRPC.GetMetadata(ctx, "authorization")
		if token == "" {
			return nil, status.Error(codes.Unauthenticated, "missing authorization token")
		}
		if len(token) > 7 && strings.EqualFold(token[:7], "bearer ") {
			token = token[7:]
		}

		claims, err := i.validator.ValidateAccessToken(token)
		if err != nil {
			return nil, status.Error(codes.Unauthenticated, "invalid token")
		}

		// Reject user tokens: service tokens must carry Phone prefixed "service:".
		// This also rejects any token with UserID=0 that is not a legitimate service token.
		if !strings.HasPrefix(claims.Phone, ServicePrefix) {
			return nil, status.Error(codes.PermissionDenied, "service token required")
		}

		return handler(ctx, req)
	}
}
