package grpc

import (
	"context"
	"fmt"
	"time"

	"github.com/google/uuid"
	"gitlab.com/xakpro/cg-shared-libs/logger"
	"go.uber.org/zap"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/credentials/insecure"
	"google.golang.org/grpc/metadata"
	"google.golang.org/grpc/status"
)

// ClientConfig holds gRPC client configuration
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
}

// Addr returns client target address
func (c *ClientConfig) Addr() string {
	return fmt.Sprintf("%s:%d", c.Host, c.Port)
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

	logger.Info("gRPC client configuration",
		zap.String("host", cfg.Host),
		zap.Int("port", cfg.Port),
		zap.Int("max_recv_msg_size", maxRecvMsgSize),
		zap.Int("max_send_msg_size", maxSendMsgSize),
		zap.Int("max_retries", cfg.MaxRetries),
		zap.Duration("retry_wait_time", cfg.RetryWaitTime),
		zap.Duration("timeout", cfg.Timeout),
		zap.String("addr", cfg.Addr()),
	)

	defaultOpts := []grpc.DialOption{
		grpc.WithTransportCredentials(insecure.NewCredentials()),
		grpc.WithDefaultCallOptions(
			grpc.MaxCallRecvMsgSize(maxRecvMsgSize),
			grpc.MaxCallSendMsgSize(maxSendMsgSize),
		),
		grpc.WithChainUnaryInterceptor(
			clientLoggingInterceptor(),
			retryInterceptor(cfg.MaxRetries, cfg.RetryWaitTime),
		),
	}

	allOpts := append(defaultOpts, opts...)

	conn, err := grpc.DialContext(ctx, cfg.Addr(), allOpts...)
	if err != nil {
		logger.Error("failed to dial gRPC server",
			zap.String("addr", cfg.Addr()),
			zap.Error(err),
		)
		return nil, fmt.Errorf("dial grpc: %w", err)
	}

	logger.Info("gRPC client connected",
		zap.String("addr", cfg.Addr()),
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
