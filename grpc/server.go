package grpc

import (
	"context"
	"fmt"
	"net"
	"runtime/debug"
	"time"

	"github.com/google/uuid"
	"github.com/4ubak/cg-shared-libs/logger"
	"go.uber.org/zap"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/keepalive"
	"google.golang.org/grpc/metadata"
	"google.golang.org/grpc/status"
)

// ServerConfig holds gRPC server configuration
type ServerConfig struct {
	Host            string        `yaml:"host" env:"GRPC_HOST" env-default:"0.0.0.0"`
	Port            int           `yaml:"port" env:"GRPC_PORT" env-default:"50051"`
	MaxRecvMsgSize  int           `yaml:"max_recv_msg_size" env:"GRPC_MAX_RECV_MSG_SIZE" env-default:"4194304"` // 4MB
	MaxSendMsgSize  int           `yaml:"max_send_msg_size" env:"GRPC_MAX_SEND_MSG_SIZE" env-default:"4194304"` // 4MB
	ConnectionLimit int           `yaml:"connection_limit" env:"GRPC_CONN_LIMIT" env-default:"1000"`
	Timeout         time.Duration `yaml:"timeout" env:"GRPC_TIMEOUT" env-default:"30s"`
	TLS             TLSConfig     `yaml:"tls"`
}

// Addr returns server address
func (c *ServerConfig) Addr() string {
	return fmt.Sprintf("%s:%d", c.Host, c.Port)
}

// Server wraps gRPC server
type Server struct {
	server   *grpc.Server
	listener net.Listener
	config   ServerConfig
}

// NewServer creates a new gRPC server
func NewServer(cfg ServerConfig, opts ...grpc.ServerOption) (*Server, error) {
	// Apply defaults if not set
	maxRecvMsgSize := cfg.MaxRecvMsgSize
	if maxRecvMsgSize == 0 {
		maxRecvMsgSize = 4194304 // 4MB default
	}
	maxSendMsgSize := cfg.MaxSendMsgSize
	if maxSendMsgSize == 0 {
		maxSendMsgSize = 4194304 // 4MB default
	}

	logger.Info("gRPC server configuration",
		zap.String("host", cfg.Host),
		zap.Int("port", cfg.Port),
		zap.Int("max_recv_msg_size", maxRecvMsgSize),
		zap.Int("max_send_msg_size", maxSendMsgSize),
		zap.Int("connection_limit", cfg.ConnectionLimit),
		zap.Duration("timeout", cfg.Timeout),
		zap.String("addr", cfg.Addr()),
	)

	// Apply TLS credentials if enabled
	if cfg.TLS.Enabled {
		creds, err := cfg.TLS.ServerCredentials()
		if err != nil {
			return nil, fmt.Errorf("server TLS credentials: %w", err)
		}
		if creds != nil {
			opts = append(opts, grpc.Creds(creds))
			logger.Info("gRPC server TLS enabled",
				zap.String("cert_file", cfg.TLS.CertFile),
				zap.Bool("mtls", cfg.TLS.CAFile != ""),
			)
		}
	}

	// Keepalive enforcement policy — allow client pings without active streams
	// and permit more frequent pings (matches client PermitWithoutStream: true)
	defaultOpts := []grpc.ServerOption{
		grpc.MaxRecvMsgSize(maxRecvMsgSize),
		grpc.MaxSendMsgSize(maxSendMsgSize),
		grpc.KeepaliveEnforcementPolicy(keepalive.EnforcementPolicy{
			MinTime:             30 * time.Second, // Minimum time between client pings
			PermitWithoutStream: true,             // Allow pings even without active streams
		}),
		grpc.ChainUnaryInterceptor(
			recoveryInterceptor(),
			loggingInterceptor(),
			timeoutInterceptor(cfg.Timeout),
		),
	}

	// WARNING: gRPC silently overwrites if multiple ChainUnaryInterceptor
	// options are provided. Callers that need extra interceptors should use
	// NewServerWithInterceptors instead.
	allOpts := append(defaultOpts, opts...)
	server := grpc.NewServer(allOpts...)

	logger.Info("gRPC server created successfully",
		zap.String("addr", cfg.Addr()),
		zap.Int("applied_max_recv_msg_size", maxRecvMsgSize),
		zap.Int("applied_max_send_msg_size", maxSendMsgSize),
	)

	return &Server{
		server: server,
		config: cfg,
	}, nil
}

