package kafka

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	"github.com/4ubak/cg-shared-libs/logger"
	"github.com/4ubak/cg-shared-libs/metrics"
	"github.com/segmentio/kafka-go"
	"go.uber.org/zap"
)

const (
	defaultMaxRetries = 5
	defaultBackoffMin = 100 * time.Millisecond
	defaultBackoffMax = 30 * time.Second
)

// retryConfig holds resolved retry/backoff parameters for a consumer.
type retryConfig struct {
	maxRetries int
	backoffMin time.Duration
	backoffMax time.Duration
}

// newRetryConfig builds a retryConfig from Config, applying defaults for
// zero values so callers don't have to set every field explicitly.
func newRetryConfig(cfg Config) retryConfig {
	rc := retryConfig{
		maxRetries: cfg.MaxRetries,
		backoffMin: cfg.BackoffMin,
		backoffMax: cfg.BackoffMax,
	}
	if rc.maxRetries <= 0 {
		rc.maxRetries = defaultMaxRetries
	}
	if rc.backoffMin <= 0 {
		rc.backoffMin = defaultBackoffMin
	}
	if rc.backoffMax <= 0 {
		rc.backoffMax = defaultBackoffMax
	}
	return rc
}

// calcBackoff returns the delay for attempt n (0-indexed).
// delay = min(backoffMin * 2^n, backoffMax)
func (rc retryConfig) calcBackoff(attempt int) time.Duration {
	if attempt <= 0 {
		return rc.backoffMin
	}
	delay := rc.backoffMin
	for i := 0; i < attempt; i++ {
		delay *= 2
		if delay >= rc.backoffMax {
			return rc.backoffMax
		}
	}
	return delay
}

// dlqPayload is the envelope written to the dead-letter topic.
type dlqPayload struct {
	OriginalTopic string          `json:"original_topic"`
	OriginalKey   string          `json:"original_key"`
	OriginalValue json.RawMessage `json:"original_value"`
	ErrorString   string          `json:"error"`
	RetryCount    int             `json:"retry_count"`
	FailedAt      time.Time       `json:"failed_at"`
}

type dlqPublisher interface {
	PublishJSON(ctx context.Context, key string, data any) error
	Close() error
}

// sendToDLQ publishes msg to the DLQ topic. The caller must not commit the
// source offset unless this function returns nil.
func sendToDLQ(
	ctx context.Context,
	prod dlqPublisher,
	dlqTopic string,
	topic string,
	msg kafka.Message,
	handlerErr error,
	retryCount int,
) error {
	payload := dlqPayload{
		OriginalTopic: topic,
		OriginalKey:   string(msg.Key),
		OriginalValue: json.RawMessage(msg.Value),
		ErrorString:   handlerErr.Error(),
		RetryCount:    retryCount,
		FailedAt:      time.Now().UTC(),
	}

	if err := prod.PublishJSON(ctx, string(msg.Key), payload); err != nil {
		return fmt.Errorf("publish to DLQ: %w", err)
	}

	logger.Warn("kafka message moved to DLQ",
		zap.String("original_topic", topic),
		zap.String("dlq_topic", dlqTopic),
		zap.Int64("offset", msg.Offset),
		zap.Int("retry_count", retryCount),
		zap.String("error", handlerErr.Error()),
	)
	return nil
}

func (c *Consumer) sendToDLQUntilSuccess(
	ctx context.Context,
	msg kafka.Message,
	handlerErr error,
) error {
	retainingOffset := false
	attempt := 0
	defer func() {
		if retainingOffset {
			metrics.ReleaseKafkaConsumerOffset(c.topic, c.groupID)
		}
	}()

	for {
		err := sendToDLQ(
			ctx, c.dlqProducer, c.dlqTopic, c.topic, msg, handlerErr, c.retryCfg.maxRetries,
		)
		if err == nil {
			metrics.RecordKafkaDLQ(c.topic, c.groupID)
			return nil
		}

		if !retainingOffset {
			metrics.RetainKafkaConsumerOffset(c.topic, c.groupID)
			retainingOffset = true
		}
		delay := c.retryCfg.calcBackoff(attempt)
		attempt++
		metrics.RecordKafkaConsumerRetainedRetry(c.topic, c.groupID)
		fields := []zap.Field{
			zap.Error(err),
			zap.String("topic", c.topic),
			zap.String("dlq_topic", c.dlqTopic),
			zap.String("consumer_group", c.groupID),
			zap.Int("partition", msg.Partition),
			zap.Int64("offset", msg.Offset),
			zap.Int("attempt", attempt),
			zap.Duration("backoff", delay),
		}
		if attempt == 1 || attempt%10 == 0 {
			logger.Error("kafka DLQ unavailable; source offset retained", fields...)
		} else {
			logger.Debug("kafka DLQ retained-offset retry", fields...)
		}

		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(delay):
		}
	}
}

