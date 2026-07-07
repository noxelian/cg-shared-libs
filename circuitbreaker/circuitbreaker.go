package circuitbreaker

import (
	"context"
	"errors"
	"sync"
	"time"

	"github.com/4ubak/cg-shared-libs/logger"
	"github.com/4ubak/cg-shared-libs/metrics"
	"go.uber.org/zap"
)

// State represents circuit breaker state
type State int

const (
	StateClosed State = iota
	StateOpen
	StateHalfOpen
)

func (s State) String() string {
	switch s {
	case StateClosed:
		return "closed"
	case StateOpen:
		return "open"
	case StateHalfOpen:
		return "half-open"
	default:
		return "unknown"
	}
}

var (
	ErrCircuitOpen = errors.New("circuit breaker is open")
)

// Config holds circuit breaker configuration
type Config struct {
	Name             string        // Name for logging
	MaxFailures      int           // Max failures before opening (default: 5)
	Timeout          time.Duration // Time to wait before half-open (default: 30s)
	MaxHalfOpenCalls int           // Max calls in half-open state (default: 3)
	// IsFailure classifies an error as a service-health failure. Errors it
	// rejects (returns false) count as successes: the callee answered, so the
	// breaker must not trip — e.g. a per-caller rate limit or bad argument.
	// nil means every error is a failure.
	IsFailure func(err error) bool
}

// CircuitBreaker implements the circuit breaker pattern
type CircuitBreaker struct {
	name             string
	maxFailures      int
	timeout          time.Duration
	maxHalfOpenCalls int
	isFailure        func(err error) bool

	mu               sync.RWMutex
	state            State
	failures         int
	successes        int
	halfOpenCalls    int
	lastFailureTime  time.Time
}

// New creates a new circuit breaker
func New(cfg Config) *CircuitBreaker {
	if cfg.MaxFailures <= 0 {
		cfg.MaxFailures = 5
	}
	if cfg.Timeout <= 0 {
		cfg.Timeout = 30 * time.Second
	}
	if cfg.MaxHalfOpenCalls <= 0 {
		cfg.MaxHalfOpenCalls = 3
	}
	if cfg.IsFailure == nil {
		cfg.IsFailure = func(error) bool { return true }
	}

	cb := &CircuitBreaker{
		name:             cfg.Name,
		maxFailures:      cfg.MaxFailures,
		timeout:          cfg.Timeout,
		maxHalfOpenCalls: cfg.MaxHalfOpenCalls,
		isFailure:        cfg.IsFailure,
		state:            StateClosed,
	}
	metrics.SetCircuitBreakerState(cb.name, int(StateClosed))
	return cb
}

// Execute runs the given function with circuit breaker protection
func (cb *CircuitBreaker) Execute(ctx context.Context, fn func(ctx context.Context) error) error {
	if err := cb.beforeRequest(); err != nil {
		return err
	}

	err := fn(ctx)
	cb.afterRequest(err)
	return err
}

func (cb *CircuitBreaker) beforeRequest() error {
	cb.mu.Lock()
	defer cb.mu.Unlock()

	switch cb.state {
	case StateClosed:
		return nil

	case StateOpen:
		// Check if timeout has passed
		if time.Since(cb.lastFailureTime) > cb.timeout {
			cb.toHalfOpen()
			return nil
		}
		logger.Debug("circuit breaker open",
			zap.String("name", cb.name),
			zap.Duration("retry_after", cb.timeout-time.Since(cb.lastFailureTime)),
		)
		return ErrCircuitOpen

	case StateHalfOpen:
		if cb.halfOpenCalls >= cb.maxHalfOpenCalls {
			return ErrCircuitOpen
		}
		cb.halfOpenCalls++
		return nil
	}

	return nil
}

func (cb *CircuitBreaker) afterRequest(err error) {
	cb.mu.Lock()
	defer cb.mu.Unlock()

	if err != nil && cb.isFailure(err) {
		cb.onFailure()
	} else {
		cb.onSuccess()
	}
}

func (cb *CircuitBreaker) onSuccess() {
	metrics.RecordCircuitBreakerSuccess(cb.name)
	switch cb.state {
	case StateClosed:
		cb.failures = 0

	case StateHalfOpen:
		cb.successes++
		if cb.successes >= cb.maxHalfOpenCalls {
			cb.toClosed()
		}
	}
}

func (cb *CircuitBreaker) onFailure() {
	metrics.RecordCircuitBreakerFailure(cb.name)
	switch cb.state {
	case StateClosed:
		cb.failures++
		cb.lastFailureTime = time.Now()
		if cb.failures >= cb.maxFailures {
			cb.toOpen()
		}

	case StateHalfOpen:
		cb.toOpen()
	}
}

func (cb *CircuitBreaker) toClosed() {
	cb.state = StateClosed
	cb.failures = 0
	cb.successes = 0
	cb.halfOpenCalls = 0
	metrics.SetCircuitBreakerState(cb.name, int(StateClosed))
	logger.Info("circuit breaker closed", zap.String("name", cb.name))
}

func (cb *CircuitBreaker) toOpen() {
	cb.state = StateOpen
	cb.lastFailureTime = time.Now()
	metrics.SetCircuitBreakerState(cb.name, int(StateOpen))
	logger.Warn("circuit breaker opened",
		zap.String("name", cb.name),
		zap.Int("failures", cb.failures),
	)
}

func (cb *CircuitBreaker) toHalfOpen() {
	cb.state = StateHalfOpen
	cb.successes = 0
	cb.halfOpenCalls = 0
	metrics.SetCircuitBreakerState(cb.name, int(StateHalfOpen))
	logger.Info("circuit breaker half-open", zap.String("name", cb.name))
}

// State returns current state
func (cb *CircuitBreaker) State() State {
	cb.mu.RLock()
	defer cb.mu.RUnlock()
	return cb.state
}

// Reset resets the circuit breaker to closed state
func (cb *CircuitBreaker) Reset() {
	cb.mu.Lock()
	defer cb.mu.Unlock()
	cb.toClosed()
}
