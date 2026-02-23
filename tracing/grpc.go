package tracing

import (
	"go.opentelemetry.io/contrib/instrumentation/google.golang.org/grpc/otelgrpc"
	"google.golang.org/grpc"
)

// GRPCServerInterceptors returns gRPC server options for OTEL tracing.
// Only includes the StatsHandler; logging interceptors are returned separately
// via UnaryServerInterceptors / StreamServerInterceptors to avoid overwriting
// existing ChainUnaryInterceptor/ChainStreamInterceptor chains.
func GRPCServerInterceptors() []grpc.ServerOption {
	return []grpc.ServerOption{
		grpc.StatsHandler(otelgrpc.NewServerHandler()),
	}
}

// UnaryServerInterceptors returns gRPC unary interceptors for trace-log correlation.
// Append these to the service's ChainUnaryInterceptor call.
func UnaryServerInterceptors() []grpc.UnaryServerInterceptor {
	return []grpc.UnaryServerInterceptor{
		LoggingUnaryServerInterceptor(),
	}
}

// StreamServerInterceptors returns gRPC stream interceptors for trace-log correlation.
// Append these to the service's ChainStreamInterceptor call.
func StreamServerInterceptors() []grpc.StreamServerInterceptor {
	return []grpc.StreamServerInterceptor{
		LoggingStreamServerInterceptor(),
	}
}

// GRPCClientInterceptors returns gRPC dial options that add tracing.
// Add these to your gRPC client's options list.
func GRPCClientInterceptors() []grpc.DialOption {
	return []grpc.DialOption{
		grpc.WithStatsHandler(otelgrpc.NewClientHandler()),
	}
}