// NewServerWithInterceptors creates a gRPC server with additional unary and
// stream interceptors merged into a single chain alongside the default ones
// (recovery, logging, timeout). This avoids the gRPC limitation where multiple
// ChainUnaryInterceptor options silently overwrite each other.
//
// Usage:
//
//	srv, err := grpc.NewServerWithInterceptors(cfg,
//	    []grpc.UnaryServerInterceptor{authInterceptor, metricsInterceptor},
//	    tracing.StreamServerInterceptors(),
//	    tracing.GRPCServerInterceptors()...,
//	)
func NewServerWithInterceptors(
	cfg ServerConfig,
	unaryInterceptors []grpc.UnaryServerInterceptor,
	streamInterceptors []grpc.StreamServerInterceptor,
	opts ...grpc.ServerOption,
) (*Server, error) {
	maxRecvMsgSize := cfg.MaxRecvMsgSize
	if maxRecvMsgSize == 0 {
		maxRecvMsgSize = 4194304
	}
	maxSendMsgSize := cfg.MaxSendMsgSize
	if maxSendMsgSize == 0 {
		maxSendMsgSize = 4194304
	}

	logger.Info("gRPC server configuration",
		zap.String("host", cfg.Host),
		zap.Int("port", cfg.Port),
		zap.Int("max_recv_msg_size", maxRecvMsgSize),
		zap.Int("max_send_msg_size", maxSendMsgSize),
		zap.Int("connection_limit", cfg.ConnectionLimit),
		zap.Duration("timeout", cfg.Timeout),
		zap.String("addr", cfg.Addr()),
	)

	// Build merged interceptor chain: defaults first, then caller's
	allUnary := []grpc.UnaryServerInterceptor{
		recoveryInterceptor(),
		loggingInterceptor(),
		timeoutInterceptor(cfg.Timeout),
	}
	allUnary = append(allUnary, unaryInterceptors...)

	allOpts := []grpc.ServerOption{
		grpc.MaxRecvMsgSize(maxRecvMsgSize),
		grpc.MaxSendMsgSize(maxSendMsgSize),
		grpc.KeepaliveEnforcementPolicy(keepalive.EnforcementPolicy{
			MinTime:             30 * time.Second,
			PermitWithoutStream: true,
		}),
		grpc.ChainUnaryInterceptor(allUnary...),
	}

	if len(streamInterceptors) > 0 {
		allOpts = append(allOpts, grpc.ChainStreamInterceptor(streamInterceptors...))
	}

	// TLS
	if cfg.TLS.Enabled {
		creds, err := cfg.TLS.ServerCredentials()
		if err != nil {
			return nil, fmt.Errorf("server TLS credentials: %w", err)
		}
		if creds != nil {
			allOpts = append(allOpts, grpc.Creds(creds))
		}
	}

	// Append caller's extra options (StatsHandler, etc.)
	allOpts = append(allOpts, opts...)

	server := grpc.NewServer(allOpts...)

	logger.Info("gRPC server created successfully",
		zap.String("addr", cfg.Addr()),
		zap.Int("unary_interceptors", len(allUnary)),
		zap.Int("stream_interceptors", len(streamInterceptors)),
	)

	return &Server{
		server: server,
		config: cfg,
	}, nil
}

// Server returns the underlying gRPC server
func (s *Server) Server() *grpc.Server {
	return s.server
}

// Start starts the gRPC server
func (s *Server) Start() error {
	listener, err := net.Listen("tcp", s.config.Addr())
	if err != nil {
		return fmt.Errorf("listen: %w", err)
	}
	s.listener = listener

	logger.Info("gRPC server starting",
		zap.String("addr", s.config.Addr()),
	)

	return s.server.Serve(listener)
}

// Stop gracefully stops the server
func (s *Server) Stop() {
	logger.Info("gRPC server stopping")
	s.server.GracefulStop()
}

// Interceptors

