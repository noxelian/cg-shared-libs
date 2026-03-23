package logger

import (
	"context"
	"os"

	"go.opentelemetry.io/otel/trace"
	"go.uber.org/zap"
	"go.uber.org/zap/zapcore"
)

type ctxKey struct{}

var (
	global *zap.Logger
	sugar  *zap.SugaredLogger
)

// Config holds logger configuration
type Config struct {
	Level       string `yaml:"level" env:"LOG_LEVEL" env-default:"info"`
	Development bool   `yaml:"development" env:"LOG_DEV" env-default:"false"`
	Encoding    string `yaml:"encoding" env:"LOG_ENCODING" env-default:"json"`
}

// Init initializes the global logger
func Init(cfg Config) error {
	level, err := zapcore.ParseLevel(cfg.Level)
	if err != nil {
		level = zapcore.InfoLevel
	}

	var config zap.Config
	if cfg.Development {
		config = zap.NewDevelopmentConfig()
		config.EncoderConfig.EncodeLevel = zapcore.CapitalColorLevelEncoder
	} else {
		config = zap.NewProductionConfig()
	}

	config.Level = zap.NewAtomicLevelAt(level)
	if cfg.Encoding != "" {
		config.Encoding = cfg.Encoding
	}

	logger, err := config.Build(
		zap.AddCallerSkip(1),
		zap.AddStacktrace(zapcore.ErrorLevel),
	)
	if err != nil {
		return err
	}

	global = logger
	sugar = logger.Sugar()

	return nil
}

// InitDefault initializes logger with default settings
func InitDefault() {
	if global != nil {
		return
	}

	var config zap.Config
	if os.Getenv("GO_ENV") == "development" {
		config = zap.NewDevelopmentConfig()
		config.EncoderConfig.EncodeLevel = zapcore.CapitalColorLevelEncoder
	} else {
		config = zap.NewProductionConfig()
	}

	logger, _ := config.Build(zap.AddCallerSkip(1))
	global = logger
	sugar = logger.Sugar()
}

// L returns the global logger
func L() *zap.Logger {
	if global == nil {
		InitDefault()
	}
	return global
}

// S returns the global sugared logger
func S() *zap.SugaredLogger {
	if sugar == nil {
		InitDefault()
	}
	return sugar
}

// WithContext returns a logger from context or global logger
func WithContext(ctx context.Context) *zap.Logger {
	if l, ok := ctx.Value(ctxKey{}).(*zap.Logger); ok {
		return l
	}
	return L()
}

// ToContext adds logger to context
func ToContext(ctx context.Context, l *zap.Logger) context.Context {
	return context.WithValue(ctx, ctxKey{}, l)
}

// WithFields returns a new logger with additional fields
func WithFields(fields ...zap.Field) *zap.Logger {
	return L().With(fields...)
}

// WithRequestID adds request_id field to logger
func WithRequestID(ctx context.Context, requestID string) context.Context {
	l := WithContext(ctx).With(zap.String("request_id", requestID))
	return ToContext(ctx, l)
}

// WithUserID adds user_id field to logger
func WithUserID(ctx context.Context, userID int64) context.Context {
	l := WithContext(ctx).With(zap.Int64("user_id", userID))
	return ToContext(ctx, l)
}

// WithSessionID adds session_id field to logger
func WithSessionID(ctx context.Context, sessionID string) context.Context {
	l := WithContext(ctx).With(zap.String("session_id", sessionID))
	return ToContext(ctx, l)
}

// WithTransactionID adds transaction_id field to logger
func WithTransactionID(ctx context.Context, txnID string) context.Context {
	l := WithContext(ctx).With(zap.String("transaction_id", txnID))
	return ToContext(ctx, l)
}

// WithTraceID extracts trace_id and span_id from the OpenTelemetry span context
// and injects them into the logger stored in ctx. If no valid span exists, ctx is returned unchanged.
func WithTraceID(ctx context.Context) context.Context {
	span := trace.SpanFromContext(ctx)
	sc := span.SpanContext()
	if !sc.IsValid() {
		return ctx
	}
	l := WithContext(ctx).With(
		zap.String("trace_id", sc.TraceID().String()),
		zap.String("span_id", sc.SpanID().String()),
	)
	return ToContext(ctx, l)
}

// WithServiceName permanently adds a "service" field to the global logger.
// Call once after Init() in cmd/main.go. Every subsequent log line includes the field.
func WithServiceName(name string) {
	if global == nil {
		InitDefault()
	}
	global = global.With(zap.String("service", name))
	sugar = global.Sugar()
}

// WithPlatform adds a "platform" field to the context logger.
// Use where a real platform/channel value is available (cg-ai, cg-bff webhook handlers).
func WithPlatform(ctx context.Context, platform string) context.Context {
	l := WithContext(ctx).With(zap.String("platform", platform))
	return ToContext(ctx, l)
}

// MaskPhone masks a phone number for secure logging.
// Example: +79001234567 -> +7***4567
func MaskPhone(phone string) string {
	if len(phone) <= 4 {
		return "****"
	}
	return phone[:2] + "***" + phone[len(phone)-4:]
}

// Convenience methods

func Debug(msg string, fields ...zap.Field) {
	L().Debug(msg, fields...)
}

func Info(msg string, fields ...zap.Field) {
	L().Info(msg, fields...)
}

func Warn(msg string, fields ...zap.Field) {
	L().Warn(msg, fields...)
}

func Error(msg string, fields ...zap.Field) {
	L().Error(msg, fields...)
}

func Fatal(msg string, fields ...zap.Field) {
	L().Fatal(msg, fields...)
}

func Sync() error {
	if global != nil {
		return global.Sync()
	}
	return nil
}

