package health

import (
	"context"
	"encoding/json"
	"net/http"
	"sync"
	"time"

	"gitlab.com/xakpro/cg-shared-libs/logger"
	"go.uber.org/zap"
)

// Status represents health check status
type Status string

const (
	StatusOK       Status = "ok"
	StatusDegraded Status = "degraded"
	StatusDown     Status = "down"
)

// Checker is an interface for health check components
type Checker interface {
	Check(ctx context.Context) error
	Name() string
}

// Health holds health check state
type Health struct {
	mu       sync.RWMutex
	checkers []Checker
	version  string
	startTime time.Time
}

// Response represents health check response
type Response struct {
	Status    Status               `json:"status"`
	Version   string               `json:"version,omitempty"`
	Uptime    string               `json:"uptime,omitempty"`
	Timestamp string               `json:"timestamp"`
	Checks    map[string]CheckResult `json:"checks,omitempty"`
}

// CheckResult represents result of a single check
type CheckResult struct {
	Status  Status `json:"status"`
	Message string `json:"message,omitempty"`
	Error   string `json:"error,omitempty"`
}

// New creates a new Health instance
func New(version string) *Health {
	return &Health{
		checkers:  make([]Checker, 0),
		version:   version,
		startTime: time.Now(),
	}
}

// RegisterChecker adds a health check component
func (h *Health) RegisterChecker(checker Checker) {
	h.mu.Lock()
	defer h.mu.Unlock()
	h.checkers = append(h.checkers, checker)
}

// Check performs all health checks
func (h *Health) Check(ctx context.Context) Response {
	h.mu.RLock()
	defer h.mu.RUnlock()

	checks := make(map[string]CheckResult)
	overallStatus := StatusOK

	for _, checker := range h.checkers {
		err := checker.Check(ctx)
		result := CheckResult{
			Status: StatusOK,
		}

		if err != nil {
			result.Status = StatusDown
			result.Error = err.Error()
			result.Message = "Check failed"
			overallStatus = StatusDegraded
		} else {
			result.Message = "OK"
		}

		checks[checker.Name()] = result
	}

	uptime := time.Since(h.startTime)

	return Response{
		Status:    overallStatus,
		Version:   h.version,
		Uptime:    uptime.String(),
		Timestamp: time.Now().UTC().Format(time.RFC3339),
		Checks:    checks,
	}
}

// Handler returns HTTP handler for health check
func (h *Health) Handler() http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		ctx := r.Context()
		response := h.Check(ctx)

		statusCode := http.StatusOK
		if response.Status == StatusDown {
			statusCode = http.StatusServiceUnavailable
		} else if response.Status == StatusDegraded {
			statusCode = http.StatusOK // Still OK, but degraded
		}

		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(statusCode)

		if err := json.NewEncoder(w).Encode(response); err != nil {
			logger.Error("failed to encode health check response",
				zap.Error(err),
			)
		}
	}
}

// SimpleHandler returns a simple health check handler (no checks)
func SimpleHandler() http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		response := Response{
			Status:    StatusOK,
			Timestamp: time.Now().UTC().Format(time.RFC3339),
		}

		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)

		if err := json.NewEncoder(w).Encode(response); err != nil {
			logger.Error("failed to encode health check response",
				zap.Error(err),
			)
		}
	}
}

// ReadinessHandler returns readiness check handler
// Readiness checks if service is ready to accept traffic
func (h *Health) ReadinessHandler() http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		ctx := r.Context()
		response := h.Check(ctx)

		// For readiness, we need all checks to pass
		statusCode := http.StatusOK
		for _, check := range response.Checks {
			if check.Status != StatusOK {
				statusCode = http.StatusServiceUnavailable
				response.Status = StatusDown
				break
			}
		}

		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(statusCode)

		if err := json.NewEncoder(w).Encode(response); err != nil {
			logger.Error("failed to encode readiness check response",
				zap.Error(err),
			)
		}
	}
}