func recoveryInterceptor() grpc.UnaryServerInterceptor {
	return func(
		ctx context.Context,
		req any,
		info *grpc.UnaryServerInfo,
		handler grpc.UnaryHandler,
	) (resp any, err error) {
		defer func() {
			if r := recover(); r != nil {
				logger.Error("gRPC panic recovered",
					zap.Any("panic", r),
					zap.String("method", info.FullMethod),
					zap.String("stack", string(debug.Stack())),
				)
				err = status.Errorf(codes.Internal, "internal error")
			}
		}()
		return handler(ctx, req)
	}
}

func loggingInterceptor() grpc.UnaryServerInterceptor {
	return func(
		ctx context.Context,
		req any,
		info *grpc.UnaryServerInfo,
		handler grpc.UnaryHandler,
	) (any, error) {
		start := time.Now()

		// Extract request ID from metadata
		requestID := GetRequestID(ctx)
		if requestID == "" {
			// Generate request ID if not present
			requestID = generateRequestID()
			// Add to context for downstream use
			ctx = logger.WithRequestID(ctx, requestID)
		} else {
			// Add existing request ID to logger context
			ctx = logger.WithRequestID(ctx, requestID)
		}

		// Extract session_id from metadata (bank compliance)
		sessionID := GetMetadata(ctx, "x-session-id")
		if sessionID != "" {
			ctx = logger.WithSessionID(ctx, sessionID)
		}

		// Inject trace_id/span_id from OpenTelemetry context
		ctx = logger.WithTraceID(ctx)

		// Log incoming request details with request ID
		logger.Debug("gRPC request received",
			zap.String("request_id", requestID),
			zap.String("method", info.FullMethod),
		)

		resp, err := handler(ctx, req)

		duration := time.Since(start)
		code := codes.OK
		if err != nil {
			code = status.Code(err)
		}

		// Log based on status with request ID
		if code == codes.OK {
			logger.Debug("gRPC request completed",
				zap.String("request_id", requestID),
				zap.String("method", info.FullMethod),
				zap.Int64("duration_ms", duration.Milliseconds()),
			)
		} else {
			logger.Warn("gRPC request failed",
				zap.String("request_id", requestID),
				zap.String("method", info.FullMethod),
				zap.Int64("duration_ms", duration.Milliseconds()),
				zap.String("code", code.String()),
				zap.Error(err),
			)
		}

		return resp, err
	}
}

// generateRequestID generates a UUID-based request ID
func generateRequestID() string {
	return uuid.New().String()
}

func timeoutInterceptor(timeout time.Duration) grpc.UnaryServerInterceptor {
	return func(
		ctx context.Context,
		req any,
		info *grpc.UnaryServerInfo,
		handler grpc.UnaryHandler,
	) (any, error) {
		ctx, cancel := context.WithTimeout(ctx, timeout)
		defer cancel()
		return handler(ctx, req)
	}
}

// GetMetadata extracts metadata from context
func GetMetadata(ctx context.Context, key string) string {
	md, ok := metadata.FromIncomingContext(ctx)
	if !ok {
		logger.Debug("metadata not found in context",
			zap.String("key", key),
		)
		return ""
	}
	values := md.Get(key)
	if len(values) == 0 {
		logger.Debug("metadata key not found",
			zap.String("key", key),
		)
		return ""
	}
	value := values[0]
	logger.Debug("metadata extracted",
		zap.String("key", key),
	)
	return value
}

// GetUserID extracts user_id from metadata
func GetUserID(ctx context.Context) int64 {
	val := GetMetadata(ctx, "x-user-id")
	if val == "" {
		logger.Debug("user_id not found in metadata")
		return 0
	}
	var id int64
	n, err := fmt.Sscanf(val, "%d", &id)
	if err != nil || n != 1 {
		logger.Warn("failed to parse user_id",
			zap.String("value", val),
			zap.Error(err),
		)
		return 0
	}
	logger.Debug("user_id extracted",
		zap.Int64("user_id", id),
	)
	return id
}

// GetRequestID extracts request_id from metadata
func GetRequestID(ctx context.Context) string {
	return GetMetadata(ctx, "x-request-id")
}

