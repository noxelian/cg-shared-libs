package metrics

import (
	"bufio"
	"context"
	"fmt"
	"net"
	"net/http"
	"strconv"
	"time"

	"github.com/4ubak/cg-shared-libs/logger"
	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/promauto"
	"github.com/prometheus/client_golang/prometheus/promhttp"
	"go.uber.org/zap"
	"google.golang.org/grpc"
	"google.golang.org/grpc/status"
)

// Metrics holds all Prometheus metrics for a service
type Metrics struct {
	serviceName string

	// HTTP metrics
	httpRequestsTotal   *prometheus.CounterVec
	httpRequestDuration *prometheus.HistogramVec
	httpErrorsTotal     *prometheus.CounterVec

	// gRPC metrics
	grpcRequestsTotal   *prometheus.CounterVec
	grpcRequestDuration *prometheus.HistogramVec
	grpcErrorsTotal     *prometheus.CounterVec
}

// New creates a new Metrics instance for a service
func New(serviceName string) *Metrics {
	return &Metrics{
		serviceName: serviceName,
		httpRequestsTotal: promauto.NewCounterVec(
			prometheus.CounterOpts{
				Name: "http_requests_total",
				Help: "Total number of HTTP requests",
			},
			[]string{"service", "method", "endpoint", "status"},
		),
		httpRequestDuration: promauto.NewHistogramVec(
			prometheus.HistogramOpts{
				Name:    "http_request_duration_seconds",
				Help:    "HTTP request duration in seconds",
				Buckets: []float64{0.001, 0.005, 0.01, 0.025, 0.05, 0.1, 0.25, 0.5, 1, 2.5, 5, 10},
			},
			[]string{"service", "method", "endpoint"},
		),
		httpErrorsTotal: promauto.NewCounterVec(
			prometheus.CounterOpts{
				Name: "http_errors_total",
				Help: "Total number of HTTP errors",
			},
			[]string{"service", "method", "endpoint", "error_type"},
		),
		grpcRequestsTotal: promauto.NewCounterVec(
			prometheus.CounterOpts{
				Name: "grpc_requests_total",
				Help: "Total number of gRPC requests",
			},
			[]string{"service", "method", "status"},
		),
		grpcRequestDuration: promauto.NewHistogramVec(
			prometheus.HistogramOpts{
				Name:    "grpc_request_duration_seconds",
				Help:    "gRPC request duration in seconds",
				Buckets: []float64{0.001, 0.005, 0.01, 0.025, 0.05, 0.1, 0.25, 0.5, 1, 2.5, 5, 10},
			},
			[]string{"service", "method"},
		),
		grpcErrorsTotal: promauto.NewCounterVec(
			prometheus.CounterOpts{
				Name: "grpc_errors_total",
				Help: "Total number of gRPC errors",
			},
			[]string{"service", "method", "error_code"},
		),
	}
}

// RecordHTTPRequest records HTTP request metrics
func (m *Metrics) RecordHTTPRequest(method, endpoint string, statusCode int, duration time.Duration) {
	status := strconv.Itoa(statusCode)
	m.httpRequestsTotal.WithLabelValues(m.serviceName, method, endpoint, status).Inc()
	m.httpRequestDuration.WithLabelValues(m.serviceName, method, endpoint).Observe(duration.Seconds())

	if statusCode >= 400 {
		errorType := "client_error"
		if statusCode >= 500 {
			errorType = "server_error"
		}
		m.httpErrorsTotal.WithLabelValues(m.serviceName, method, endpoint, errorType).Inc()
	}
}

// RecordGRPCRequest records gRPC request metrics
func (m *Metrics) RecordGRPCRequest(method, status string, duration time.Duration) {
	m.grpcRequestsTotal.WithLabelValues(m.serviceName, method, status).Inc()
	m.grpcRequestDuration.WithLabelValues(m.serviceName, method).Observe(duration.Seconds())

	if status != "OK" {
		m.grpcErrorsTotal.WithLabelValues(m.serviceName, method, status).Inc()
	}
}

// HTTPMetricsMiddleware wraps HTTP handler with metrics collection
func (m *Metrics) HTTPMetricsMiddleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		start := time.Now()

		// Wrap ResponseWriter to capture status code
		rw := &responseWriter{ResponseWriter: w, statusCode: http.StatusOK}

		next.ServeHTTP(rw, r)

		duration := time.Since(start)
		endpoint := r.URL.Path
		method := r.Method

		m.RecordHTTPRequest(method, endpoint, rw.statusCode, duration)

		logger.Debug("HTTP request metrics",
			zap.String("service", m.serviceName),
			zap.String("method", method),
			zap.String("endpoint", endpoint),
			zap.Int("status", rw.statusCode),
			zap.Duration("duration", duration),
		)
	})
}

