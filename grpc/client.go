package grpc

import (
	"context"
	"fmt"
	"os"
	"sync"
	"time"

	"github.com/google/uuid"
	"gitlab.com/xakpro/cg-shared-libs/circuitbreaker"
	"gitlab.com/xakpro/cg-shared-libs/logger"
	"go.uber.org/zap"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/credentials/insecure"
	"google.golang.org/grpc/keepalive"
	"google.golang.org/grpc/metadata"
	"google.golang.org/grpc/status"

	// Import DNS resolver for client-side load balancing
	_ "google.golang.org/grpc/resolver/dns"
)

// isRunningInLinkerd detects if the service is running with Linkerd sidecar
// When Linkerd is present, we should disable client-side LB as Linkerd handles it
func isRunningInLinkerd() bool {
	// Linkerd injects these env vars
	_, hasIdentity := os.LookupEnv("LINKERD2_PROXY_IDENTITY_LOCAL_NAME")
	_, hasInbound := os.LookupEnv("LINKERD2_PROXY_INBOUND_LISTEN_ADDR")
	return hasIdentity || hasInbound
}

// ClientConfig holds gRPC client configuration
//
// Load Balancing Setup (IMPORTANT for Kubernetes):
//
// 1. Create HEADLESS Kubernetes Service:
//
//	apiVersion: v1
//	kind: Service
//	metadata:
//	  name: user-service
//	spec:
//	  clusterIP: None  # REQUIRED for load balancing!
//	  selector:
//	    app: user-service
//	  ports:
//	    - port: 50052
//
// 2. Set LoadBalancing: "round_robin" (default)
//
// 3. How it works:
//   - DNS query to "user-service" returns all pod IPs (A records)
//   - gRPC establishes connections to ALL pods
//   - round_robin distributes requests across connections
//   - Keepalive detects dead connections
//
// 4. Limitations:
//   - DNS refresh depends on CoreDNS TTL (default 30s in K8s)
//   - For instant failover, use service mesh (Istio/Linkerd)
type ClientConfig struct {
	Host              string        `yaml:"host"`
	Port              int           `yaml:"port"`
	Timeout           time.Duration `yaml:"timeout" env-default:"5s"`
	MaxRetries        int           `yaml:"max_retries" env-default:"3"`
	RetryWaitTime     time.Duration `yaml:"retry_wait_time" env-default:"100ms"`
	MaxRecvMsgSize    int           `yaml:"max_recv_msg_size" env-default:"4194304"`
	MaxSendMsgSize    int           `yaml:"max_send_msg_size" env-default:"4194304"`
	KeepAliveTime     time.Duration `yaml:"keep_alive_time" env-default:"30s"`
	KeepAliveTimeout  time.Duration `yaml:"keep_alive_timeout" env-default:"10s"`
	InitialWindowSize int32         `yaml:"initial_window_size" env-default:"65536"`
	InitialConnWindow int32         `yaml:"initial_conn_window" env-default:"65536"`
	// LoadBalancing enables client-side load balancing for multiple backends
	// Requires headless Kubernetes service (clusterIP: None)
	// Options: "" (disabled), "round_robin" (default), "pick_first"
	LoadBalancing string `yaml:"load_balancing" env:"GRPC_LOAD_BALANCING" env-default:"round_robin"`
}

// Addr returns client target address
func (c *ClientConfig) Addr() string {
	return fmt.Sprintf("%s:%d", c.Host, c.Port)
}

// Target returns the gRPC target with proper scheme for load balancing
// For load balancing to work, use dns:/// scheme with headless Kubernetes service
func (c *ClientConfig) Target() string {
	// When running in Linkerd, use simple address - Linkerd handles LB
	if isRunningInLinkerd() {
		return c.Addr()
	}

	if c.LoadBalancing != "" && c.LoadBalancing != "pick_first" {
		// Use DNS resolver for client-side load balancing
		// Format: dns://[authority]/host:port
		// Empty authority uses system DNS resolver
		return fmt.Sprintf("dns:///%s:%d", c.Host, c.Port)
	}
	return c.Addr()
}

// ServiceConfig returns gRPC service config JSON for load balancing
// Includes health checking and load balancing configuration
func (c *ClientConfig) ServiceConfig() string {
	// When running in Linkerd, no client-side LB config needed
	if isRunningInLinkerd() {
		return ""
	}

	if c.LoadBalancing == "" || c.LoadBalancing == "pick_first" {
		return ""
	}
	// Service config with:
	// - Load balancing policy (round_robin)
	// - Health checking (optional, for better failover)
	// - Retry policy is handled by our interceptor
	return fmt.Sprintf(`{
		"loadBalancingConfig": [{"%s":{}}],
		"methodConfig": [{
			"name": [{"service": ""}],
			"waitForReady": true
		}]
	}`, c.LoadBalancing)
}