// GetSessionID extracts session_id from metadata
func GetSessionID(ctx context.Context) string {
	return GetMetadata(ctx, "x-session-id")
}

// AuthInterceptorConfig holds auth interceptor configuration
type AuthInterceptorConfig struct {
	// SkipMethods - list of methods to skip auth (e.g., "/auth.AuthService/SendCode")
	SkipMethods []string
}

// JWTValidator interface for JWT validation
type JWTValidator interface {
	ValidateAccessToken(token string) (*JWTClaims, error)
}

// JWTClaims represents JWT claims extracted from an access token.
// App context fields are optional; absent App is treated as "client".
type JWTClaims struct {
	UserID   int64
	Phone    string
	DeviceID string

	// App context claims (optional; backward-compat: absent = "client")
	App     string
	OrgID   string
	OrgType string
	CityID  int64
	OrgRole string
}

// AuthContextKey is the key for auth info in context
type authContextKey struct{}

// AuthInfo holds authenticated user info extracted from the JWT.
// App context fields mirror JWTClaims; App defaults to "client" when empty.
type AuthInfo struct {
	UserID   int64
	Phone    string
	DeviceID string

	// App context claims (optional; backward-compat: absent = "client")
	App     string
	OrgID   string
	OrgType string
	CityID  int64
	OrgRole string
}

// GetAuthInfo extracts auth info from context
func GetAuthInfo(ctx context.Context) (*AuthInfo, bool) {
	info, ok := ctx.Value(authContextKey{}).(*AuthInfo)
	return info, ok
}

// MustGetAuthInfo extracts auth info from context or panics
func MustGetAuthInfo(ctx context.Context) *AuthInfo {
	info, ok := GetAuthInfo(ctx)
	if !ok {
		panic("auth info not found in context")
	}
	return info
}

// ContextWithAuthInfo returns a new context with AuthInfo set.
// Intended for use in tests where the auth interceptor is not running.
func ContextWithAuthInfo(ctx context.Context, info *AuthInfo) context.Context {
	return context.WithValue(ctx, authContextKey{}, info)
}

// AuthInterceptor creates authentication interceptor
func AuthInterceptor(validator JWTValidator, cfg AuthInterceptorConfig) grpc.UnaryServerInterceptor {
	skipMap := make(map[string]bool)
	for _, method := range cfg.SkipMethods {
		skipMap[method] = true
	}

	return func(
		ctx context.Context,
		req any,
		info *grpc.UnaryServerInfo,
		handler grpc.UnaryHandler,
	) (any, error) {
		// Skip auth for certain methods
		if skipMap[info.FullMethod] {
			return handler(ctx, req)
		}

		// Extract token from metadata
		token := GetMetadata(ctx, "authorization")
		if token == "" {
			logger.Warn("authorization token missing",
				zap.String("method", info.FullMethod),
			)
			return nil, status.Error(codes.Unauthenticated, "missing authorization token")
		}

		// Remove "Bearer " prefix if present
		if len(token) > 7 && token[:7] == "Bearer " {
			token = token[7:]
		}

		// Validate token
		claims, err := validator.ValidateAccessToken(token)
		if err != nil {
			logger.Warn("invalid token",
				zap.Error(err),
				zap.String("method", info.FullMethod),
			)
			return nil, status.Error(codes.Unauthenticated, "invalid token")
		}

		logger.Debug("token validated successfully",
			zap.String("method", info.FullMethod),
			zap.Int64("user_id", claims.UserID),
			zap.String("phone", logger.MaskPhone(claims.Phone)),
		)

		// Add auth info to context
		authInfo := &AuthInfo{
			UserID:   claims.UserID,
			Phone:    claims.Phone,
			DeviceID: claims.DeviceID,
			App:      claims.App,
			OrgID:    claims.OrgID,
			OrgType:  claims.OrgType,
			CityID:   claims.CityID,
			OrgRole:  claims.OrgRole,
		}
		ctx = context.WithValue(ctx, authContextKey{}, authInfo)

		// Also set user_id in metadata for backward compatibility
		ctx = metadata.AppendToOutgoingContext(ctx, "x-user-id", fmt.Sprintf("%d", claims.UserID))

		return handler(ctx, req)
	}
}