// handleWithRetry calls handler for msg, retrying on transient errors with
// exponential backoff. It returns:
//   - (true, nil)  — handler succeeded; caller should commit the offset.
//   - (true, err)  — max retries exhausted or DLQ path taken; caller should commit.
//   - (false, err) — context cancelled; caller should propagate.
//
// Errors marked with RetryUntilCanceled bypass finite retry/DLQ handling and
// return (false, ctx.Err()) only when the consumer is shutting down.
//
// The boolean indicates "commit the offset".
func (c *Consumer) handleWithRetry(
	ctx context.Context,
	msg kafka.Message,
	handler MessageHandler,
) (commit bool, err error) {
	normalRetries := 0
	nonCommittableRetries := 0
	retainingOffset := false
	defer func() {
		if retainingOffset {
			metrics.ReleaseKafkaConsumerOffset(c.topic, c.groupID)
		}
	}()
	for {
		handlerErr := handler(ctx, msg)
		if handlerErr == nil {
			// Success — reset happens implicitly (no state to reset per-message).
			return true, nil
		}

		if IsRetryUntilCanceled(handlerErr) {
			if !retainingOffset {
				metrics.RetainKafkaConsumerOffset(c.topic, c.groupID)
				retainingOffset = true
			}
			delay := c.retryCfg.calcBackoff(nonCommittableRetries)
			nonCommittableRetries++
			metrics.RecordKafkaConsumerRetainedRetry(c.topic, c.groupID)
			fields := []zap.Field{
				zap.Error(handlerErr),
				zap.String("topic", c.topic),
				zap.String("consumer_group", c.groupID),
				zap.Int("partition", msg.Partition),
				zap.Int64("offset", msg.Offset),
				zap.Int("attempt", nonCommittableRetries),
				zap.Duration("backoff", delay),
			}
			if nonCommittableRetries == 1 || nonCommittableRetries%10 == 0 {
				logger.Error("kafka handler blocked by required dependency; offset retained", fields...)
			} else {
				logger.Debug("kafka retained-offset retry", fields...)
			}

			select {
			case <-ctx.Done():
				return false, ctx.Err()
			case <-time.After(delay):
			}
			continue
		}

		if retainingOffset {
			metrics.ReleaseKafkaConsumerOffset(c.topic, c.groupID)
			retainingOffset = false
		}

		// Unmarshal errors are permanent; commit immediately without retrying.
		if IsUnmarshalError(handlerErr) {
			logger.Error("kafka message skipped: unmarshal error (schema mismatch)",
				zap.Error(handlerErr),
				zap.String("topic", c.topic),
				zap.String("consumer_group", c.groupID),
				zap.Int("partition", msg.Partition),
				zap.Int64("offset", msg.Offset),
			)
			metrics.RecordKafkaUnmarshalError(c.topic, c.groupID)
			return true, nil
		}

		// Transient error on a non-final attempt — back off and retry.
		if normalRetries < c.retryCfg.maxRetries {
			delay := c.retryCfg.calcBackoff(normalRetries)
			normalRetries++
			metrics.RecordKafkaConsumerRetry(c.topic, c.groupID)
			logger.Warn("kafka handler error, retrying with backoff",
				zap.Error(handlerErr),
				zap.String("topic", c.topic),
				zap.Int64("offset", msg.Offset),
				zap.Int("attempt", normalRetries),
				zap.Int("max_retries", c.retryCfg.maxRetries),
				zap.Duration("backoff", delay),
			)

			select {
			case <-ctx.Done():
				return false, ctx.Err()
			case <-time.After(delay):
			}
			continue
		}

		// Max retries exhausted.
		logger.Error("kafka message handler failed after max retries",
			zap.Error(handlerErr),
			zap.String("topic", c.topic),
			zap.Int64("offset", msg.Offset),
			zap.Int("retry_count", c.retryCfg.maxRetries),
		)

		if c.dlqProducer != nil {
			if err := c.sendToDLQUntilSuccess(ctx, msg, handlerErr); err != nil {
				return false, err
			}
		} else {
			metrics.RecordKafkaDLQ(c.topic, c.groupID)
		}

		// Return the handler error so the caller has the original cause, but
		// still commit so we do not replay the message indefinitely.
		return true, fmt.Errorf("max retries (%d) exhausted: %w", c.retryCfg.maxRetries, handlerErr)
	}
}
