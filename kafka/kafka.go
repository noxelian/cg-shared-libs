package kafka

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/4ubak/cg-shared-libs/logger"
	"github.com/4ubak/cg-shared-libs/metrics"
	"github.com/segmentio/kafka-go"
	"go.uber.org/zap"
)

// FlexibleTime is a time.Time wrapper that can unmarshal from multiple formats:
// - RFC3339 string (e.g., "2006-01-02T15:04:05Z07:00")
// - Unix timestamp as number (int64 or float64)
// - Unix timestamp as string
type FlexibleTime struct {
	time.Time
}

// UnmarshalJSON implements json.Unmarshaler for FlexibleTime
func (ft *FlexibleTime) UnmarshalJSON(data []byte) error {
	// Try to unmarshal as string first (RFC3339 format)
	var str string
	if err := json.Unmarshal(data, &str); err == nil {
		// Try RFC3339 format
		t, err := time.Parse(time.RFC3339, str)
		if err == nil {
			ft.Time = t
			return nil
		}
		// Try RFC3339Nano format
		t, err = time.Parse(time.RFC3339Nano, str)
		if err == nil {
			ft.Time = t
			return nil
		}
		// Try Unix timestamp as string
		unix, err := strconv.ParseInt(str, 10, 64)
		if err == nil {
			ft.Time = time.Unix(unix, 0)
			return nil
		}
		return fmt.Errorf("failed to parse time string: %q", str)
	}

	// Try to unmarshal as number (Unix timestamp)
	var num float64
	if err := json.Unmarshal(data, &num); err == nil {
		ft.Time = time.Unix(int64(num), 0)
		return nil
	}

	return fmt.Errorf("time value is not a string or number: %q", string(data))
}

// MarshalJSON implements json.Marshaler for FlexibleTime
func (ft FlexibleTime) MarshalJSON() ([]byte, error) {
	return json.Marshal(ft.Time.Format(time.RFC3339))
}

// UnmarshalError signals that a Kafka message could not be decoded due to a
// schema mismatch. Returning this from a MessageHandler causes the consumer to:
//   - log at error level with topic, partition, offset, and error details
//   - increment the kafka_consumer_unmarshal_errors_total Prometheus counter
//   - commit the offset (the message will never parse correctly — retrying is pointless)
//
// Usage:
//
//	if err := json.Unmarshal(msg.Value, &payload); err != nil {
//	    return kafka.NewUnmarshalError(err)
//	}
type UnmarshalError struct {
	cause error
}

// NewUnmarshalError wraps an underlying decode error as an UnmarshalError.
func NewUnmarshalError(cause error) *UnmarshalError {
	return &UnmarshalError{cause: cause}
}

func (e *UnmarshalError) Error() string {
	return fmt.Sprintf("unmarshal error: %v", e.cause)
}

func (e *UnmarshalError) Unwrap() error {
	return e.cause
}

// IsUnmarshalError reports whether err is (or wraps) an *UnmarshalError.
func IsUnmarshalError(err error) bool {
	var target *UnmarshalError
	return errors.As(err, &target)
}

// retryUntilCanceledError marks an infrastructure failure for which committing
// the offset would lose a valid message. The consumer retries it with bounded
// backoff until the handler succeeds or the consumer context is cancelled.
type retryUntilCanceledError struct {
	cause error
}

func (e *retryUntilCanceledError) Error() string {
	return fmt.Sprintf("retry until canceled: %v", e.cause)
}

func (e *retryUntilCanceledError) Unwrap() error {
	return e.cause
}

// RetryUntilCanceled marks err as non-committable. Use this only for a valid
// message blocked by a required dependency, where DLQ/commit would be data
// loss. Poison messages and ordinary transient failures must use normal errors.
func RetryUntilCanceled(err error) error {
	if err == nil {
		return nil
	}
	return &retryUntilCanceledError{cause: err}
}

// IsRetryUntilCanceled reports whether err carries the non-committable retry
// disposition, including through additional wrapping.
func IsRetryUntilCanceled(err error) bool {
	var target *retryUntilCanceledError
	return errors.As(err, &target)
}

