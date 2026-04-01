package audit

import (
	"context"
	"encoding/json"
	"time"

	"github.com/4ubak/cg-shared-libs/logger"
	"go.uber.org/zap"
	"go.uber.org/zap/zapcore"
)

// EventType represents the type of audit event
type EventType string

const (
	EventTypeAuth       EventType = "auth"
	EventTypeDataAccess EventType = "data_access"
	EventTypeDataModify EventType = "data_modify"
	EventTypeAdminAction EventType = "admin_action"
	EventTypeSecurity   EventType = "security"
)

// Status represents the outcome of an audited operation
type Status string

const (
	StatusSuccess Status = "success"
	StatusFailure Status = "failure"
)

// Event represents a structured audit log event for bank compliance
type Event struct {
	// Event metadata
	EventType  EventType `json:"event_type"`
	Timestamp  time.Time `json:"timestamp"`
	RequestID  string    `json:"request_id,omitempty"`

	// Actor information
	UserID    int64  `json:"user_id,omitempty"`
	IPAddress string `json:"ip_address,omitempty"`
	UserAgent string `json:"user_agent,omitempty"`

	// Action details
	Action     string `json:"action"`
	Resource   string `json:"resource"`
	ResourceID string `json:"resource_id,omitempty"`
	Status     Status `json:"status"`

	// Additional context
	Details    map[string]interface{} `json:"details,omitempty"`
	ErrorMsg   string                 `json:"error,omitempty"`
	Duration   time.Duration          `json:"duration_ms,omitempty"`
}

// ctxKey is the context key type for audit logger
type ctxKey struct{}

// Auditor is the audit logger interface
type Auditor interface {
	Log(ctx context.Context, event Event)
	LogAuth(ctx context.Context, action string, userID int64, status Status, details map[string]interface{})
	LogDataAccess(ctx context.Context, action, resource, resourceID string, userID int64, status Status)
	LogDataModify(ctx context.Context, action, resource, resourceID string, userID int64, status Status, details map[string]interface{})
	LogAdminAction(ctx context.Context, action, resource, resourceID string, userID int64, status Status, details map[string]interface{})
	LogSecurity(ctx context.Context, action string, userID int64, status Status, details map[string]interface{})
}

// auditLogger implements structured audit logging using Zap
type auditLogger struct {
	logger *zap.Logger
}

// Config holds audit logger configuration
type Config struct {
	Enabled     bool   `yaml:"enabled" env:"AUDIT_ENABLED" env-default:"true"`
	Level       string `yaml:"level" env:"AUDIT_LEVEL" env-default:"info"`
	ServiceName string `yaml:"service_name" env:"SERVICE_NAME" env-default:"unknown"`
}

// global audit logger instance
var global Auditor

// Init initializes the global audit logger
func Init(cfg Config) error {
	if !cfg.Enabled {
		global = &noopAuditor{}
		return nil
	}

	// Create a dedicated audit logger with JSON encoding for ELK/Loki
	encoderConfig := zapcore.EncoderConfig{
		TimeKey:        "timestamp",
		LevelKey:       "level",
		NameKey:        "logger",
		CallerKey:      "caller",
		FunctionKey:    zapcore.OmitKey,
		MessageKey:     "message",
		StacktraceKey:  "stacktrace",
		LineEnding:     zapcore.DefaultLineEnding,
		EncodeLevel:    zapcore.LowercaseLevelEncoder,
		EncodeTime:     zapcore.ISO8601TimeEncoder,
		EncodeDuration: zapcore.MillisDurationEncoder,
		EncodeCaller:   zapcore.ShortCallerEncoder,
	}

	config := zap.Config{
		Level:            zap.NewAtomicLevelAt(zapcore.InfoLevel),
		Development:      false,
		Encoding:         "json",
		EncoderConfig:    encoderConfig,
		OutputPaths:      []string{"stdout"},
		ErrorOutputPaths: []string{"stderr"},
	}

	zapLogger, err := config.Build(
		zap.AddCallerSkip(2),
	)
	if err != nil {
		return err
	}

	// Add service name to all audit logs
	zapLogger = zapLogger.With(
		zap.String("service", cfg.ServiceName),
		zap.String("log_type", "audit"),
	)

	global = &auditLogger{logger: zapLogger}
	return nil
}

// InitDefault initializes audit logger with default settings
func InitDefault() {
	if global != nil {
		return
	}
	if err := Init(Config{Enabled: true, ServiceName: "unknown"}); err != nil {
		global = &noopAuditor{}
	}
}

// L returns the global audit logger
func L() Auditor {
	if global == nil {
		InitDefault()
	}
	return global
}

// ToContext adds audit logger to context
func ToContext(ctx context.Context, a Auditor) context.Context {
	return context.WithValue(ctx, ctxKey{}, a)
}

// FromContext returns audit logger from context or global
func FromContext(ctx context.Context) Auditor {
	if a, ok := ctx.Value(ctxKey{}).(Auditor); ok {
		return a
	}
	return L()
}

