package health_test

import (
	"context"
	"encoding/json"
	"errors"
	"net"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/alicebob/miniredis/v2"
	"github.com/redis/go-redis/v9"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"gitlab.com/xakpro/cg-shared-libs/health"
)

// testChecker implements health.Checker for testing
type testChecker struct {
	name string
	err  error
}

func (c *testChecker) Check(_ context.Context) error { return c.err }
func (c *testChecker) Name() string                   { return c.name }

func TestNew_ReturnsNonNil(t *testing.T) {
	h := health.New("v1.0.0")
	require.NotNil(t, h)
}

func TestHandler_NoCheckers_ReturnsOK(t *testing.T) {
	h := health.New("v1.0.0")
	handler := h.Handler()

	rr := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/health", nil)
	handler.ServeHTTP(rr, req)

	assert.Equal(t, http.StatusOK, rr.Code)

	var resp health.Response
	err := json.NewDecoder(rr.Body).Decode(&resp)
	require.NoError(t, err)
	assert.Equal(t, health.StatusOK, resp.Status)
	assert.Equal(t, "v1.0.0", resp.Version)
}

func TestHandler_AllCheckersPass_ReturnsOK(t *testing.T) {
	h := health.New("v2.0.0")
	h.RegisterChecker(&testChecker{name: "db", err: nil})
	h.RegisterChecker(&testChecker{name: "redis", err: nil})

	rr := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/health", nil)
	h.Handler().ServeHTTP(rr, req)

	assert.Equal(t, http.StatusOK, rr.Code)

	var resp health.Response
	err := json.NewDecoder(rr.Body).Decode(&resp)
	require.NoError(t, err)
	assert.Equal(t, health.StatusOK, resp.Status)
	assert.Len(t, resp.Checks, 2)
	assert.Equal(t, health.StatusOK, resp.Checks["db"].Status)
	assert.Equal(t, health.StatusOK, resp.Checks["redis"].Status)
}

func TestHandler_OneCheckerFails_ReturnsDegraded(t *testing.T) {
	h := health.New("v1.0.0")
	h.RegisterChecker(&testChecker{name: "db", err: nil})
	h.RegisterChecker(&testChecker{name: "redis", err: errors.New("connection refused")})

	rr := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/health", nil)
	h.Handler().ServeHTTP(rr, req)

	// Degraded still returns 200
	assert.Equal(t, http.StatusOK, rr.Code)

	var resp health.Response
	err := json.NewDecoder(rr.Body).Decode(&resp)
	require.NoError(t, err)
	assert.Equal(t, health.StatusDegraded, resp.Status)
	assert.Equal(t, health.StatusOK, resp.Checks["db"].Status)
	assert.Equal(t, health.StatusDown, resp.Checks["redis"].Status)
	assert.Equal(t, "connection refused", resp.Checks["redis"].Error)
}

func TestHandler_AllCheckersFail_ReturnsDegraded(t *testing.T) {
	// Note: the current implementation sets status to "degraded" when any checker fails,
	// not "down". This matches the source code behavior in health.go Check() method.
	h := health.New("v1.0.0")
	h.RegisterChecker(&testChecker{name: "db", err: errors.New("db down")})
	h.RegisterChecker(&testChecker{name: "redis", err: errors.New("redis down")})

	rr := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/health", nil)
	h.Handler().ServeHTTP(rr, req)

	var resp health.Response
	err := json.NewDecoder(rr.Body).Decode(&resp)
	require.NoError(t, err)
	// Source code sets overallStatus = StatusDegraded for any failure
	assert.Equal(t, health.StatusDegraded, resp.Status)
	assert.Equal(t, health.StatusDown, resp.Checks["db"].Status)
	assert.Equal(t, health.StatusDown, resp.Checks["redis"].Status)
}

func TestRegister_CheckerNameAppears(t *testing.T) {
	h := health.New("v1.0.0")
	h.RegisterChecker(&testChecker{name: "custom-checker", err: nil})

	rr := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/health", nil)
	h.Handler().ServeHTTP(rr, req)

	var resp health.Response
	err := json.NewDecoder(rr.Body).Decode(&resp)
	require.NoError(t, err)

	_, exists := resp.Checks["custom-checker"]
	assert.True(t, exists, "checker name should appear in response")
	assert.Equal(t, "OK", resp.Checks["custom-checker"].Message)
}