// Config holds Kafka configuration
type Config struct {
	Brokers       []string      `yaml:"brokers" env:"KAFKA_BROKERS" env-default:"localhost:9092"`
	GroupID       string        `yaml:"group_id" env:"KAFKA_GROUP_ID"`
	TopicSuffix   string        `yaml:"topic_suffix" env:"KAFKA_TOPIC_SUFFIX"`
	GroupIDSuffix string        `yaml:"group_id_suffix" env:"KAFKA_GROUP_ID_SUFFIX"`
	MinBytes      int           `yaml:"min_bytes" env:"KAFKA_MIN_BYTES" env-default:"10000"`    // 10KB
	MaxBytes      int           `yaml:"max_bytes" env:"KAFKA_MAX_BYTES" env-default:"10000000"` // 10MB
	MaxWait       time.Duration `yaml:"max_wait" env:"KAFKA_MAX_WAIT" env-default:"500ms"`
	CommitTimeout time.Duration `yaml:"commit_timeout" env:"KAFKA_COMMIT_TIMEOUT" env-default:"5s"`
	BatchSize     int           `yaml:"batch_size" env:"KAFKA_BATCH_SIZE" env-default:"100"`
	BatchTimeout  time.Duration `yaml:"batch_timeout" env:"KAFKA_BATCH_TIMEOUT" env-default:"100ms"`

	// Retry / DLQ configuration.
	//
	// MaxRetries controls how many times a transient handler error is retried
	// before the message is considered permanently failed. Default: 5.
	MaxRetries int `yaml:"max_retries" env:"KAFKA_MAX_RETRIES" env-default:"5"`
	// BackoffMin is the initial delay before the first retry. Default: 100ms.
	BackoffMin time.Duration `yaml:"backoff_min" env:"KAFKA_BACKOFF_MIN" env-default:"100ms"`
	// BackoffMax caps the exponential delay. Default: 30s.
	BackoffMax time.Duration `yaml:"backoff_max" env:"KAFKA_BACKOFF_MAX" env-default:"30s"`
	// DLQEnabled, when true, publishes exhausted messages to a dead-letter topic
	// named "<original_topic>.dlq" instead of simply logging and discarding them.
	// Opt-in so existing services are not affected by default.
	DLQEnabled bool `yaml:"dlq_enabled" env:"KAFKA_DLQ_ENABLED" env-default:"false"`

	// ReadBackoffMin is the minimum time the reader waits when the broker has no
	// new messages before polling again. Increasing this value reduces idle
	// polling pressure on the broker during consumer catch-up. Default: 100ms.
	ReadBackoffMin time.Duration `yaml:"read_backoff_min" env:"KAFKA_READ_BACKOFF_MIN" env-default:"100ms"`
	// ReadBackoffMax caps the read backoff interval. Default: 1s.
	ReadBackoffMax time.Duration `yaml:"read_backoff_max" env:"KAFKA_READ_BACKOFF_MAX" env-default:"1s"`
}

// Topic returns the environment-qualified topic name. Production keeps the
// empty suffix; deployments that share a broker but not their backing data use
// a suffix such as ".stage". Qualification is idempotent, including DLQ names
// derived from an already-qualified source topic.
func (c Config) Topic(topic string) string {
	return qualifyTopic(topic, c.TopicSuffix)
}

// ConsumerGroup returns the environment-qualified consumer group. A separate
// group suffix is required because topic and group naming conventions differ.
func (c Config) ConsumerGroup(groupID string) string {
	groupID = strings.TrimSpace(groupID)
	suffix := strings.TrimSpace(c.GroupIDSuffix)
	if groupID == "" || suffix == "" || strings.HasSuffix(groupID, suffix) {
		return groupID
	}
	return groupID + suffix
}

func qualifyTopic(topic, suffix string) string {
	topic = strings.TrimSpace(topic)
	suffix = strings.TrimSpace(suffix)
	if topic == "" || suffix == "" || strings.HasSuffix(topic, suffix) || strings.Contains(topic, suffix+".") {
		return topic
	}
	return topic + suffix
}

