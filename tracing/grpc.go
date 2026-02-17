package tracing

import (
	"go.opentelemetry.io/contrib/instrumentation/google.golang.org/grpc/otelgrpc"
	"google.golang.org/grpc"
)

// GRPCServerInterceptors returns gRPC server options that add tracing.
// Add these to your gRPC server's options list.
func GRPCServerInterceptors() []grpc.ServerOption {
	return []grpc.ServerOption{
		grpc.StatsHandler(otelgrpc.NewServerHandler()),
	}
}

// GRPCClientInterceptors returns gRPC dial options that add tracing.
// Add these to your gRPC client's options list.
func GRPCClientInterceptors() []grpc.DialOption {
	return []grpc.DialOption{
		grpc.WithStatsHandler(otelgrpc.NewClientHandler()),
	}
}
