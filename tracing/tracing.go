package tracing

import (
	"context"
	"fmt"
	"strings"
	"time"

	"gitlab.com/xakpro/cg-shared-libs/logger"
	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/codes"
	"go.opentelemetry.io/otel/exporters/otlp/otlptrace/otlptracegrpc"
	"go.opentelemetry.io/otel/propagation"
	"go.opentelemetry.io/otel/sdk/resource"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
	semconv "go.opentelemetry.io/otel/semconv/v1.28.0"
	"go.opentelemetry.io/otel/trace"
	"go.uber.org/zap"
)

// Config holds OpenTelemetry configuration
type Config struct {
	Enabled        bool   `yaml:"enabled" env:"OTEL_ENABLED" env-default:"false"`
	ServiceName    string `yaml:"service_name" env:"OTEL_SERVICE_NAME"`
	ServiceVersion string `yaml:"service_version" env:"OTEL_SERVICE_VERSION" env-default:"0.0.0"`
	Environment    string `yaml:"environment" env:"OTEL_ENVIRONMENT" env-default:"development"`
	OTLPEndpoint   string `yaml:"otlp_endpoint" env:"OTEL_EXPORTER_OTLP_ENDPOINT" env-default:"localhost:4317"`
}

// Init initializes OpenTelemetry tracing
func Init(ctx context.Context, cfg Config) (func(), error) {
	if !cfg.Enabled {
		logger.Info("OpenTelemetry tracing disabled")
		return func() {}, nil
	}

	if cfg.ServiceName == "" {
		return nil, fmt.Errorf("service name is required for tracing")
	}

	// Create resource
	res, err := resource.New(ctx,
		resource.WithAttributes(
			semconv.ServiceNameKey.String(cfg.ServiceName),
			semconv.ServiceVersionKey.String(cfg.ServiceVersion),
			attribute.String("deployment.environment", cfg.Environment),
		),
	)
	if err != nil {
		return nil, fmt.Errorf("failed to create resource: %w", err)
	}

	// Create OTLP exporter
	otlpEndpoint := cfg.OTLPEndpoint
	if otlpEndpoint == "" {
		otlpEndpoint = "localhost:4317"
	}

	// Build endpoint URL: WithEndpointURL expects "http://host:port"
	endpointURL := otlpEndpoint
	if !strings.HasPrefix(endpointURL, "http://") && !strings.HasPrefix(endpointURL, "https://") {
		endpointURL = "http://" + endpointURL
	}

	exporter, err := otlptracegrpc.New(ctx,
		otlptracegrpc.WithEndpointURL(endpointURL),
		otlptracegrpc.WithInsecure(),
	)
	if err != nil {
		return nil, fmt.Errorf("failed to create OTLP exporter: %w", err)
	}

	// Create trace provider
	tp := sdktrace.NewTracerProvider(
		sdktrace.WithBatcher(exporter),
		sdktrace.WithResource(res),
		sdktrace.WithSampler(sdktrace.AlwaysSample()),
	)

	// Set global tracer provider
	otel.SetTracerProvider(tp)

	// Set global propagator
	otel.SetTextMapPropagator(propagation.NewCompositeTextMapPropagator(
		propagation.TraceContext{},
		propagation.Baggage{},
	))

	logger.Info("OpenTelemetry tracing initialized",
		zap.String("service", cfg.ServiceName),
		zap.String("version", cfg.ServiceVersion),
		zap.String("environment", cfg.Environment),
		zap.String("otlp_endpoint", otlpEndpoint),
	)

	// Return shutdown function
	return func() {
		shutdownCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		if err := tp.Shutdown(shutdownCtx); err != nil {
			logger.Error("failed to shutdown tracer provider", zap.Error(err))
		}
	}, nil
}

// StartSpan starts a new span with the given name
func StartSpan(ctx context.Context, name string, opts ...trace.SpanStartOption) (context.Context, trace.Span) {
	tracer := otel.Tracer("cg-platform")
	return tracer.Start(ctx, name, opts...)
}

// SpanFromContext extracts span from context
func SpanFromContext(ctx context.Context) trace.Span {
	return trace.SpanFromContext(ctx)
}

// AddSpanAttributes adds attributes to the current span
func AddSpanAttributes(ctx context.Context, attrs ...attribute.KeyValue) {
	span := trace.SpanFromContext(ctx)
	if span.IsRecording() {
		span.SetAttributes(attrs...)
	}
}

// RecordError records an error in the current span
func RecordError(ctx context.Context, err error) {
	span := trace.SpanFromContext(ctx)
	if span.IsRecording() {
		span.RecordError(err)
		span.SetStatus(codes.Error, err.Error())
	}
}

// SetSpanStatus sets the status of the current span
func SetSpanStatus(ctx context.Context, code codes.Code, msg string) {
	span := trace.SpanFromContext(ctx)
	if span.IsRecording() {
		span.SetStatus(code, msg)
	}
}