// Event represents a domain event
type Event struct {
	ID        string          `json:"id"`
	Type      string          `json:"type"`
	Source    string          `json:"source"`
	Data      json.RawMessage `json:"data"`
	Timestamp FlexibleTime    `json:"timestamp"`
	Metadata  Metadata        `json:"metadata,omitempty"`
}

// Metadata contains event metadata
type Metadata struct {
	UserID    int64  `json:"user_id,omitempty"`
	RequestID string `json:"request_id,omitempty"`
	TraceID   string `json:"trace_id,omitempty"`
}

// Producer wraps kafka.Writer
type Producer struct {
	writer      *kafka.Writer
	topic       string
	topicSuffix string
	requireKey  bool

	// extraWriters holds per-topic writers spawned lazily by PublishJSONTo.
	// Guarded by extraMu — both fields stay nil for callers that only ever
	// write to the bound topic, so the common path keeps zero overhead.
	extraMu      sync.Mutex
	extraWriters map[string]*kafka.Writer
}

// NewProducer creates a new Kafka producer
func NewProducer(cfg Config, topic string) *Producer {
	return newProducer(cfg, topic, &kafka.LeastBytes{}, false)
}

// NewKeyedProducer creates a producer that routes equal non-empty keys to the
// same partition using Java-compatible Murmur2. Use it for ordered aggregate
// streams such as request events. The topic partition count must remain fixed;
// expanding it requires a controlled drain/version cutover because hash modulo
// partition-count can remap existing keys.
func NewKeyedProducer(cfg Config, topic string) *Producer {
	return newProducer(cfg, topic, &kafka.Murmur2Balancer{Consistent: true}, true)
}

func newProducer(cfg Config, topic string, balancer kafka.Balancer, requireKey bool) *Producer {
	topic = cfg.Topic(topic)
	writer := newWriterWithBalancer(cfg, topic, balancer)

	logger.Info("Kafka producer created",
		zap.Strings("brokers", cfg.Brokers),
		zap.String("topic", topic),
	)

	return &Producer{
		writer:      writer,
		topic:       topic,
		topicSuffix: cfg.TopicSuffix,
		requireKey:  requireKey,
	}
}

func newWriter(cfg Config, topic string) *kafka.Writer {
	return newWriterWithBalancer(cfg, topic, &kafka.LeastBytes{})
}

func newWriterWithBalancer(cfg Config, topic string, balancer kafka.Balancer) *kafka.Writer {
	return &kafka.Writer{
		Addr:         kafka.TCP(cfg.Brokers...),
		Topic:        cfg.Topic(topic),
		Balancer:     balancer,
		BatchSize:    cfg.BatchSize,
		BatchTimeout: cfg.BatchTimeout,
		RequiredAcks: kafka.RequireAll,
		Async:        false,
	}
}

// Publish publishes an event to Kafka
func (p *Producer) Publish(ctx context.Context, key string, event Event) error {
	if p.requireKey && key == "" {
		return fmt.Errorf("publish keyed event: key is required")
	}
	data, err := json.Marshal(event)
	if err != nil {
		return fmt.Errorf("marshal event: %w", err)
	}

	msg := kafka.Message{
		Key:   []byte(key),
		Value: data,
		Time:  time.Now(),
	}

	if err := p.writer.WriteMessages(ctx, msg); err != nil {
		return fmt.Errorf("write message: %w", err)
	}

	metrics.RecordKafkaMessageProduced(p.topic)

	logger.Debug("Event published",
		zap.String("topic", p.topic),
		zap.String("key", key),
		zap.String("event_type", event.Type),
	)

	return nil
}

// PublishJSON publishes a JSON message to Kafka
func (p *Producer) PublishJSON(ctx context.Context, key string, data any) error {
	if p.requireKey && key == "" {
		return fmt.Errorf("publish keyed JSON: key is required")
	}
	value, err := json.Marshal(data)
	if err != nil {
		return fmt.Errorf("marshal data: %w", err)
	}

	msg := kafka.Message{
		Key:   []byte(key),
		Value: value,
		Time:  time.Now(),
	}

	if err := p.writer.WriteMessages(ctx, msg); err != nil {
		return err
	}

	metrics.RecordKafkaMessageProduced(p.topic)
	return nil
}