// responseWriter wraps http.ResponseWriter to capture status code.
// It also implements http.Hijacker so that WebSocket upgrades work
// through the metrics middleware without panicking.
type responseWriter struct {
	http.ResponseWriter
	statusCode int
}

func (rw *responseWriter) WriteHeader(code int) {
	rw.statusCode = code
	rw.ResponseWriter.WriteHeader(code)
}

// Hijack implements http.Hijacker, required for WebSocket upgrades.
func (rw *responseWriter) Hijack() (net.Conn, *bufio.ReadWriter, error) {
	if hj, ok := rw.ResponseWriter.(http.Hijacker); ok {
		return hj.Hijack()
	}
	return nil, nil, fmt.Errorf("underlying ResponseWriter does not implement http.Hijacker")
}

// GRPCMetricsInterceptor creates a gRPC interceptor for metrics
func (m *Metrics) GRPCMetricsInterceptor() grpc.UnaryServerInterceptor {
	return func(
		ctx context.Context,
		req interface{},
		info *grpc.UnaryServerInfo,
		handler grpc.UnaryHandler,
	) (interface{}, error) {
		start := time.Now()

		resp, err := handler(ctx, req)

		duration := time.Since(start)
		method := info.FullMethod

		statusCode := "OK"
		if err != nil {
			if st, ok := status.FromError(err); ok {
				statusCode = st.Code().String()
			} else {
				statusCode = "Unknown"
			}
		}

		m.RecordGRPCRequest(method, statusCode, duration)

		logger.Debug("gRPC request metrics",
			zap.String("service", m.serviceName),
			zap.String("method", method),
			zap.String("status", statusCode),
			zap.Duration("duration", duration),
		)

		return resp, err
	}
}

// Handler returns the Prometheus metrics handler for /metrics endpoint
func Handler() http.Handler {
	return promhttp.Handler()
}

// Circuit Breaker metrics
var (
	circuitBreakerState = promauto.NewGaugeVec(
		prometheus.GaugeOpts{
			Name: "circuit_breaker_state",
			Help: "Circuit breaker state (0=closed, 1=open, 2=half-open)",
		},
		[]string{"name"},
	)

	circuitBreakerFailures = promauto.NewCounterVec(
		prometheus.CounterOpts{
			Name: "circuit_breaker_failures_total",
			Help: "Total number of circuit breaker failures",
		},
		[]string{"name"},
	)

	circuitBreakerSuccesses = promauto.NewCounterVec(
		prometheus.CounterOpts{
			Name: "circuit_breaker_successes_total",
			Help: "Total number of circuit breaker successes",
		},
		[]string{"name"},
	)

	// Database metrics
	dbConnectionsActive = promauto.NewGaugeVec(
		prometheus.GaugeOpts{
			Name: "db_connections_active",
			Help: "Number of active database connections",
		},
		[]string{"database"},
	)

	dbConnectionsIdle = promauto.NewGaugeVec(
		prometheus.GaugeOpts{
			Name: "db_connections_idle",
			Help: "Number of idle database connections",
		},
		[]string{"database"},
	)

	// Redis metrics
	redisOperationsTotal = promauto.NewCounterVec(
		prometheus.CounterOpts{
			Name: "redis_operations_total",
			Help: "Total number of Redis operations",
		},
		[]string{"operation", "status"},
	)

	// Kafka metrics
	kafkaMessagesProduced = promauto.NewCounterVec(
		prometheus.CounterOpts{
			Name: "kafka_messages_produced_total",
			Help: "Total number of Kafka messages produced",
		},
		[]string{"topic"},
	)

	kafkaMessagesConsumed = promauto.NewCounterVec(
		prometheus.CounterOpts{
			Name: "kafka_messages_consumed_total",
			Help: "Total number of Kafka messages consumed",
		},
		[]string{"topic", "group"},
	)

	kafkaUnmarshalErrors = promauto.NewCounterVec(
		prometheus.CounterOpts{
			Name: "kafka_consumer_unmarshal_errors_total",
			Help: "Total number of Kafka messages skipped due to JSON unmarshal errors (schema mismatch)",
		},
		[]string{"topic", "consumer_group"},
	)

	kafkaConsumerRetries = promauto.NewCounterVec(
		prometheus.CounterOpts{
			Name: "kafka_consumer_retries_total",
			Help: "Total number of Kafka message handler retries due to transient errors",
		},
		[]string{"topic", "consumer_group"},
	)

	kafkaConsumerRetainedRetries = promauto.NewCounterVec(
		prometheus.CounterOpts{
			Name: "kafka_consumer_retained_retries_total",
			Help: "Total retries that retained a Kafka offset because a required dependency was unavailable",
		},
		[]string{"topic", "consumer_group"},
	)

	kafkaConsumerRetainedOffsets = promauto.NewGaugeVec(
		prometheus.GaugeOpts{
			Name: "kafka_consumer_retained_offsets",
			Help: "Current Kafka messages retaining their offset while a required dependency is unavailable",
		},
		[]string{"topic", "consumer_group"},
	)

	kafkaDLQTotal = promauto.NewCounterVec(
		prometheus.CounterOpts{
			Name: "kafka_consumer_dlq_total",
			Help: "Total number of Kafka messages sent to the dead-letter queue after max retries",
		},
		[]string{"topic", "consumer_group"},
	)

	// Business metrics
	activeWebsockets = promauto.NewGauge(
		prometheus.GaugeOpts{
			Name: "websocket_connections_active",
			Help: "Number of active WebSocket connections",
		},
	)

	requestsCreated = promauto.NewCounter(
		prometheus.CounterOpts{
			Name: "business_requests_created_total",
			Help: "Total number of requests created",
		},
	)

	bidsCreated = promauto.NewCounter(
		prometheus.CounterOpts{
			Name: "business_bids_created_total",
			Help: "Total number of bids created",
		},
	)
)

