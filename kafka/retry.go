package kafka

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	"github.com/segmentio/kafka-go"
	"gitlab.com/xakpro/cg-shared-libs/logger"
	"gitlab.com/xakpro/cg-shared-libs/metrics"
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

// sendToDLQ publishes msg to the DLQ topic. If the publish fails the error
// is logged at error level and the caller should still commit the offset so
// the consumer is not permanently stalled.
func sendToDLQ(
	ctx context.Context,
	prod *Producer,
	topic string,
	msg kafka.Message,
	handlerErr error,
	retryCount int,
) {
	payload := dlqPayload{
		OriginalTopic: topic,
		OriginalKey:   string(msg.Key),
		OriginalValue: json.RawMessage(msg.Value),
		ErrorString:   handlerErr.Error(),
		RetryCount:    retryCount,
		FailedAt:      time.Now().UTC(),
	}

	if err := prod.PublishJSON(ctx, string(msg.Key), payload); err != nil {
		logger.Error("kafka DLQ publish failed — committing offset and continuing",
			zap.Error(err),
			zap.String("original_topic", topic),
			zap.Int64("offset", msg.Offset),
			zap.Int("retry_count", retryCount),
		)
		return
	}

	logger.Warn("kafka message moved to DLQ",
		zap.String("original_topic", topic),
		zap.String("dlq_topic", prod.topic),
		zap.Int64("offset", msg.Offset),
		zap.Int("retry_count", retryCount),
		zap.String("error", handlerErr.Error()),
	)
}

// handleWithRetry calls handler for msg, retrying on transient errors with
// exponential backoff. It returns:
//   - (true, nil)  — handler succeeded; caller should commit the offset.
//   - (true, err)  — max retries exhausted or DLQ path taken; caller should commit.
//   - (false, err) — context cancelled; caller should propagate.
//
// The boolean indicates "commit the offset".
func (c *Consumer) handleWithRetry(
	ctx context.Context,
	msg kafka.Message,
	handler MessageHandler,
) (commit bool, err error) {
	for attempt := 0; attempt <= c.retryCfg.maxRetries; attempt++ {
		handlerErr := handler(ctx, msg)
		if handlerErr == nil {
			// Success — reset happens implicitly (no state to reset per-message).
			return true, nil
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
		if attempt < c.retryCfg.maxRetries {
			delay := c.retryCfg.calcBackoff(attempt)
			metrics.RecordKafkaConsumerRetry(c.topic, c.groupID)
			logger.Warn("kafka handler error, retrying with backoff",
				zap.Error(handlerErr),
				zap.String("topic", c.topic),
				zap.Int64("offset", msg.Offset),
				zap.Int("attempt", attempt+1),
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
		metrics.RecordKafkaDLQ(c.topic, c.groupID)
		logger.Error("kafka message handler failed after max retries",
			zap.Error(handlerErr),
			zap.String("topic", c.topic),
			zap.Int64("offset", msg.Offset),
			zap.Int("retry_count", c.retryCfg.maxRetries),
		)

		if c.dlqProducer != nil {
			sendToDLQ(ctx, c.dlqProducer, c.topic, msg, handlerErr, c.retryCfg.maxRetries)
		}

		// Return the handler error so the caller has the original cause, but
		// still commit so we do not replay the message indefinitely.
		return true, fmt.Errorf("max retries (%d) exhausted: %w", c.retryCfg.maxRetries, handlerErr)
	}

	// Unreachable — the loop always returns from within.
	return true, nil
}
