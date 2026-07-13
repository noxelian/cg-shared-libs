package kafka

import (
	"context"
	"encoding/json"
	"errors"
	"sync/atomic"
	"testing"
	"time"

	"github.com/segmentio/kafka-go"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// --- retryConfig helpers ---

func TestNewRetryConfig_Defaults(t *testing.T) {
	rc := newRetryConfig(Config{})

	assert.Equal(t, defaultMaxRetries, rc.maxRetries)
	assert.Equal(t, defaultBackoffMin, rc.backoffMin)
	assert.Equal(t, defaultBackoffMax, rc.backoffMax)
}

func TestNewRetryConfig_CustomValues(t *testing.T) {
	rc := newRetryConfig(Config{
		MaxRetries: 3,
		BackoffMin: 50 * time.Millisecond,
		BackoffMax: 5 * time.Second,
	})

	assert.Equal(t, 3, rc.maxRetries)
	assert.Equal(t, 50*time.Millisecond, rc.backoffMin)
	assert.Equal(t, 5*time.Second, rc.backoffMax)
}

func TestNewRetryConfig_NegativeMaxRetriesUsesDefault(t *testing.T) {
	rc := newRetryConfig(Config{MaxRetries: -1})
	assert.Equal(t, defaultMaxRetries, rc.maxRetries)
}

// --- calcBackoff ---

func TestCalcBackoff_FirstAttempt(t *testing.T) {
	rc := retryConfig{
		backoffMin: 100 * time.Millisecond,
		backoffMax: 30 * time.Second,
	}
	assert.Equal(t, 100*time.Millisecond, rc.calcBackoff(0))
}

func TestCalcBackoff_Doubles(t *testing.T) {
	rc := retryConfig{
		backoffMin: 100 * time.Millisecond,
		backoffMax: 30 * time.Second,
	}
	assert.Equal(t, 100*time.Millisecond, rc.calcBackoff(0))
	assert.Equal(t, 200*time.Millisecond, rc.calcBackoff(1))
	assert.Equal(t, 400*time.Millisecond, rc.calcBackoff(2))
	assert.Equal(t, 800*time.Millisecond, rc.calcBackoff(3))
}

func TestCalcBackoff_CappedAtMax(t *testing.T) {
	rc := retryConfig{
		backoffMin: 100 * time.Millisecond,
		backoffMax: 300 * time.Millisecond,
	}
	// 100 -> 200 -> would be 400 but capped at 300
	assert.Equal(t, 300*time.Millisecond, rc.calcBackoff(2))
	assert.Equal(t, 300*time.Millisecond, rc.calcBackoff(10))
}

func TestCalcBackoff_NegativeAttempt(t *testing.T) {
	rc := retryConfig{
		backoffMin: 100 * time.Millisecond,
		backoffMax: 30 * time.Second,
	}
	assert.Equal(t, 100*time.Millisecond, rc.calcBackoff(-5))
}

// --- handleWithRetry ---

// makeConsumer builds a minimal Consumer with an extremely short backoff so
// tests complete quickly without hitting Kafka.
func makeConsumer(maxRetries int, dlqProd *Producer) *Consumer {
	return &Consumer{
		topic:   "test.topic",
		groupID: "test-group",
		retryCfg: retryConfig{
			maxRetries: maxRetries,
			backoffMin: time.Millisecond,
			backoffMax: 5 * time.Millisecond,
		},
		dlqProducer: dlqProd,
	}
}

func makeMsg(value string) kafka.Message {
	return kafka.Message{
		Topic:     "test.topic",
		Partition: 0,
		Offset:    42,
		Key:       []byte("k1"),
		Value:     []byte(value),
	}
}

func TestHandleWithRetry_SuccessOnFirstAttempt(t *testing.T) {
	c := makeConsumer(3, nil)
	msg := makeMsg(`{"id":"1"}`)

	commit, err := c.handleWithRetry(context.Background(), msg, func(_ context.Context, _ kafka.Message) error {
		return nil
	})

	assert.True(t, commit)
	assert.NoError(t, err)
}

func TestHandleWithRetry_UnmarshalErrorCommitsImmediately(t *testing.T) {
	c := makeConsumer(3, nil)
	msg := makeMsg(`bad json`)

	var calls int
	commit, err := c.handleWithRetry(context.Background(), msg, func(_ context.Context, _ kafka.Message) error {
		calls++
		return NewUnmarshalError(errors.New("unexpected end of JSON"))
	})

	assert.True(t, commit)
	assert.NoError(t, err)
	assert.Equal(t, 1, calls, "unmarshal errors must not be retried")
}

func TestHandleWithRetry_RetriesTransientError(t *testing.T) {
	c := makeConsumer(3, nil)
	msg := makeMsg(`{"id":"1"}`)

	var calls int32
	commit, err := c.handleWithRetry(context.Background(), msg, func(_ context.Context, _ kafka.Message) error {
		n := atomic.AddInt32(&calls, 1)
		if n < 3 {
			return errors.New("db connection refused")
		}
		return nil
	})

	assert.True(t, commit)
	assert.NoError(t, err)
	assert.Equal(t, int32(3), calls)
}

func TestHandleWithRetry_ExhaustsRetriesReturnsError(t *testing.T) {
	c := makeConsumer(3, nil)
	msg := makeMsg(`{"id":"1"}`)

	transient := errors.New("db timeout")
	var calls int32
	commit, err := c.handleWithRetry(context.Background(), msg, func(_ context.Context, _ kafka.Message) error {
		atomic.AddInt32(&calls, 1)
		return transient
	})

	// maxRetries=3 means 1 initial call + 3 retries = 4 total calls
	assert.True(t, commit, "offset must be committed after exhausting retries")
	require.Error(t, err)
	assert.ErrorIs(t, err, transient)
	assert.Equal(t, int32(4), calls)
}

func TestHandleWithRetry_ContextCancelledDuringBackoff(t *testing.T) {
	c := makeConsumer(5, nil)
	// Use a longer backoff so context cancellation wins the race.
	c.retryCfg.backoffMin = 200 * time.Millisecond
	c.retryCfg.backoffMax = 200 * time.Millisecond

	msg := makeMsg(`{"id":"1"}`)
	ctx, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
	defer cancel()

	commit, err := c.handleWithRetry(ctx, msg, func(_ context.Context, _ kafka.Message) error {
		return errors.New("transient")
	})

	assert.False(t, commit)
	assert.ErrorIs(t, err, context.DeadlineExceeded)
}

func TestHandleWithRetry_RetryUntilCanceledDoesNotCommitOrDLQ(t *testing.T) {
	c := makeConsumer(1, nil)
	c.retryCfg.backoffMin = time.Millisecond
	c.retryCfg.backoffMax = time.Millisecond
	msg := makeMsg(`{"id":"1"}`)
	dependencyErr := errors.New("redis unavailable")
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Millisecond)
	defer cancel()

	var calls int32
	commit, err := c.handleWithRetry(ctx, msg, func(_ context.Context, _ kafka.Message) error {
		atomic.AddInt32(&calls, 1)
		return RetryUntilCanceled(dependencyErr)
	})

	assert.False(t, commit)
	assert.ErrorIs(t, err, context.DeadlineExceeded)
	assert.Greater(t, calls, int32(c.retryCfg.maxRetries+1))
}

