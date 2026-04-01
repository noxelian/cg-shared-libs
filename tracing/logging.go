package tracing

import (
	"context"

	"github.com/4ubak/cg-shared-libs/logger"
	"go.opentelemetry.io/otel/trace"
	"go.uber.org/zap"
	"google.golang.org/grpc"
)

// Compile-time interface assertion.
var _ grpc.ServerStream = (*wrappedServerStream)(nil)

// LoggingUnaryServerInterceptor injects trace_id and span_id from the current
// OpenTelemetry span into the logger stored in ctx. Every subsequent log line
// within the handler will automatically carry these fields, enabling log-trace
// correlation in Grafana/Jaeger.
func LoggingUnaryServerInterceptor() grpc.UnaryServerInterceptor {
	return func(
		ctx context.Context,
		req any,
		info *grpc.UnaryServerInfo,
		handler grpc.UnaryHandler,
	) (any, error) {
		ctx = injectTraceFields(ctx, info.FullMethod)
		return handler(ctx, req)
	}
}

// LoggingStreamServerInterceptor injects trace_id and span_id into the logger
// context for streaming RPCs.
func LoggingStreamServerInterceptor() grpc.StreamServerInterceptor {
	return func(
		srv any,
		ss grpc.ServerStream,
		info *grpc.StreamServerInfo,
		handler grpc.StreamHandler,
	) error {
		ctx := injectTraceFields(ss.Context(), info.FullMethod)
		return handler(srv, &wrappedServerStream{ServerStream: ss, ctx: ctx})
	}
}

// injectTraceFields reuses logger.WithTraceID to inject trace_id and span_id,
// then layers the grpc_method field on top.
func injectTraceFields(ctx context.Context, method string) context.Context {
	span := trace.SpanFromContext(ctx)
	if !span.SpanContext().IsValid() {
		return ctx
	}

	ctx = logger.WithTraceID(ctx)
	l := logger.WithContext(ctx).With(zap.String("grpc_method", method))
	return logger.ToContext(ctx, l)
}

// wrappedServerStream wraps grpc.ServerStream to override Context().
type wrappedServerStream struct {
	grpc.ServerStream
	ctx context.Context
}

func (w *wrappedServerStream) Context() context.Context {
	return w.ctx
}