// Client wraps gRPC client connection
type Client struct {
	conn   *grpc.ClientConn
	config ClientConfig
}

// NewClient creates a new gRPC client connection
func NewClient(ctx context.Context, cfg ClientConfig, opts ...grpc.DialOption) (*Client, error) {
	// Apply defaults if not set
	maxRecvMsgSize := cfg.MaxRecvMsgSize
	if maxRecvMsgSize == 0 {
		maxRecvMsgSize = 4194304 // 4MB default
	}
	maxSendMsgSize := cfg.MaxSendMsgSize
	if maxSendMsgSize == 0 {
		maxSendMsgSize = 4194304 // 4MB default
	}

	// Detect service mesh
	inLinkerd := isRunningInLinkerd()
	effectiveLB := cfg.LoadBalancing
	if inLinkerd {
		effectiveLB = "linkerd (service mesh)"
	}

	logger.Info("gRPC client configuration",
		zap.String("host", cfg.Host),
		zap.Int("port", cfg.Port),
		zap.Int("max_recv_msg_size", maxRecvMsgSize),
		zap.Int("max_send_msg_size", maxSendMsgSize),
		zap.Int("max_retries", cfg.MaxRetries),
		zap.Duration("retry_wait_time", cfg.RetryWaitTime),
		zap.Duration("timeout", cfg.Timeout),
		zap.String("target", cfg.Target()),
		zap.String("load_balancing", effectiveLB),
		zap.Bool("linkerd_detected", inLinkerd),
	)

	// Keepalive settings for connection health
	keepAliveTime := cfg.KeepAliveTime
	if keepAliveTime == 0 {
		keepAliveTime = 30 * time.Second
	}
	keepAliveTimeout := cfg.KeepAliveTimeout
	if keepAliveTimeout == 0 {
		keepAliveTimeout = 10 * time.Second
	}

	defaultOpts := []grpc.DialOption{
		grpc.WithTransportCredentials(insecure.NewCredentials()),
		grpc.WithDefaultCallOptions(
			grpc.MaxCallRecvMsgSize(maxRecvMsgSize),
			grpc.MaxCallSendMsgSize(maxSendMsgSize),
		),
		// Keepalive is important for load balancing - detects dead connections
		grpc.WithKeepaliveParams(keepalive.ClientParameters{
			Time:                keepAliveTime,    // Ping interval when idle
			Timeout:             keepAliveTimeout, // Wait for ping response
			PermitWithoutStream: true,             // Send pings even without active streams
		}),
		grpc.WithChainUnaryInterceptor(
			clientLoggingInterceptor(),
			circuitBreakerInterceptor(), // Circuit breaker for high availability
			retryInterceptor(cfg.MaxRetries, cfg.RetryWaitTime),
		),
	}

	// Add load balancing service config if enabled
	if svcCfg := cfg.ServiceConfig(); svcCfg != "" {
		defaultOpts = append(defaultOpts, grpc.WithDefaultServiceConfig(svcCfg))
	}

	allOpts := append(defaultOpts, opts...)

	conn, err := grpc.DialContext(ctx, cfg.Target(), allOpts...)
	if err != nil {
		logger.Error("failed to dial gRPC server",
			zap.String("target", cfg.Target()),
			zap.Error(err),
		)
		return nil, fmt.Errorf("dial grpc: %w", err)
	}

	logger.Info("gRPC client connected",
		zap.String("target", cfg.Target()),
		zap.String("load_balancing", cfg.LoadBalancing),
		zap.Int("applied_max_recv_msg_size", maxRecvMsgSize),
		zap.Int("applied_max_send_msg_size", maxSendMsgSize),
	)

	return &Client{
		conn:   conn,
		config: cfg,
	}, nil
}

// Conn returns the underlying connection
func (c *Client) Conn() *grpc.ClientConn {
	return c.conn
}

// Close closes the client connection
func (c *Client) Close() error {
	if c.conn != nil {
		logger.Info("gRPC client disconnected",
			zap.String("addr", c.config.Addr()),
		)
		return c.conn.Close()
	}
	return nil
}

// Client interceptors

