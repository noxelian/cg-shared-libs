package kafka

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
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
func makeConsumer(maxRetries int, dlqProd dlqPublisher) *Consumer {
	return &Consumer{
		topic:       "test.topic",
		groupID:     "test-group",
		dlqTopic:    "test.topic.dlq",
		dlqProducer: dlqProd,
		retryCfg: retryConfig{
			maxRetries: maxRetries,
			backoffMin: time.Millisecond,
			backoffMax: 5 * time.Millisecond,
		},
	}
}

type fakeDLQPublisher struct {
	failures int
	calls    int
	err      error
	keys     []string
	payloads []any
}

func (p *fakeDLQPublisher) PublishJSON(_ context.Context, key string, payload any) error {
	p.calls++
	if p.calls <= p.failures {
		return p.err
	}
	p.keys = append(p.keys, key)
	p.payloads = append(p.payloads, payload)
	return nil
}

func (p *fakeDLQPublisher) Close() error { return nil }

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

func TestHandleWithRetry_DLQFailureRetainsSourceOffset(t *testing.T) {
	dlqErr := errors.New("DLQ broker unavailable")
	dlq := &fakeDLQPublisher{failures: 1000, err: dlqErr}
	c := makeConsumer(1, dlq)
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Millisecond)
	defer cancel()

	commit, err := c.handleWithRetry(ctx, makeMsg(`{"id":"1"}`), func(context.Context, kafka.Message) error {
		return errors.New("permanent handler failure")
	})

	assert.False(t, commit)
	assert.ErrorIs(t, err, context.DeadlineExceeded)
	assert.Positive(t, dlq.calls)
}

func TestHandleWithRetry_DLQRecoveryAllowsCommit(t *testing.T) {
	dlq := &fakeDLQPublisher{failures: 2, err: errors.New("temporary DLQ failure")}
	c := makeConsumer(1, dlq)
	handlerErr := errors.New("permanent handler failure")

	commit, err := c.handleWithRetry(context.Background(), makeMsg(`{"id":"1"}`), func(context.Context, kafka.Message) error {
		return handlerErr
	})

	assert.True(t, commit)
	assert.ErrorIs(t, err, handlerErr)
	assert.Equal(t, 3, dlq.calls)
}

func TestHandleWithRetry_DLQRedactsOriginalValue(t *testing.T) {
	dlq := &fakeDLQPublisher{}
	c := makeConsumer(0, dlq)
	c.dlqValueRedactor = func(_ context.Context, value []byte) (json.RawMessage, error) {
		assert.JSONEq(t, `{"request_id":"request-1","note":"private"}`, string(value))
		return json.RawMessage(`{"request_id":"request-1"}`), nil
	}
	handlerErr := errors.New("model unavailable")

	commit, err := c.handleWithRetry(
		context.Background(),
		makeMsg(`{"request_id":"request-1","note":"private"}`),
		func(context.Context, kafka.Message) error { return handlerErr },
	)

	assert.True(t, commit)
	assert.ErrorIs(t, err, handlerErr)
	require.Len(t, dlq.payloads, 1)
	payload, ok := dlq.payloads[0].(dlqPayload)
	require.True(t, ok)
	assert.JSONEq(t, `{"request_id":"request-1"}`, string(payload.OriginalValue))
	assert.NotContains(t, string(payload.OriginalValue), "private")
}

func TestHandleWithRetry_DLQRedactsOriginalKey(t *testing.T) {
	dlq := &fakeDLQPublisher{}
	c := makeConsumer(0, dlq)
	c.dlqKeyRedactor = func(_ context.Context, key []byte) ([]byte, error) {
		assert.Equal(t, "private-key", string(key))
		return nil, nil
	}
	msg := makeMsg(`{"request_id":"request-1"}`)
	msg.Key = []byte("private-key")

	commit, err := c.handleWithRetry(
		context.Background(),
		msg,
		func(context.Context, kafka.Message) error { return errors.New("model unavailable") },
	)

	assert.True(t, commit)
	assert.Error(t, err)
	require.Equal(t, []string{""}, dlq.keys)
	require.Len(t, dlq.payloads, 1)
	payload, ok := dlq.payloads[0].(dlqPayload)
	require.True(t, ok)
	assert.Empty(t, payload.OriginalKey)
}

