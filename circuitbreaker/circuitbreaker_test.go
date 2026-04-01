package circuitbreaker_test

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/4ubak/cg-shared-libs/circuitbreaker"
)

var errTest = errors.New("test error")

func TestNew_DefaultConfig(t *testing.T) {
	cb := circuitbreaker.New(circuitbreaker.Config{Name: "test"})

	require.NotNil(t, cb)
	assert.Equal(t, circuitbreaker.StateClosed, cb.State())
}

func TestNew_CustomConfig(t *testing.T) {
	cb := circuitbreaker.New(circuitbreaker.Config{
		Name:             "custom",
		MaxFailures:      3,
		Timeout:          5 * time.Second,
		MaxHalfOpenCalls: 2,
	})

	require.NotNil(t, cb)
	assert.Equal(t, circuitbreaker.StateClosed, cb.State())
}

func TestExecute_SuccessInClosed(t *testing.T) {
	cb := circuitbreaker.New(circuitbreaker.Config{Name: "test"})

	err := cb.Execute(context.Background(), func(ctx context.Context) error {
		return nil
	})

	assert.NoError(t, err)
	assert.Equal(t, circuitbreaker.StateClosed, cb.State())
}

func TestExecute_FailureCounterIncrement(t *testing.T) {
	cb := circuitbreaker.New(circuitbreaker.Config{
		Name:        "test",
		MaxFailures: 2,
	})

	// First failure: still closed
	err := cb.Execute(context.Background(), func(ctx context.Context) error {
		return errTest
	})
	assert.ErrorIs(t, err, errTest)
	assert.Equal(t, circuitbreaker.StateClosed, cb.State())

	// Second failure: opens the circuit
	err = cb.Execute(context.Background(), func(ctx context.Context) error {
		return errTest
	})
	assert.ErrorIs(t, err, errTest)
	assert.Equal(t, circuitbreaker.StateOpen, cb.State())
}

func TestExecute_OpenRejectsCallsImmediately(t *testing.T) {
	cb := circuitbreaker.New(circuitbreaker.Config{
		Name:        "test",
		MaxFailures: 1,
		Timeout:     10 * time.Second,
	})

	// Trigger open state
	_ = cb.Execute(context.Background(), func(ctx context.Context) error {
		return errTest
	})
	require.Equal(t, circuitbreaker.StateOpen, cb.State())

	// Next call should be rejected without calling fn
	fnCalled := false
	err := cb.Execute(context.Background(), func(ctx context.Context) error {
		fnCalled = true
		return nil
	})

	assert.ErrorIs(t, err, circuitbreaker.ErrCircuitOpen)
	assert.False(t, fnCalled, "fn should not be called when circuit is open")
}

func TestExecute_HalfOpenAfterTimeout(t *testing.T) {
	cb := circuitbreaker.New(circuitbreaker.Config{
		Name:             "test",
		MaxFailures:      1,
		Timeout:          1 * time.Millisecond,
		MaxHalfOpenCalls: 1,
	})

	// Trigger open state
	_ = cb.Execute(context.Background(), func(ctx context.Context) error {
		return errTest
	})
	require.Equal(t, circuitbreaker.StateOpen, cb.State())

	// Wait for timeout to expire
	time.Sleep(5 * time.Millisecond)

	// Next call should transition to half-open and allow the call
	err := cb.Execute(context.Background(), func(ctx context.Context) error {
		return nil
	})

	assert.NoError(t, err)
	assert.Equal(t, circuitbreaker.StateClosed, cb.State())
}

func TestExecute_HalfOpenFailureReturnsToOpen(t *testing.T) {
	cb := circuitbreaker.New(circuitbreaker.Config{
		Name:             "test",
		MaxFailures:      1,
		Timeout:          1 * time.Millisecond,
		MaxHalfOpenCalls: 3,
	})

	// Trigger open state
	_ = cb.Execute(context.Background(), func(ctx context.Context) error {
		return errTest
	})
	require.Equal(t, circuitbreaker.StateOpen, cb.State())

	// Wait for timeout to expire
	time.Sleep(5 * time.Millisecond)

	// Call in half-open that fails should return to open
	err := cb.Execute(context.Background(), func(ctx context.Context) error {
		return errTest
	})

	assert.ErrorIs(t, err, errTest)
	assert.Equal(t, circuitbreaker.StateOpen, cb.State())
}