// PublishJSONTo publishes a JSON message to an arbitrary topic, overriding
// the Producer's bound topic. Useful for buffered publishers that fan
// events from multiple domains through a single Producer instance — the
// previous design quietly dropped all routing because PublishJSON only
// ever wrote to the bound topic.
//
// Internally creates a kafka-go Writer per target topic on first use and
// caches it. Cleared on Close().
func (p *Producer) PublishJSONTo(ctx context.Context, topic, key string, data any) error {
	topic = qualifyTopic(topic, p.topicSuffix)
	if topic == "" || topic == p.topic {
		return p.PublishJSON(ctx, key, data)
	}
	if p.requireKey {
		return fmt.Errorf("publish keyed JSON: cross-topic publishing is not supported")
	}

	value, err := json.Marshal(data)
	if err != nil {
		return fmt.Errorf("marshal data: %w", err)
	}

	w := p.writerFor(topic)
	msg := kafka.Message{
		Key:   []byte(key),
		Value: value,
		Time:  time.Now(),
	}
	if err := w.WriteMessages(ctx, msg); err != nil {
		return err
	}

	metrics.RecordKafkaMessageProduced(topic)
	return nil
}

func (p *Producer) writerFor(topic string) *kafka.Writer {
	topic = qualifyTopic(topic, p.topicSuffix)
	p.extraMu.Lock()
	defer p.extraMu.Unlock()

	if p.extraWriters == nil {
		p.extraWriters = make(map[string]*kafka.Writer)
	}
	if w, ok := p.extraWriters[topic]; ok {
		return w
	}

	src := p.writer
	w := &kafka.Writer{
		Addr:         src.Addr,
		Topic:        topic,
		Balancer:     src.Balancer,
		BatchSize:    src.BatchSize,
		BatchTimeout: src.BatchTimeout,
		RequiredAcks: src.RequiredAcks,
		Async:        src.Async,
	}
	p.extraWriters[topic] = w
	logger.Info("Kafka producer extra writer created", zap.String("topic", topic))
	return w
}

// Close closes the producer and any topic-specific writers spawned by
// PublishJSONTo. Returns the first error encountered, but always attempts
// to close all writers so we don't leak connections on shutdown.
func (p *Producer) Close() error {
	var firstErr error
	if p.writer != nil {
		logger.Info("Kafka producer closed", zap.String("topic", p.topic))
		if err := p.writer.Close(); err != nil {
			firstErr = err
		}
	}
	p.extraMu.Lock()
	for topic, w := range p.extraWriters {
		if err := w.Close(); err != nil && firstErr == nil {
			firstErr = err
		}
		logger.Info("Kafka extra writer closed", zap.String("topic", topic))
	}
	p.extraWriters = nil
	p.extraMu.Unlock()
	return firstErr
}

// Consumer wraps kafka.Reader
type Consumer struct {
	reader                 *kafka.Reader
	topic                  string
	groupID                string
	retryCfg               retryConfig
	dlqProducer            dlqPublisher // nil when DLQ is disabled
	dlqTopic               string
	dlqValueRedactor       DLQValueRedactor
	dlqKeyRedactor         DLQKeyRedactor
	eventDecodeErrorsToDLQ bool
	eventDecodeRedactor    DLQValueRedactor
}

// DLQValueRedactor transforms source message bytes before they are persisted
// in a dead-letter envelope. The returned bytes must be valid JSON. Use it to
// enforce data minimization when an event schema may contain sensitive fields.
// Returning an error retains the source offset rather than publishing raw data.
// Implementations must honor context cancellation and must not perform
// unbounded network I/O.
type DLQValueRedactor func(ctx context.Context, value []byte) (json.RawMessage, error)

// DLQKeyRedactor transforms a source Kafka key before it is used as the DLQ
// record key and original_key field. Returning nil drops the key.
type DLQKeyRedactor func(ctx context.Context, key []byte) ([]byte, error)