func clientLoggingInterceptor() grpc.UnaryClientInterceptor {
	return func(
		ctx context.Context,
		method string,
		req, reply any,
		cc *grpc.ClientConn,
		invoker grpc.UnaryInvoker,
		opts ...grpc.CallOption,
	) error {
		start := time.Now()

		// Extract request ID from context
		requestID := GetRequestIDFromContext(ctx)
		
		// Add request ID to outgoing metadata if not present
		md, ok := metadata.FromOutgoingContext(ctx)
		if !ok {
			md = metadata.New(nil)
		}
		
		// Check if request ID already exists in metadata
		existingVals := md.Get("x-request-id")
		if len(existingVals) > 0 {
			requestID = existingVals[0]
		} else if requestID != "" {
			// Use extracted request ID
			md.Set("x-request-id", requestID)
			ctx = metadata.NewOutgoingContext(ctx, md)
		} else {
			// Generate new request ID if not found
			requestID = generateClientRequestID()
			md.Set("x-request-id", requestID)
			ctx = metadata.NewOutgoingContext(ctx, md)
		}

		// Log outgoing request details with request ID
		logger.Debug("gRPC client call started",
			zap.String("request_id", requestID),
			zap.String("method", method),
			zap.String("target", cc.Target()),
		)

		err := invoker(ctx, method, req, reply, cc, opts...)

		duration := time.Since(start)
		code := codes.OK
		if err != nil {
			code = status.Code(err)
		}

		if code == codes.OK {
			logger.Debug("gRPC client call completed",
				zap.String("request_id", requestID),
				zap.String("method", method),
				zap.Duration("duration", duration),
			)
		} else {
			logger.Warn("gRPC client call failed",
				zap.String("request_id", requestID),
				zap.String("method", method),
				zap.Duration("duration", duration),
				zap.String("code", code.String()),
				zap.String("target", cc.Target()),
				zap.Error(err),
			)
		}

		return err
	}
}

// GetRequestIDFromContext extracts request ID from context
// Checks metadata first, then tries to extract from logger context
func GetRequestIDFromContext(ctx context.Context) string {
	// Try outgoing metadata first
	if md, ok := metadata.FromOutgoingContext(ctx); ok {
		if vals := md.Get("x-request-id"); len(vals) > 0 {
			return vals[0]
		}
	}
	// Try incoming metadata
	if md, ok := metadata.FromIncomingContext(ctx); ok {
		if vals := md.Get("x-request-id"); len(vals) > 0 {
			return vals[0]
		}
	}
	
	// Note: logger.WithRequestID stores request_id in logger fields,
	// but we can't easily extract it. The request_id should be passed
	// via metadata from the caller (BFF middleware does this via context)
	return ""
}

// generateClientRequestID generates a UUID-based request ID for client calls
func generateClientRequestID() string {
	return uuid.New().String()
}

func retryInterceptor(maxRetries int, waitTime time.Duration) grpc.UnaryClientInterceptor {
	return func(
		ctx context.Context,
		method string,
		req, reply any,
		cc *grpc.ClientConn,
		invoker grpc.UnaryInvoker,
		opts ...grpc.CallOption,
	) error {
		var lastErr error

		for i := 0; i <= maxRetries; i++ {
			attempt := i + 1
			logger.Debug("gRPC client call attempt",
				zap.String("method", method),
				zap.Int("attempt", attempt),
				zap.Int("max_retries", maxRetries),
			)

			err := invoker(ctx, method, req, reply, cc, opts...)
			if err == nil {
				if attempt > 1 {
					logger.Info("gRPC client call succeeded after retry",
						zap.String("method", method),
						zap.Int("attempt", attempt),
					)
				}
				return nil
			}

			lastErr = err
			code := status.Code(err)

			logger.Debug("gRPC client call attempt failed",
				zap.String("method", method),
				zap.Int("attempt", attempt),
				zap.String("code", code.String()),
				zap.Bool("retryable", isRetryable(code)),
				zap.Error(err),
			)

			// Only retry on specific codes
			if !isRetryable(code) {
				logger.Debug("error is not retryable, stopping retries",
					zap.String("method", method),
					zap.String("code", code.String()),
				)
				return err
			}

			if i < maxRetries {
				waitDuration := waitTime * time.Duration(i+1)
				logger.Debug("waiting before retry",
					zap.String("method", method),
					zap.Int("next_attempt", attempt+1),
					zap.Duration("wait_time", waitDuration),
				)
				select {
				case <-ctx.Done():
					logger.Warn("context cancelled during retry wait",
						zap.String("method", method),
						zap.Error(ctx.Err()),
					)
					return ctx.Err()
				case <-time.After(waitDuration):
					logger.Debug("retrying gRPC call",
						zap.String("method", method),
						zap.Int("attempt", attempt+1),
					)
				}
			}
		}

		logger.Warn("gRPC client call failed after all retries",
			zap.String("method", method),
			zap.Int("total_attempts", maxRetries+1),
			zap.Error(lastErr),
		)

		return lastErr
	}
}