func TestHandler_ResponseIsJSON(t *testing.T) {
	h := health.New("v1.0.0")
	rr := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/health", nil)
	h.Handler().ServeHTTP(rr, req)

	assert.Equal(t, "application/json", rr.Header().Get("Content-Type"))
}

func TestHandler_ResponseContainsUptime(t *testing.T) {
	h := health.New("v1.0.0")
	rr := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/health", nil)
	h.Handler().ServeHTTP(rr, req)

	var resp health.Response
	err := json.NewDecoder(rr.Body).Decode(&resp)
	require.NoError(t, err)
	assert.NotEmpty(t, resp.Uptime)
	assert.NotEmpty(t, resp.Timestamp)
}

func TestCheck_DirectCall(t *testing.T) {
	h := health.New("v1.0.0")
	h.RegisterChecker(&testChecker{name: "db", err: nil})

	resp := h.Check(context.Background())
	assert.Equal(t, health.StatusOK, resp.Status)
	assert.Equal(t, "v1.0.0", resp.Version)
	assert.Len(t, resp.Checks, 1)
}

func TestSimpleHandler_ReturnsOK(t *testing.T) {
	handler := health.SimpleHandler()
	rr := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/health", nil)
	handler.ServeHTTP(rr, req)

	assert.Equal(t, http.StatusOK, rr.Code)

	var resp health.Response
	err := json.NewDecoder(rr.Body).Decode(&resp)
	require.NoError(t, err)
	assert.Equal(t, health.StatusOK, resp.Status)
}

func TestReadinessHandler_AllPass_ReturnsOK(t *testing.T) {
	h := health.New("v1.0.0")
	h.RegisterChecker(&testChecker{name: "db", err: nil})

	rr := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/readiness", nil)
	h.ReadinessHandler().ServeHTTP(rr, req)

	assert.Equal(t, http.StatusOK, rr.Code)

	var resp health.Response
	err := json.NewDecoder(rr.Body).Decode(&resp)
	require.NoError(t, err)
	assert.Equal(t, health.StatusOK, resp.Status)
}

func TestReadinessHandler_CheckFails_ReturnsServiceUnavailable(t *testing.T) {
	h := health.New("v1.0.0")
	h.RegisterChecker(&testChecker{name: "db", err: errors.New("connection lost")})

	rr := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/readiness", nil)
	h.ReadinessHandler().ServeHTTP(rr, req)

	assert.Equal(t, http.StatusServiceUnavailable, rr.Code)

	var resp health.Response
	err := json.NewDecoder(rr.Body).Decode(&resp)
	require.NoError(t, err)
	assert.Equal(t, health.StatusDown, resp.Status)
}

// ==================== Checker Tests ====================

func TestCustomChecker_Success(t *testing.T) {
	checker := health.NewCustomChecker("custom", func(ctx context.Context) error {
		return nil
	})

	assert.Equal(t, "custom", checker.Name())

	err := checker.Check(context.Background())
	assert.NoError(t, err)
}

func TestCustomChecker_Failure(t *testing.T) {
	checker := health.NewCustomChecker("custom", func(ctx context.Context) error {
		return errors.New("check failed")
	})

	err := checker.Check(context.Background())
	assert.EqualError(t, err, "check failed")
}

func TestCustomChecker_IntegrationWithHealth(t *testing.T) {
	h := health.New("v1.0.0")

	successChecker := health.NewCustomChecker("success-check", func(ctx context.Context) error {
		return nil
	})
	failChecker := health.NewCustomChecker("fail-check", func(ctx context.Context) error {
		return errors.New("failed")
	})

	h.RegisterChecker(successChecker)
	h.RegisterChecker(failChecker)

	rr := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/health", nil)
	h.Handler().ServeHTTP(rr, req)

	var resp health.Response
	err := json.NewDecoder(rr.Body).Decode(&resp)
	require.NoError(t, err)

	assert.Equal(t, health.StatusDegraded, resp.Status)
	assert.Equal(t, health.StatusOK, resp.Checks["success-check"].Status)
	assert.Equal(t, health.StatusDown, resp.Checks["fail-check"].Status)
}

func TestRedisChecker_Name(t *testing.T) {
	checker := health.NewRedisChecker(nil, "redis-primary")
	assert.Equal(t, "redis-primary", checker.Name())
}

func TestDatabaseChecker_Name(t *testing.T) {
	checker := health.NewDatabaseChecker(nil, "postgres-main")
	assert.Equal(t, "postgres-main", checker.Name())
}