// ConsumerOption configures behavior that is specific to one consumer.
type ConsumerOption func(*Consumer)

type eventDecodeError struct {
	cause error
}

func (e *eventDecodeError) Error() string {
	return "event envelope decode failed"
}

func (e *eventDecodeError) Unwrap() error {
	return e.cause
}

// WithDLQValueRedactor configures a fail-closed transform for original_value
// in DLQ records. It has no effect while DLQ is disabled.
func WithDLQValueRedactor(redactor DLQValueRedactor) ConsumerOption {
	return func(consumer *Consumer) {
		consumer.dlqValueRedactor = redactor
	}
}

// WithDLQKeyRedactor configures a fail-closed transform for source Kafka keys.
// A redaction error retains the source offset rather than using the raw key.
func WithDLQKeyRedactor(redactor DLQKeyRedactor) ConsumerOption {
	return func(consumer *Consumer) {
		consumer.dlqKeyRedactor = redactor
	}
}

// WithEventDecodeErrorsToDLQ routes malformed top-level event envelopes
// through bounded retry and the configured DLQ path. By default ConsumeEvent
// keeps its historical behavior and commits malformed envelopes immediately.
// The required value redactor handles source bytes that cannot be trusted to
// match the event schema. Source keys are dropped so they cannot bypass value
// redaction. A nil redactor fails closed and retains the source offset.
func WithEventDecodeErrorsToDLQ(redactor DLQValueRedactor) ConsumerOption {
	return func(consumer *Consumer) {
		consumer.eventDecodeErrorsToDLQ = true
		consumer.eventDecodeRedactor = redactor
	}
}

// NewConsumer creates a Kafka consumer with the stable default behavior.
func NewConsumer(cfg Config, topic string) *Consumer {
	return NewConsumerWithOptions(cfg, topic)
}

// NewConsumerWithOptions creates a Kafka consumer with explicit per-consumer
// behavior such as privacy-safe DLQ redaction.
func NewConsumerWithOptions(cfg Config, topic string, options ...ConsumerOption) *Consumer {
	topic = cfg.Topic(topic)
	groupID := cfg.ConsumerGroup(cfg.GroupID)
	reader := kafka.NewReader(kafka.ReaderConfig{
		Brokers:        cfg.Brokers,
		Topic:          topic,
		GroupID:        groupID,
		MinBytes:       cfg.MinBytes,
		MaxBytes:       cfg.MaxBytes,
		MaxWait:        cfg.MaxWait,
		ReadBackoffMin: cfg.ReadBackoffMin,
		ReadBackoffMax: cfg.ReadBackoffMax,
	})

	logger.Info("Kafka consumer created",
		zap.Strings("brokers", cfg.Brokers),
		zap.String("topic", topic),
		zap.String("group_id", groupID),
	)

	rc := newRetryConfig(cfg)

	var dlqProd dlqPublisher
	if cfg.DLQEnabled {
		dlqTopic := topic + ".dlq"
		dlqProd = NewProducer(cfg, dlqTopic)
		logger.Info("Kafka DLQ producer created",
			zap.String("dlq_topic", dlqTopic),
		)
	}

	consumer := &Consumer{
		reader:      reader,
		topic:       topic,
		groupID:     groupID,
		retryCfg:    rc,
		dlqProducer: dlqProd,
		dlqTopic:    topic + ".dlq",
	}
	for _, option := range options {
		if option != nil {
			option(consumer)
		}
	}
	consumer.finalizeOptions()
	return consumer
}

func (c *Consumer) finalizeOptions() {
	if !c.eventDecodeErrorsToDLQ {
		return
	}
	if c.eventDecodeRedactor == nil {
		c.eventDecodeRedactor = func(context.Context, []byte) (json.RawMessage, error) {
			return nil, fmt.Errorf("event decode DLQ value redactor is required")
		}
	}
	// Enforce these invariants after all public options have run so option order
	// cannot restore raw malformed values or source keys.
	c.dlqValueRedactor = c.eventDecodeRedactor
	c.dlqKeyRedactor = func(context.Context, []byte) ([]byte, error) {
		return nil, nil
	}
}