// SetCircuitBreakerState sets the circuit breaker state metric
func SetCircuitBreakerState(name string, state int) {
	circuitBreakerState.WithLabelValues(name).Set(float64(state))
}

// RecordCircuitBreakerFailure records a circuit breaker failure
func RecordCircuitBreakerFailure(name string) {
	circuitBreakerFailures.WithLabelValues(name).Inc()
}

// RecordCircuitBreakerSuccess records a circuit breaker success
func RecordCircuitBreakerSuccess(name string) {
	circuitBreakerSuccesses.WithLabelValues(name).Inc()
}

// SetDBConnections sets database connection metrics
func SetDBConnections(database string, active, idle int) {
	dbConnectionsActive.WithLabelValues(database).Set(float64(active))
	dbConnectionsIdle.WithLabelValues(database).Set(float64(idle))
}

// RecordRedisOperation records a Redis operation
func RecordRedisOperation(operation, status string) {
	redisOperationsTotal.WithLabelValues(operation, status).Inc()
}

// RecordKafkaMessageProduced records a produced Kafka message
func RecordKafkaMessageProduced(topic string) {
	kafkaMessagesProduced.WithLabelValues(topic).Inc()
}

// RecordKafkaMessageConsumed records a consumed Kafka message
func RecordKafkaMessageConsumed(topic, group string) {
	kafkaMessagesConsumed.WithLabelValues(topic, group).Inc()
}

// SetActiveWebsockets sets the number of active WebSocket connections
func SetActiveWebsockets(count int) {
	activeWebsockets.Set(float64(count))
}

// RecordRequestCreated records a request creation
func RecordRequestCreated() {
	requestsCreated.Inc()
}

// RecordBidCreated records a bid creation
func RecordBidCreated() {
	bidsCreated.Inc()
}

// RecordKafkaUnmarshalError records a Kafka message that was skipped due to a
// JSON unmarshal error. The offset is committed so the message is not retried.
func RecordKafkaUnmarshalError(topic, consumerGroup string) {
	kafkaUnmarshalErrors.WithLabelValues(topic, consumerGroup).Inc()
}

// RecordKafkaConsumerRetry records a single retry attempt for a transient
// handler error on the given topic/consumer-group pair.
func RecordKafkaConsumerRetry(topic, consumerGroup string) {
	kafkaConsumerRetries.WithLabelValues(topic, consumerGroup).Inc()
}

// RecordKafkaConsumerRetainedRetry records a retry that deliberately keeps the
// current offset instead of entering finite retry/DLQ handling.
func RecordKafkaConsumerRetainedRetry(topic, consumerGroup string) {
	kafkaConsumerRetainedRetries.WithLabelValues(topic, consumerGroup).Inc()
}

// RetainKafkaConsumerOffset tracks a message currently blocked on a required
// dependency. Every successful retain must be paired with Release.
func RetainKafkaConsumerOffset(topic, consumerGroup string) {
	kafkaConsumerRetainedOffsets.WithLabelValues(topic, consumerGroup).Inc()
}

// ReleaseKafkaConsumerOffset clears one currently retained message.
func ReleaseKafkaConsumerOffset(topic, consumerGroup string) {
	kafkaConsumerRetainedOffsets.WithLabelValues(topic, consumerGroup).Dec()
}

// RecordKafkaDLQ records a message that has been routed to the dead-letter
// queue (or discarded) after exhausting all retry attempts.
func RecordKafkaDLQ(topic, consumerGroup string) {
	kafkaDLQTotal.WithLabelValues(topic, consumerGroup).Inc()
}