func TestReadinessHandler_NoCheckers_ReturnsOK(t *testing.T) {
	h := health.New("v1.0.0")

	rr := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/readiness", nil)
	h.ReadinessHandler().ServeHTTP(rr, req)

	assert.Equal(t, http.StatusOK, rr.Code)

	var resp health.Response
	err := json.NewDecoder(rr.Body).Decode(&resp)
	require.NoError(t, err)
	assert.Equal(t, health.StatusOK, resp.Status)
}

func TestSimpleHandler_ResponseIsJSON(t *testing.T) {
	handler := health.SimpleHandler()
	rr := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/health", nil)
	handler.ServeHTTP(rr, req)

	assert.Equal(t, "application/json", rr.Header().Get("Content-Type"))

	var resp health.Response
	err := json.NewDecoder(rr.Body).Decode(&resp)
	require.NoError(t, err)
	assert.NotEmpty(t, resp.Timestamp)
}

func TestRedisChecker_Success(t *testing.T) {
	s := miniredis.RunT(t)
	client := redis.NewClient(&redis.Options{Addr: s.Addr()})
	defer client.Close()

	checker := health.NewRedisChecker(client, "redis")
	assert.Equal(t, "redis", checker.Name())

	err := checker.Check(context.Background())
	assert.NoError(t, err)
}

func TestRedisChecker_Failure(t *testing.T) {
	// Use a client pointing to a closed server
	s := miniredis.RunT(t)
	client := redis.NewClient(&redis.Options{Addr: s.Addr()})
	defer client.Close()

	s.Close()

	checker := health.NewRedisChecker(client, "redis")
	err := checker.Check(context.Background())
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "redis ping failed")
}

func TestReadinessHandler_MultipleCheckers_OneFailsReturnUnavailable(t *testing.T) {
	h := health.New("v1.0.0")
	h.RegisterChecker(&testChecker{name: "db", err: nil})
	h.RegisterChecker(&testChecker{name: "cache", err: errors.New("cache down")})

	rr := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/readiness", nil)
	h.ReadinessHandler().ServeHTTP(rr, req)

	// Readiness requires ALL checks to pass
	assert.Equal(t, http.StatusServiceUnavailable, rr.Code)
}

// ==================== LivenessHandler Tests ====================

func TestLivenessHandler_ReturnsOK_Always(t *testing.T) {
	h := health.New("v1.0.0")
	// Register a failing checker — liveness must ignore it
	h.RegisterChecker(&testChecker{name: "db", err: errors.New("db down")})

	rr := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/healthz", nil)
	h.LivenessHandler().ServeHTTP(rr, req)

	assert.Equal(t, http.StatusOK, rr.Code)

	var resp health.Response
	err := json.NewDecoder(rr.Body).Decode(&resp)
	require.NoError(t, err)
	assert.Equal(t, health.StatusOK, resp.Status)
	// Liveness MUST NOT run checkers — Checks map must be empty
	assert.Empty(t, resp.Checks)
}

func TestLivenessHandler_ResponseContainsUptimeAndVersion(t *testing.T) {
	h := health.New("v1.24.0")

	rr := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/healthz", nil)
	h.LivenessHandler().ServeHTTP(rr, req)

	assert.Equal(t, http.StatusOK, rr.Code)

	var resp health.Response
	err := json.NewDecoder(rr.Body).Decode(&resp)
	require.NoError(t, err)
	assert.Equal(t, "v1.24.0", resp.Version)
	assert.NotEmpty(t, resp.Uptime)
	assert.NotEmpty(t, resp.Timestamp)
}

// ==================== KafkaChecker Tests ====================

func TestKafkaChecker_ReachableBroker_ReturnsNil(t *testing.T) {
	// Start a real TCP listener on a random port
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	require.NoError(t, err)
	defer ln.Close()

	checker := health.NewKafkaChecker([]string{ln.Addr().String()}, "kafka")
	assert.Equal(t, "kafka", checker.Name())

	ctx := context.Background()
	assert.NoError(t, checker.Check(ctx))
}

func TestKafkaChecker_UnreachableBroker_ReturnsError(t *testing.T) {
	// Port 19999 should not be listening
	checker := health.NewKafkaChecker([]string{"localhost:19999"}, "kafka")

	ctx := context.Background()
	err := checker.Check(ctx)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "kafka broker unreachable")
}

func TestKafkaChecker_EmptyBrokers_ReturnsNil(t *testing.T) {
	checker := health.NewKafkaChecker([]string{}, "kafka")

	ctx := context.Background()
	assert.NoError(t, checker.Check(ctx))
}