// MessageHandler handles consumed messages
type MessageHandler func(ctx context.Context, msg kafka.Message) error

// Consume starts consuming messages with exponential backoff on transient
// errors and optional dead-letter queue routing when max retries are exhausted.
// RetryUntilCanceled errors retain the current offset until recovery or shutdown.
func (c *Consumer) Consume(ctx context.Context, handler MessageHandler) error {
	for {
		select {
		case <-ctx.Done():
			return ctx.Err()
		default:
			msg, err := c.reader.FetchMessage(ctx)
			if err != nil {
				if ctx.Err() != nil {
					return ctx.Err()
				}
				logger.Error("fetch message failed", zap.Error(err))
				continue
			}

			shouldCommit, handleErr := c.handleWithRetry(ctx, msg, handler)

			if !shouldCommit {
				// Context cancelled during a backoff sleep.
				return handleErr
			}

			if handleErr != nil {
				// Max-retries exhausted (already logged + DLQ'd inside
				// handleWithRetry). Commit so we don't replay the poison pill.
				logger.Error("committing offset after exhausted retries",
					zap.Error(handleErr),
					zap.String("topic", c.topic),
					zap.Int64("offset", msg.Offset),
				)
			}

			if cerr := c.reader.CommitMessages(ctx, msg); cerr != nil {
				logger.Error("commit message failed", zap.Error(cerr))
			} else if handleErr == nil {
				metrics.RecordKafkaMessageConsumed(c.topic, c.groupID)
			}
		}
	}
}

// ConsumeEvent consumes and parses events. By default, an invalid top-level
// envelope is committed as an UnmarshalError. WithEventDecodeErrorsToDLQ opts
// into bounded retry followed by the configured DLQ path instead.
func (c *Consumer) ConsumeEvent(ctx context.Context, handler func(ctx context.Context, event Event) error) error {
	return c.Consume(ctx, func(ctx context.Context, msg kafka.Message) error {
		event, err := c.decodeEvent(msg.Value)
		if err != nil {
			return err
		}
		return handler(ctx, event)
	})
}

func (c *Consumer) decodeEvent(value []byte) (Event, error) {
	var event Event
	if err := json.Unmarshal(value, &event); err != nil {
		decodeErr := &eventDecodeError{cause: err}
		if c.eventDecodeErrorsToDLQ {
			return Event{}, decodeErr
		}
		return Event{}, NewUnmarshalError(decodeErr)
	}
	return event, nil
}

// Close closes the consumer
func (c *Consumer) Close() error {
	var firstErr error
	if c.reader != nil {
		logger.Info("Kafka consumer closed", zap.String("topic", c.topic))
		if err := c.reader.Close(); err != nil {
			firstErr = err
		}
	}
	if c.dlqProducer != nil {
		if err := c.dlqProducer.Close(); err != nil && firstErr == nil {
			firstErr = err
		}
	}
	return firstErr
}

// MultiConsumer consumes from multiple topics
type MultiConsumer struct {
	consumers []*Consumer
}

// NewMultiConsumer creates a consumer for multiple topics
func NewMultiConsumer(cfg Config, topics []string) *MultiConsumer {
	consumers := make([]*Consumer, 0, len(topics))
	for _, topic := range topics {
		consumers = append(consumers, NewConsumer(cfg, topic))
	}
	return &MultiConsumer{consumers: consumers}
}

// ConsumeAll starts consuming from all topics
func (mc *MultiConsumer) ConsumeAll(ctx context.Context, handler MessageHandler) error {
	errCh := make(chan error, len(mc.consumers))

	for _, c := range mc.consumers {
		go func(consumer *Consumer) {
			errCh <- consumer.Consume(ctx, handler)
		}(c)
	}

	// Wait for first error or context cancellation
	select {
	case err := <-errCh:
		return err
	case <-ctx.Done():
		return ctx.Err()
	}
}

// Close closes all consumers
func (mc *MultiConsumer) Close() error {
	for _, c := range mc.consumers {
		if err := c.Close(); err != nil {
			return err
		}
	}
	return nil
}
