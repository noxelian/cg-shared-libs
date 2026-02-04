package audit

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestInit(t *testing.T) {
	tests := []struct {
		name    string
		cfg     Config
		wantErr bool
	}{
		{
			name: "enabled audit logger",
			cfg: Config{
				Enabled:     true,
				Level:       "info",
				ServiceName: "test-service",
			},
			wantErr: false,
		},
		{
			name: "disabled audit logger",
			cfg: Config{
				Enabled:     false,
				ServiceName: "test-service",
			},
			wantErr: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// Reset global for test isolation
			global = nil

			err := Init(tt.cfg)
			if tt.wantErr {
				assert.Error(t, err)
			} else {
				assert.NoError(t, err)
				assert.NotNil(t, L())
			}
		})
	}
}

func TestInitDefault(t *testing.T) {
	// Reset global
	global = nil

	InitDefault()
	assert.NotNil(t, L())
}

func TestEventTypes(t *testing.T) {
	assert.Equal(t, EventType("auth"), EventTypeAuth)
	assert.Equal(t, EventType("data_access"), EventTypeDataAccess)
	assert.Equal(t, EventType("data_modify"), EventTypeDataModify)
	assert.Equal(t, EventType("admin_action"), EventTypeAdminAction)
	assert.Equal(t, EventType("security"), EventTypeSecurity)
}

func TestStatusTypes(t *testing.T) {
	assert.Equal(t, Status("success"), StatusSuccess)
	assert.Equal(t, Status("failure"), StatusFailure)
}

func TestAuditLogger_Log(t *testing.T) {
	// Initialize audit logger
	global = nil
	err := Init(Config{
		Enabled:     true,
		ServiceName: "test-service",
	})
	require.NoError(t, err)

	ctx := context.Background()

	// Test logging a full event
	event := Event{
		EventType:  EventTypeAuth,
		Timestamp:  time.Now().UTC(),
		RequestID:  "req-123",
		UserID:     12345,
		IPAddress:  "192.168.1.1",
		UserAgent:  "TestAgent/1.0",
		Action:     "login",
		Resource:   "auth",
		ResourceID: "",
		Status:     StatusSuccess,
		Details: map[string]interface{}{
			"method": "phone_code",
		},
		Duration: 100 * time.Millisecond,
	}

	// Should not panic
	assert.NotPanics(t, func() {
		Log(ctx, event)
	})
}

func TestAuditLogger_LogAuth(t *testing.T) {
	global = nil
	err := Init(Config{
		Enabled:     true,
		ServiceName: "test-service",
	})
	require.NoError(t, err)

	ctx := context.Background()

	// Test success case
	assert.NotPanics(t, func() {
		LogAuth(ctx, "login", 12345, StatusSuccess, map[string]interface{}{
			"method":   "phone_code",
			"phone":    "+7***1234",
			"device":   "device-123",
		})
	})

	// Test failure case
	assert.NotPanics(t, func() {
		LogAuth(ctx, "login_failed", 0, StatusFailure, map[string]interface{}{
			"reason": "invalid_code",
			"phone":  "+7***1234",
		})
	})
}

func TestAuditLogger_LogDataAccess(t *testing.T) {
	global = nil
	err := Init(Config{
		Enabled:     true,
		ServiceName: "test-service",
	})
	require.NoError(t, err)

	ctx := context.Background()

	assert.NotPanics(t, func() {
		LogDataAccess(ctx, "read", "user", "12345", 12345, StatusSuccess)
	})

	assert.NotPanics(t, func() {
		LogDataAccess(ctx, "read", "request", "req-456", 12345, StatusFailure)
	})
}

func TestAuditLogger_LogDataModify(t *testing.T) {
	global = nil
	err := Init(Config{
		Enabled:     true,
		ServiceName: "test-service",
	})
	require.NoError(t, err)

	ctx := context.Background()

	// Create operation
	assert.NotPanics(t, func() {
		LogDataModify(ctx, "create", "request", "req-789", 12345, StatusSuccess, map[string]interface{}{
			"request_type": "buy",
			"category":     "parts",
		})
	})

	// Update operation
	assert.NotPanics(t, func() {
		LogDataModify(ctx, "update", "organization", "org-123", 12345, StatusSuccess, map[string]interface{}{
			"fields_changed": []string{"name", "address"},
		})
	})

	// Delete operation
	assert.NotPanics(t, func() {
		LogDataModify(ctx, "delete", "request", "req-789", 12345, StatusSuccess, nil)
	})
}

