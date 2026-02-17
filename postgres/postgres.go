package postgres

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"
	"gitlab.com/xakpro/cg-shared-libs/logger"
	"go.uber.org/zap"
)

// Config holds PostgreSQL connection configuration
type Config struct {
	Host            string        `yaml:"host" env:"POSTGRES_HOST" env-default:"localhost"`
	Port            int           `yaml:"port" env:"POSTGRES_PORT" env-default:"5432"`
	User            string        `yaml:"user" env:"POSTGRES_USER" env-default:"cg_user"`
	Password        string        `yaml:"password" env:"POSTGRES_PASSWORD"`
	Database        string        `yaml:"database" env:"POSTGRES_DB"`
	SSLMode         string        `yaml:"ssl_mode" env:"POSTGRES_SSL_MODE" env-default:"require"`
	MaxConns        int32         `yaml:"max_conns" env:"POSTGRES_MAX_CONNS" env-default:"25"`
	MinConns        int32         `yaml:"min_conns" env:"POSTGRES_MIN_CONNS" env-default:"5"`
	MaxConnLifetime time.Duration `yaml:"max_conn_lifetime" env:"POSTGRES_MAX_CONN_LIFETIME" env-default:"1h"`
	MaxConnIdleTime time.Duration `yaml:"max_conn_idle_time" env:"POSTGRES_MAX_CONN_IDLE_TIME" env-default:"30m"`
}

// DSN returns PostgreSQL connection string
func (c *Config) DSN() string {
	return fmt.Sprintf(
		"postgres://%s:%s@%s:%d/%s?sslmode=%s",
		c.User, c.Password, c.Host, c.Port, c.Database, c.SSLMode,
	)
}

// Database interface for read/write separation
type Database interface {
	Writer() *Pool
	Reader() *Pool
	WithTx(ctx context.Context, fn func(tx pgx.Tx) error) error
	Close()
}

// NewDatabase creates either Pool or ReplicaPool based on configuration
// If ReplicaPoolConfig is provided with replicas, creates ReplicaPool
// Otherwise creates simple Pool
func NewDatabase(ctx context.Context, cfg Config, replicaCfg *ReplicaPoolConfig) (Database, error) {
	if replicaCfg != nil && len(replicaCfg.Replicas) > 0 {
		// Use ReplicaPool if replicas are configured
		return NewReplicaPool(ctx, *replicaCfg)
	}
	// Use simple Pool
	return New(ctx, cfg)
}

// Pool wraps pgxpool.Pool with additional functionality
// Implements Database interface for backward compatibility
type Pool struct {
	*pgxpool.Pool
}

// New creates a new PostgreSQL connection pool
func New(ctx context.Context, cfg Config) (*Pool, error) {
	poolConfig, err := pgxpool.ParseConfig(cfg.DSN())
	if err != nil {
		return nil, fmt.Errorf("parse config: %w", err)
	}

	poolConfig.MaxConns = cfg.MaxConns
	poolConfig.MinConns = cfg.MinConns
	poolConfig.MaxConnLifetime = cfg.MaxConnLifetime
	poolConfig.MaxConnIdleTime = cfg.MaxConnIdleTime

	// Add query logger in development
	poolConfig.ConnConfig.Tracer = &queryTracer{}

	pool, err := pgxpool.NewWithConfig(ctx, poolConfig)
	if err != nil {
		return nil, fmt.Errorf("create pool: %w", err)
	}

	// Test connection
	if err := pool.Ping(ctx); err != nil {
		return nil, fmt.Errorf("ping database: %w", err)
	}

	logger.Info("PostgreSQL connected",
		zap.String("host", cfg.Host),
		zap.Int("port", cfg.Port),
		zap.String("database", cfg.Database),
	)

	return &Pool{Pool: pool}, nil
}

// Writer returns the pool itself (for backward compatibility with ReplicaPool interface)
func (p *Pool) Writer() *Pool {
	return p
}

// Reader returns the pool itself (for backward compatibility with ReplicaPool interface)
func (p *Pool) Reader() *Pool {
	return p
}

// Close closes the connection pool
func (p *Pool) Close() {
	if p.Pool != nil {
		p.Pool.Close()
		logger.Info("PostgreSQL connection closed")
	}
}

// Querier interface for both Pool and Tx
type Querier interface {
	Exec(ctx context.Context, sql string, arguments ...any) (pgconn.CommandTag, error)
	Query(ctx context.Context, sql string, args ...any) (pgx.Rows, error)
	QueryRow(ctx context.Context, sql string, args ...any) pgx.Row
}

// WithTx executes function within transaction
func (p *Pool) WithTx(ctx context.Context, fn func(tx pgx.Tx) error) error {
	tx, err := p.Begin(ctx)
	if err != nil {
		return fmt.Errorf("begin tx: %w", err)
	}

	defer func() {
		if p := recover(); p != nil {
			_ = tx.Rollback(ctx)
			panic(p)
		}
	}()

	if err := fn(tx); err != nil {
		if rbErr := tx.Rollback(ctx); rbErr != nil {
			logger.Error("rollback failed", zap.Error(rbErr))
		}
		return err
	}

	if err := tx.Commit(ctx); err != nil {
		return fmt.Errorf("commit tx: %w", err)
	}

	return nil
}

// queryTracer implements pgx.QueryTracer for logging
type queryTracer struct{}

type queryContextKey string

const (
	queryStartKey queryContextKey = "query_start"
	querySQLKey   queryContextKey = "query_sql"
)

func (t *queryTracer) TraceQueryStart(ctx context.Context, conn *pgx.Conn, data pgx.TraceQueryStartData) context.Context {
	ctx = context.WithValue(ctx, queryStartKey, time.Now())
	ctx = context.WithValue(ctx, querySQLKey, data.SQL)
	return ctx
}

func (t *queryTracer) TraceQueryEnd(ctx context.Context, conn *pgx.Conn, data pgx.TraceQueryEndData) {
	start, ok := ctx.Value(queryStartKey).(time.Time)
	if !ok {
		return
	}

	sql, _ := ctx.Value(querySQLKey).(string)
	duration := time.Since(start)

	// Log slow queries (> 100ms)
	if duration > 100*time.Millisecond {
		logger.Warn("slow query",
			zap.Duration("duration", duration),
			zap.String("sql", sql),
		)
	}
}

// IsNotFound checks if error is "no rows" error
func IsNotFound(err error) bool {
	return errors.Is(err, pgx.ErrNoRows)
}

// IsDuplicate checks if error is duplicate key error
func IsDuplicate(err error) bool {
	var pgErr *pgconn.PgError
	if errors.As(err, &pgErr) {
		return pgErr.Code == "23505" // unique_violation
	}
	return false
}

// IsForeignKeyViolation checks if error is foreign key violation
func IsForeignKeyViolation(err error) bool {
	var pgErr *pgconn.PgError
	if errors.As(err, &pgErr) {
		return pgErr.Code == "23503" // foreign_key_violation
	}
	return false
}