// Log logs a full audit event
func (a *auditLogger) Log(ctx context.Context, event Event) {
	if event.Timestamp.IsZero() {
		event.Timestamp = time.Now().UTC()
	}

	fields := []zap.Field{
		zap.String("event_type", string(event.EventType)),
		zap.Time("event_timestamp", event.Timestamp),
		zap.String("action", event.Action),
		zap.String("resource", event.Resource),
		zap.String("status", string(event.Status)),
	}

	if event.RequestID != "" {
		fields = append(fields, zap.String("request_id", event.RequestID))
	}
	if event.UserID != 0 {
		fields = append(fields, zap.Int64("user_id", event.UserID))
	}
	if event.IPAddress != "" {
		fields = append(fields, zap.String("ip_address", event.IPAddress))
	}
	if event.UserAgent != "" {
		fields = append(fields, zap.String("user_agent", event.UserAgent))
	}
	if event.ResourceID != "" {
		fields = append(fields, zap.String("resource_id", event.ResourceID))
	}
	if event.ErrorMsg != "" {
		fields = append(fields, zap.String("error", event.ErrorMsg))
	}
	if event.Duration > 0 {
		fields = append(fields, zap.Duration("duration", event.Duration))
	}
	if len(event.Details) > 0 {
		detailsJSON, err := json.Marshal(event.Details)
		if err == nil {
			fields = append(fields, zap.String("details", string(detailsJSON)))
		}
	}

	a.logger.Info("audit_event", fields...)
}

// LogAuth logs authentication-related events
func (a *auditLogger) LogAuth(ctx context.Context, action string, userID int64, status Status, details map[string]interface{}) {
	event := Event{
		EventType: EventTypeAuth,
		Action:    action,
		Resource:  "auth",
		UserID:    userID,
		Status:    status,
		Details:   details,
	}
	a.enrichFromContext(ctx, &event)
	a.Log(ctx, event)
}

// LogDataAccess logs data access events (read operations)
func (a *auditLogger) LogDataAccess(ctx context.Context, action, resource, resourceID string, userID int64, status Status) {
	event := Event{
		EventType:  EventTypeDataAccess,
		Action:     action,
		Resource:   resource,
		ResourceID: resourceID,
		UserID:     userID,
		Status:     status,
	}
	a.enrichFromContext(ctx, &event)
	a.Log(ctx, event)
}

// LogDataModify logs data modification events (create, update, delete)
func (a *auditLogger) LogDataModify(ctx context.Context, action, resource, resourceID string, userID int64, status Status, details map[string]interface{}) {
	event := Event{
		EventType:  EventTypeDataModify,
		Action:     action,
		Resource:   resource,
		ResourceID: resourceID,
		UserID:     userID,
		Status:     status,
		Details:    details,
	}
	a.enrichFromContext(ctx, &event)
	a.Log(ctx, event)
}

// LogAdminAction logs administrative actions
func (a *auditLogger) LogAdminAction(ctx context.Context, action, resource, resourceID string, userID int64, status Status, details map[string]interface{}) {
	event := Event{
		EventType:  EventTypeAdminAction,
		Action:     action,
		Resource:   resource,
		ResourceID: resourceID,
		UserID:     userID,
		Status:     status,
		Details:    details,
	}
	a.enrichFromContext(ctx, &event)
	a.Log(ctx, event)
}

// LogSecurity logs security-related events (failed auth, permission denied, etc.)
func (a *auditLogger) LogSecurity(ctx context.Context, action string, userID int64, status Status, details map[string]interface{}) {
	event := Event{
		EventType: EventTypeSecurity,
		Action:    action,
		Resource:  "security",
		UserID:    userID,
		Status:    status,
		Details:   details,
	}
	a.enrichFromContext(ctx, &event)
	a.Log(ctx, event)
}

// enrichFromContext extracts common fields from context
func (a *auditLogger) enrichFromContext(ctx context.Context, event *Event) {
	// Try to get request ID from logger context
	if l := logger.WithContext(ctx); l != nil {
		// The request_id is already in the logger context
	}
}

// noopAuditor is a no-op implementation for when audit is disabled
type noopAuditor struct{}

func (n *noopAuditor) Log(ctx context.Context, event Event) {}
func (n *noopAuditor) LogAuth(ctx context.Context, action string, userID int64, status Status, details map[string]interface{}) {
}
func (n *noopAuditor) LogDataAccess(ctx context.Context, action, resource, resourceID string, userID int64, status Status) {
}
func (n *noopAuditor) LogDataModify(ctx context.Context, action, resource, resourceID string, userID int64, status Status, details map[string]interface{}) {
}
func (n *noopAuditor) LogAdminAction(ctx context.Context, action, resource, resourceID string, userID int64, status Status, details map[string]interface{}) {
}
func (n *noopAuditor) LogSecurity(ctx context.Context, action string, userID int64, status Status, details map[string]interface{}) {
}

// Convenience functions for package-level access

// Log logs a full audit event using the global logger
func Log(ctx context.Context, event Event) {
	L().Log(ctx, event)
}

// LogAuth logs authentication events using the global logger
func LogAuth(ctx context.Context, action string, userID int64, status Status, details map[string]interface{}) {
	L().LogAuth(ctx, action, userID, status, details)
}

// LogDataAccess logs data access events using the global logger
func LogDataAccess(ctx context.Context, action, resource, resourceID string, userID int64, status Status) {
	L().LogDataAccess(ctx, action, resource, resourceID, userID, status)
}

// LogDataModify logs data modification events using the global logger
func LogDataModify(ctx context.Context, action, resource, resourceID string, userID int64, status Status, details map[string]interface{}) {
	L().LogDataModify(ctx, action, resource, resourceID, userID, status, details)
}

// LogAdminAction logs administrative actions using the global logger
func LogAdminAction(ctx context.Context, action, resource, resourceID string, userID int64, status Status, details map[string]interface{}) {
	L().LogAdminAction(ctx, action, resource, resourceID, userID, status, details)
}

// LogSecurity logs security events using the global logger
func LogSecurity(ctx context.Context, action string, userID int64, status Status, details map[string]interface{}) {
	L().LogSecurity(ctx, action, userID, status, details)
}