func TestHandleWithRetry_RetryUntilCanceledThenSuccessCommits(t *testing.T) {
	c := makeConsumer(1, nil)
	msg := makeMsg(`{"id":"1"}`)
	var calls int32

	commit, err := c.handleWithRetry(context.Background(), msg, func(_ context.Context, _ kafka.Message) error {
		if atomic.AddInt32(&calls, 1) < 4 {
			return RetryUntilCanceled(errors.New("redis unavailable"))
		}
		return nil
	})

	assert.True(t, commit)
	assert.NoError(t, err)
	assert.Equal(t, int32(4), calls)
}

func TestHandleWithRetry_RetryUntilCanceledOverridesUnmarshalDisposition(t *testing.T) {
	c := makeConsumer(1, nil)
	c.retryCfg.backoffMin = time.Millisecond
	c.retryCfg.backoffMax = time.Millisecond
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Millisecond)
	defer cancel()

	commit, err := c.handleWithRetry(ctx, makeMsg(`bad json`), func(_ context.Context, _ kafka.Message) error {
		return RetryUntilCanceled(NewUnmarshalError(errors.New("dependency blocked decode")))
	})

	assert.False(t, commit)
	assert.ErrorIs(t, err, context.DeadlineExceeded)
}

// --- DLQ payload marshalling ---

func TestDLQPayload_MarshalRoundtrip(t *testing.T) {
	original := dlqPayload{
		OriginalTopic: "orders.created",
		OriginalKey:   "order-123",
		OriginalValue: json.RawMessage(`{"order_id":"123"}`),
		ErrorString:   "db: connection refused",
		RetryCount:    5,
		FailedAt:      time.Date(2026, 3, 28, 12, 0, 0, 0, time.UTC),
	}

	data, err := json.Marshal(original)
	require.NoError(t, err)

	var decoded dlqPayload
	require.NoError(t, json.Unmarshal(data, &decoded))

	assert.Equal(t, original.OriginalTopic, decoded.OriginalTopic)
	assert.Equal(t, original.OriginalKey, decoded.OriginalKey)
	assert.Equal(t, original.ErrorString, decoded.ErrorString)
	assert.Equal(t, original.RetryCount, decoded.RetryCount)
	assert.JSONEq(t, string(original.OriginalValue), string(decoded.OriginalValue))
}

// --- Config DLQ flag ---

func TestConfig_DLQDisabledByDefault(t *testing.T) {
	cfg := Config{}
	assert.False(t, cfg.DLQEnabled)
}

func TestNewRetryConfig_MaxRetriesZeroUsesDefault(t *testing.T) {
	rc := newRetryConfig(Config{MaxRetries: 0})
	assert.Equal(t, defaultMaxRetries, rc.maxRetries)
}

// --- HandleWithRetry calls handler exactly maxRetries+1 times on failure ---

func TestHandleWithRetry_CallCountMatchesMaxRetries(t *testing.T) {
	for _, maxRetries := range []int{1, 2, 5} {
		maxRetries := maxRetries
		t.Run("", func(t *testing.T) {
			c := makeConsumer(maxRetries, nil)
			msg := makeMsg(`{}`)

			var calls int32
			c.handleWithRetry(context.Background(), msg, func(_ context.Context, _ kafka.Message) error { //nolint:errcheck
				atomic.AddInt32(&calls, 1)
				return errors.New("always fails")
			})

			assert.Equal(t, int32(maxRetries+1), calls,
				"expected 1 initial call + %d retries", maxRetries)
		})
	}
}