func TestHandleWithRetry_DLQRedactionFailureRetainsSourceOffset(t *testing.T) {
	dlq := &fakeDLQPublisher{}
	c := makeConsumer(0, dlq)
	c.retryCfg.backoffMin = time.Millisecond
	c.retryCfg.backoffMax = time.Millisecond
	c.dlqValueRedactor = func(context.Context, []byte) (json.RawMessage, error) {
		return nil, errors.New("cannot produce safe value")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Millisecond)
	defer cancel()

	commit, err := c.handleWithRetry(ctx, makeMsg(`{"note":"private"}`), func(context.Context, kafka.Message) error {
		return errors.New("model unavailable")
	})

	assert.False(t, commit)
	assert.ErrorIs(t, err, context.DeadlineExceeded)
	assert.Zero(t, dlq.calls, "raw source value must never reach the DLQ publisher")
}

func TestHandleWithRetry_InvalidRedactedJSONRetainsSourceOffset(t *testing.T) {
	dlq := &fakeDLQPublisher{}
	c := makeConsumer(0, dlq)
	c.retryCfg.backoffMin = time.Millisecond
	c.retryCfg.backoffMax = time.Millisecond
	c.dlqValueRedactor = func(context.Context, []byte) (json.RawMessage, error) {
		return json.RawMessage(`not-json`), nil
	}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Millisecond)
	defer cancel()

	commit, err := c.handleWithRetry(ctx, makeMsg(`{"note":"private"}`), func(context.Context, kafka.Message) error {
		return errors.New("model unavailable")
	})

	assert.False(t, commit)
	assert.ErrorIs(t, err, context.DeadlineExceeded)
	assert.Zero(t, dlq.calls, "invalid redacted JSON must never reach the DLQ publisher")
}

func TestHandleWithRetry_CancelledContextStopsBlockedDLQRedactor(t *testing.T) {
	dlq := &fakeDLQPublisher{}
	c := makeConsumer(0, dlq)
	started := make(chan struct{})
	c.dlqValueRedactor = func(ctx context.Context, _ []byte) (json.RawMessage, error) {
		close(started)
		<-ctx.Done()
		return nil, ctx.Err()
	}
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() {
		defer close(done)
		commit, err := c.handleWithRetry(ctx, makeMsg(`{"note":"private"}`), func(context.Context, kafka.Message) error {
			return errors.New("model unavailable")
		})
		assert.False(t, commit)
		assert.ErrorIs(t, err, context.Canceled)
	}()

	<-started
	cancel()
	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("consumer did not stop after redactor context cancellation")
	}
	assert.Zero(t, dlq.calls)
}

func TestWithDLQValueRedactorConfiguresConsumer(t *testing.T) {
	redactor := func(_ context.Context, value []byte) (json.RawMessage, error) {
		return json.RawMessage(value), nil
	}
	consumer := NewConsumerWithOptions(
		Config{Brokers: []string{"localhost:9092"}},
		"test.topic",
		WithDLQValueRedactor(redactor),
	)
	t.Cleanup(func() { _ = consumer.Close() })

	require.NotNil(t, consumer.dlqValueRedactor)
}

func TestEventDecodeErrorDispositionIsOptIn(t *testing.T) {
	tests := []struct {
		name          string
		options       []ConsumerOption
		wantUnmarshal bool
	}{
		{
			name:          "default preserves immediate commit behavior",
			wantUnmarshal: true,
		},
		{
			name: "opt in routes through retry and DLQ",
			options: []ConsumerOption{WithEventDecodeErrorsToDLQ(
				func(_ context.Context, value []byte) (json.RawMessage, error) {
					return json.RawMessage(value), nil
				},
			)},
			wantUnmarshal: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			consumer := &Consumer{}
			for _, option := range tt.options {
				option(consumer)
			}

			_, err := consumer.decodeEvent([]byte(`{"private_note":"do not echo"`))
			if err == nil {
				t.Fatal("expected decode error")
			}
			if got := IsUnmarshalError(err); got != tt.wantUnmarshal {
				t.Fatalf("unmarshal disposition = %t, want %t: %v", got, tt.wantUnmarshal, err)
			}
			if strings.Contains(err.Error(), "do not echo") {
				t.Fatalf("decode error leaked source value: %v", err)
			}
		})
	}
}

