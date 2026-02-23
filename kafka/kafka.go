package kafka

import (
	"context"
	"encoding/json"
	"fmt"
	"strconv"
	"time"

	"github.com/segmentio/kafka-go"
	"gitlab.com/xakpro/cg-shared-libs/logger"
	"gitlab.com/xakpro/cg-shared-libs/metrics"
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

// Config holds Kafka configuration
type Config struct {
	Brokers       []string      `yaml:"brokers" env:"KAFKA_BROKERS" env-default:"localhost:9092"`
	GroupID       string        `yaml:"group_id" env:"KAFKA_GROUP_ID"`
	MinBytes      int           `yaml:"min_bytes" env:"KAFKA_MIN_BYTES" env-default:"10000"`    // 10KB
	MaxBytes      int           `yaml:"max_bytes" env:"KAFKA_MAX_BYTES" env-default:"10000000"` // 10MB
	MaxWait       time.Duration `yaml:"max_wait" env:"KAFKA_MAX_WAIT" env-default:"500ms"`
	CommitTimeout time.Duration `yaml:"commit_timeout" env:"KAFKA_COMMIT_TIMEOUT" env-default:"5s"`
	BatchSize     int           `yaml:"batch_size" env:"KAFKA_BATCH_SIZE" env-default:"100"`
	BatchTimeout  time.Duration `yaml:"batch_timeout" env:"KAFKA_BATCH_TIMEOUT" env-default:"100ms"`
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
	writer *kafka.Writer
	topic  string
}

// NewProducer creates a new Kafka producer
func NewProducer(cfg Config, topic string) *Producer {
	writer := &kafka.Writer{
		Addr:         kafka.TCP(cfg.Brokers...),
		Topic:        topic,
		Balancer:     &kafka.LeastBytes{},
		BatchSize:    cfg.BatchSize,
		BatchTimeout: cfg.BatchTimeout,
		Async:        false,
	}

	logger.Info("Kafka producer created",
		zap.Strings("brokers", cfg.Brokers),
		zap.String("topic", topic),
	)

	return &Producer{
		writer: writer,
		topic:  topic,
	}
}

// Publish publishes an event to Kafka
func (p *Producer) Publish(ctx context.Context, key string, event Event) error {
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

// Close closes the producer
func (p *Producer) Close() error {
	if p.writer != nil {
		logger.Info("Kafka producer closed", zap.String("topic", p.topic))
		return p.writer.Close()
	}
	return nil
}

// Consumer wraps kafka.Reader
type Consumer struct {
	reader  *kafka.Reader
	topic   string
	groupID string
}

// NewConsumer creates a new Kafka consumer
func NewConsumer(cfg Config, topic string) *Consumer {
	reader := kafka.NewReader(kafka.ReaderConfig{
		Brokers:  cfg.Brokers,
		Topic:    topic,
		GroupID:  cfg.GroupID,
		MinBytes: cfg.MinBytes,
		MaxBytes: cfg.MaxBytes,
		MaxWait:  cfg.MaxWait,
	})

	logger.Info("Kafka consumer created",
		zap.Strings("brokers", cfg.Brokers),
		zap.String("topic", topic),
		zap.String("group_id", cfg.GroupID),
	)

	return &Consumer{
		reader:  reader,
		topic:   topic,
		groupID: cfg.GroupID,
	}
}

// MessageHandler handles consumed messages
type MessageHandler func(ctx context.Context, msg kafka.Message) error

// Consume starts consuming messages
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

			if err := handler(ctx, msg); err != nil {
				logger.Error("handle message failed",
					zap.Error(err),
					zap.String("topic", c.topic),
					zap.Int64("offset", msg.Offset),
				)
				// Don't commit on error - message will be reprocessed
				continue
			}

			if err := c.reader.CommitMessages(ctx, msg); err != nil {
				logger.Error("commit message failed", zap.Error(err))
			} else {
				metrics.RecordKafkaMessageConsumed(c.topic, c.groupID)
			}
		}
	}
}

// ConsumeEvent consumes and parses events
func (c *Consumer) ConsumeEvent(ctx context.Context, handler func(ctx context.Context, event Event) error) error {
	return c.Consume(ctx, func(ctx context.Context, msg kafka.Message) error {
		var event Event
		if err := json.Unmarshal(msg.Value, &event); err != nil {
			return fmt.Errorf("unmarshal event: %w", err)
		}
		return handler(ctx, event)
	})
}

// Close closes the consumer
func (c *Consumer) Close() error {
	if c.reader != nil {
		logger.Info("Kafka consumer closed", zap.String("topic", c.topic))
		return c.reader.Close()
	}
	return nil
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