func TestAuditLogger_LogAdminAction(t *testing.T) {
	global = nil
	err := Init(Config{
		Enabled:     true,
		ServiceName: "test-service",
	})
	require.NoError(t, err)

	ctx := context.Background()

	assert.NotPanics(t, func() {
		LogAdminAction(ctx, "view_user_data", "user", "12345", 1, StatusSuccess, map[string]interface{}{
			"target_user_id": 12345,
			"data_accessed":  []string{"profile", "requests", "counters"},
		})
	})

	assert.NotPanics(t, func() {
		LogAdminAction(ctx, "modify_user", "user", "12345", 1, StatusSuccess, map[string]interface{}{
			"changes": map[string]interface{}{
				"status": "suspended",
			},
		})
	})
}

func TestAuditLogger_LogSecurity(t *testing.T) {
	global = nil
	err := Init(Config{
		Enabled:     true,
		ServiceName: "test-service",
	})
	require.NoError(t, err)

	ctx := context.Background()

	// Failed authentication
	assert.NotPanics(t, func() {
		LogSecurity(ctx, "auth_failed", 0, StatusFailure, map[string]interface{}{
			"reason":    "invalid_token",
			"ip":        "192.168.1.1",
			"attempts":  3,
		})
	})

	// Permission denied
	assert.NotPanics(t, func() {
		LogSecurity(ctx, "permission_denied", 12345, StatusFailure, map[string]interface{}{
			"resource":    "/api/v1/admin/users",
			"required":    "admin",
			"user_roles":  []string{"user"},
		})
	})

	// Rate limit exceeded
	assert.NotPanics(t, func() {
		LogSecurity(ctx, "rate_limit_exceeded", 12345, StatusFailure, map[string]interface{}{
			"limit":    100,
			"period":   "1m",
			"endpoint": "/api/v1/mobile/requests",
		})
	})
}

func TestNoopAuditor(t *testing.T) {
	global = nil
	err := Init(Config{
		Enabled: false,
	})
	require.NoError(t, err)

	ctx := context.Background()

	// All methods should not panic and do nothing
	assert.NotPanics(t, func() {
		Log(ctx, Event{})
		LogAuth(ctx, "test", 0, StatusSuccess, nil)
		LogDataAccess(ctx, "test", "resource", "id", 0, StatusSuccess)
		LogDataModify(ctx, "test", "resource", "id", 0, StatusSuccess, nil)
		LogAdminAction(ctx, "test", "resource", "id", 0, StatusSuccess, nil)
		LogSecurity(ctx, "test", 0, StatusSuccess, nil)
	})
}

func TestContextOperations(t *testing.T) {
	global = nil
	err := Init(Config{
		Enabled:     true,
		ServiceName: "test-service",
	})
	require.NoError(t, err)

	ctx := context.Background()
	auditor := L()

	// Add auditor to context
	ctxWithAuditor := ToContext(ctx, auditor)

	// Retrieve from context
	retrieved := FromContext(ctxWithAuditor)
	assert.NotNil(t, retrieved)

	// FromContext with empty context should return global
	retrieved = FromContext(ctx)
	assert.NotNil(t, retrieved)
}

func TestEvent_DefaultTimestamp(t *testing.T) {
	global = nil
	err := Init(Config{
		Enabled:     true,
		ServiceName: "test-service",
	})
	require.NoError(t, err)

	ctx := context.Background()

	// Event without timestamp should get current time
	event := Event{
		EventType: EventTypeAuth,
		Action:    "test",
		Resource:  "test",
		Status:    StatusSuccess,
	}

	assert.True(t, event.Timestamp.IsZero())

	// After logging, timestamp would be set internally
	assert.NotPanics(t, func() {
		Log(ctx, event)
	})
}