func TestExecute_HalfOpenMaxCallsExceeded(t *testing.T) {
	cb := circuitbreaker.New(circuitbreaker.Config{
		Name:             "test",
		MaxFailures:      1,
		Timeout:          1 * time.Millisecond,
		MaxHalfOpenCalls: 1,
	})

	// Trigger open state
	_ = cb.Execute(context.Background(), func(ctx context.Context) error {
		return errTest
	})
	require.Equal(t, circuitbreaker.StateOpen, cb.State())

	// Wait for timeout to expire
	time.Sleep(5 * time.Millisecond)

	// First half-open call (fails, so stays open)
	_ = cb.Execute(context.Background(), func(ctx context.Context) error {
		return errTest
	})

	// Circuit should be open again, reject next call immediately
	err := cb.Execute(context.Background(), func(ctx context.Context) error {
		return nil
	})
	assert.ErrorIs(t, err, circuitbreaker.ErrCircuitOpen)
}

func TestState_ReturnsCurrentState(t *testing.T) {
	tests := []struct {
		name     string
		setup    func(cb *circuitbreaker.CircuitBreaker)
		expected circuitbreaker.State
	}{
		{
			name:     "initial state is closed",
			setup:    func(cb *circuitbreaker.CircuitBreaker) {},
			expected: circuitbreaker.StateClosed,
		},
		{
			name: "open after max failures",
			setup: func(cb *circuitbreaker.CircuitBreaker) {
				_ = cb.Execute(context.Background(), func(ctx context.Context) error {
					return errTest
				})
			},
			expected: circuitbreaker.StateOpen,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			cb := circuitbreaker.New(circuitbreaker.Config{
				Name:        "test",
				MaxFailures: 1,
				Timeout:     10 * time.Second,
			})
			tt.setup(cb)
			assert.Equal(t, tt.expected, cb.State())
		})
	}
}

func TestNew_WithDifferentNames(t *testing.T) {
	tests := []struct {
		name   string
		cbName string
	}{
		{name: "simple name", cbName: "my-breaker"},
		{name: "empty name", cbName: ""},
		{name: "name with dashes", cbName: "svc-db-primary"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			cb := circuitbreaker.New(circuitbreaker.Config{Name: tt.cbName})
			require.NotNil(t, cb)
			assert.Equal(t, circuitbreaker.StateClosed, cb.State())
		})
	}
}

func TestState_String(t *testing.T) {
	tests := []struct {
		state    circuitbreaker.State
		expected string
	}{
		{state: circuitbreaker.StateClosed, expected: "closed"},
		{state: circuitbreaker.StateOpen, expected: "open"},
		{state: circuitbreaker.StateHalfOpen, expected: "half-open"},
		{state: circuitbreaker.State(99), expected: "unknown"},
	}

	for _, tt := range tests {
		t.Run(tt.expected, func(t *testing.T) {
			assert.Equal(t, tt.expected, tt.state.String())
		})
	}
}

func TestReset_ReturnsToClosed(t *testing.T) {
	cb := circuitbreaker.New(circuitbreaker.Config{
		Name:        "test",
		MaxFailures: 1,
		Timeout:     10 * time.Second,
	})

	// Open the circuit
	_ = cb.Execute(context.Background(), func(ctx context.Context) error {
		return errTest
	})
	require.Equal(t, circuitbreaker.StateOpen, cb.State())

	// Reset should return to closed
	cb.Reset()
	assert.Equal(t, circuitbreaker.StateClosed, cb.State())

	// Should accept calls again
	err := cb.Execute(context.Background(), func(ctx context.Context) error {
		return nil
	})
	assert.NoError(t, err)
}

func TestExecute_SuccessResetFailureCounter(t *testing.T) {
	cb := circuitbreaker.New(circuitbreaker.Config{
		Name:        "test",
		MaxFailures: 3,
	})

	// Two failures
	_ = cb.Execute(context.Background(), func(ctx context.Context) error { return errTest })
	_ = cb.Execute(context.Background(), func(ctx context.Context) error { return errTest })

	// Success should reset counter
	_ = cb.Execute(context.Background(), func(ctx context.Context) error { return nil })

	// Two more failures should not open (counter was reset)
	_ = cb.Execute(context.Background(), func(ctx context.Context) error { return errTest })
	_ = cb.Execute(context.Background(), func(ctx context.Context) error { return errTest })

	assert.Equal(t, circuitbreaker.StateClosed, cb.State())
}

func TestExecute_ContextPassedToFn(t *testing.T) {
	cb := circuitbreaker.New(circuitbreaker.Config{Name: "test"})

	type ctxKey string
	ctx := context.WithValue(context.Background(), ctxKey("key"), "value")

	err := cb.Execute(ctx, func(receivedCtx context.Context) error {
		val := receivedCtx.Value(ctxKey("key"))
		assert.Equal(t, "value", val)
		return nil
	})

	assert.NoError(t, err)
}