func isRetryable(code codes.Code) bool {
	switch code {
	case codes.Unavailable, codes.ResourceExhausted, codes.Aborted, codes.Internal:
		return true
	default:
		return false
	}
}

// Circuit Breaker Registry - manages circuit breakers per target
var (
	cbRegistry   = make(map[string]*circuitbreaker.CircuitBreaker)
	cbRegistryMu sync.RWMutex
)

// getOrCreateCircuitBreaker gets or creates a circuit breaker for the target
func getOrCreateCircuitBreaker(target string) *circuitbreaker.CircuitBreaker {
	cbRegistryMu.RLock()
	cb, exists := cbRegistry[target]
	cbRegistryMu.RUnlock()

	if exists {
		return cb
	}

	cbRegistryMu.Lock()
	defer cbRegistryMu.Unlock()

	// Double-check after acquiring write lock
	if cb, exists := cbRegistry[target]; exists {
		return cb
	}

	cb = circuitbreaker.New(circuitbreaker.Config{
		Name:        target,
		MaxFailures: 5,
		Timeout:     30 * time.Second,
	})
	cbRegistry[target] = cb
	return cb
}

// circuitBreakerInterceptor wraps calls with circuit breaker
func circuitBreakerInterceptor() grpc.UnaryClientInterceptor {
	return func(
		ctx context.Context,
		method string,
		req, reply any,
		cc *grpc.ClientConn,
		invoker grpc.UnaryInvoker,
		opts ...grpc.CallOption,
	) error {
		cb := getOrCreateCircuitBreaker(cc.Target())

		return cb.Execute(ctx, func(ctx context.Context) error {
			return invoker(ctx, method, req, reply, cc, opts...)
		})
	}
}

// NewClientWithCircuitBreaker creates a new gRPC client with circuit breaker enabled
func NewClientWithCircuitBreaker(ctx context.Context, cfg ClientConfig, opts ...grpc.DialOption) (*Client, error) {
	// Apply defaults
	maxRecvMsgSize := cfg.MaxRecvMsgSize
	if maxRecvMsgSize == 0 {
		maxRecvMsgSize = 4194304
	}
	maxSendMsgSize := cfg.MaxSendMsgSize
	if maxSendMsgSize == 0 {
		maxSendMsgSize = 4194304
	}

	defaultOpts := []grpc.DialOption{
		grpc.WithTransportCredentials(insecure.NewCredentials()),
		grpc.WithDefaultCallOptions(
			grpc.MaxCallRecvMsgSize(maxRecvMsgSize),
			grpc.MaxCallSendMsgSize(maxSendMsgSize),
		),
		grpc.WithChainUnaryInterceptor(
			clientLoggingInterceptor(),
			circuitBreakerInterceptor(),
			retryInterceptor(cfg.MaxRetries, cfg.RetryWaitTime),
		),
	}

	allOpts := append(defaultOpts, opts...)

	conn, err := grpc.DialContext(ctx, cfg.Addr(), allOpts...)
	if err != nil {
		return nil, fmt.Errorf("dial grpc: %w", err)
	}

	logger.Info("gRPC client with circuit breaker connected",
		zap.String("addr", cfg.Addr()),
	)

	return &Client{
		conn:   conn,
		config: cfg,
	}, nil
}

// GetCircuitBreakerState returns the circuit breaker state for a target
func GetCircuitBreakerState(target string) circuitbreaker.State {
	cbRegistryMu.RLock()
	defer cbRegistryMu.RUnlock()

	if cb, exists := cbRegistry[target]; exists {
		return cb.State()
	}
	return circuitbreaker.StateClosed
}

// ResetCircuitBreaker resets the circuit breaker for a target
func ResetCircuitBreaker(target string) {
	cbRegistryMu.RLock()
	defer cbRegistryMu.RUnlock()

	if cb, exists := cbRegistry[target]; exists {
		cb.Reset()
	}
}