func TestWithEventDecodeErrorsToDLQFailsClosedWithoutValueRedactor(t *testing.T) {
	consumer := &Consumer{}
	WithEventDecodeErrorsToDLQ(nil)(consumer)
	consumer.finalizeOptions()

	require.NotNil(t, consumer.dlqValueRedactor)
	require.NotNil(t, consumer.dlqKeyRedactor)
	_, err := consumer.dlqValue(context.Background(), []byte(`{"note":"private"}`))
	require.Error(t, err)
	key, err := consumer.dlqKey(context.Background(), []byte("private-key"))
	require.NoError(t, err)
	assert.Empty(t, key)
}

func TestWithEventDecodeErrorsToDLQIsIndependentOfOptionOrder(t *testing.T) {
	safeValue := func(context.Context, []byte) (json.RawMessage, error) {
		return json.RawMessage(`{"type":"invalid","data":{}}`), nil
	}
	consumer := &Consumer{}
	WithEventDecodeErrorsToDLQ(safeValue)(consumer)
	WithDLQValueRedactor(nil)(consumer)
	WithDLQKeyRedactor(nil)(consumer)
	consumer.finalizeOptions()

	value, err := consumer.dlqValue(context.Background(), []byte(`{"note":"private"}`))
	require.NoError(t, err)
	assert.JSONEq(t, `{"type":"invalid","data":{}}`, string(value))
	key, err := consumer.dlqKey(context.Background(), []byte("private-key"))
	require.NoError(t, err)
	assert.Empty(t, key)
}

func TestEventDecodeErrorsToDLQDoNotLeakMalformedFields(t *testing.T) {
	dlq := &fakeDLQPublisher{}
	c := makeConsumer(0, dlq)
	WithEventDecodeErrorsToDLQ(func(context.Context, []byte) (json.RawMessage, error) {
		return json.RawMessage(`{"type":"invalid","data":{}}`), nil
	})(c)
	c.finalizeOptions()
	msg := makeMsg(`{"type":"request.created","timestamp":"private timestamp","data":{}}`)
	msg.Key = []byte("private key")

	_, decodeErr := c.decodeEvent(msg.Value)
	require.Error(t, decodeErr)
	assert.NotContains(t, decodeErr.Error(), "private timestamp")

	commit, err := c.handleWithRetry(context.Background(), msg, func(context.Context, kafka.Message) error {
		return decodeErr
	})
	assert.True(t, commit)
	assert.Error(t, err)
	require.Equal(t, []string{""}, dlq.keys)
	require.Len(t, dlq.payloads, 1)
	payload, ok := dlq.payloads[0].(dlqPayload)
	require.True(t, ok)
	assert.Empty(t, payload.OriginalKey)
	assert.JSONEq(t, `{"type":"invalid","data":{}}`, string(payload.OriginalValue))
	assert.Equal(t, "event envelope decode failed", payload.ErrorString)
	encoded, marshalErr := json.Marshal(payload)
	require.NoError(t, marshalErr)
	assert.NotContains(t, string(encoded), "private timestamp")
	assert.NotContains(t, string(encoded), "private key")
}

func TestHandleWithRetry_RetryUntilCanceledDoesNotCommitOrDLQ(t *testing.T) {
	c := makeConsumer(1, nil)
	c.retryCfg.backoffMin = time.Millisecond
	c.retryCfg.backoffMax = time.Millisecond
	msg := makeMsg(`{"id":"1"}`)
	dependencyErr := errors.New("redis unavailable")
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	var calls int32
	commit, err := c.handleWithRetry(ctx, msg, func(_ context.Context, _ kafka.Message) error {
		if atomic.AddInt32(&calls, 1) == 4 {
			cancel()
		}
		return RetryUntilCanceled(dependencyErr)
	})

	assert.False(t, commit)
	assert.ErrorIs(t, err, context.Canceled)
	assert.Equal(t, int32(4), calls)
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

// --- DLQ payload marshaling ---

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
			c.handleWithRetry(context.Background(), msg, func(_ context.Context, _ kafka.Message) error { //nolint:errcheck // The test asserts attempt count, not the expected terminal error.
				atomic.AddInt32(&calls, 1)
				return errors.New("always fails")
			})

			assert.Equal(t, int32(maxRetries+1), calls,
				"expected 1 initial call + %d retries", maxRetries)
		})
	}
}
