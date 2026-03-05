package postgres

import (
	"context"
	"fmt"
	"sync"
	"sync/atomic"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"gitlab.com/xakpro/cg-shared-libs/logger"
	"go.uber.org/zap"
)

// ReplicaPoolConfig holds configuration for primary + replicas
type ReplicaPoolConfig struct {
	// Primary (writer) configuration
	Primary Config `yaml:"primary"`

	// Read replicas configuration
	Replicas []Config `yaml:"replicas"`

	// LoadBalancing strategy: "round-robin", "random", "least-connections"
	LoadBalancing string `yaml:"load_balancing" env:"DB_LOAD_BALANCING" env-default:"round-robin"`

	// MaxReplicaLag maximum acceptable replication lag in seconds
	MaxReplicaLag time.Duration `yaml:"max_replica_lag" env:"DB_MAX_REPLICA_LAG" env-default:"5s"`

	// FallbackToPrimary if all replicas are unhealthy, fallback to primary for reads
	FallbackToPrimary bool `yaml:"fallback_to_primary" env:"DB_FALLBACK_TO_PRIMARY" env-default:"true"`
}

// ReplicaPool manages connections to primary and read replicas
// Implements Database interface
type ReplicaPool struct {
	primary  *Pool
	replicas []*replicaConn
	config   ReplicaPoolConfig
	counter  int64 // for round-robin
}

type replicaConn struct {
	pool *Pool
	mu   sync.RWMutex
	healthy bool
	lag     time.Duration
}

// NewReplicaPool creates a new replica-aware connection pool
func NewReplicaPool(ctx context.Context, cfg ReplicaPoolConfig) (*ReplicaPool, error) {
	// Connect to primary
	primary, err := New(ctx, cfg.Primary)
	if err != nil {
		return nil, fmt.Errorf("connect to primary: %w", err)
	}

	logger.Info("Connected to primary database",
		zap.String("host", cfg.Primary.Host),
		zap.Int("port", cfg.Primary.Port),
	)

	// Connect to replicas
	replicas := make([]*replicaConn, 0, len(cfg.Replicas))
	for i, replicaCfg := range cfg.Replicas {
		replica, err := New(ctx, replicaCfg)
		if err != nil {
			logger.Warn("Failed to connect to replica, skipping",
				zap.Int("replica_index", i),
				zap.String("host", replicaCfg.Host),
				zap.Error(err),
			)
			continue
		}

		replicas = append(replicas, &replicaConn{
			pool:    replica,
			healthy: true,
			lag:     0,
		})

		logger.Info("Connected to replica database",
			zap.Int("replica_index", i),
			zap.String("host", replicaCfg.Host),
			zap.Int("port", replicaCfg.Port),
		)
	}

	rp := &ReplicaPool{
		primary:  primary,
		replicas: replicas,
		config:   cfg,
	}

	// Start health checker
	go rp.healthChecker(ctx)

	return rp, nil
}

// Writer returns the primary (write) connection pool
func (rp *ReplicaPool) Writer() *Pool {
	return rp.primary
}

// Reader returns a read replica connection pool using load balancing
func (rp *ReplicaPool) Reader() *Pool {
	// Get healthy replicas
	healthy := rp.healthyReplicas()

	if len(healthy) == 0 {
		if rp.config.FallbackToPrimary {
			logger.Warn("No healthy replicas, falling back to primary")
			return rp.primary
		}
		// Return first replica even if unhealthy (better than nil)
		if len(rp.replicas) > 0 {
			return rp.replicas[0].pool
		}
		return rp.primary
	}

	// Load balancing
	switch rp.config.LoadBalancing {
	case "round-robin":
		idx := atomic.AddInt64(&rp.counter, 1)
		return healthy[int(idx)%len(healthy)].pool
	case "random":
		// Use counter as pseudo-random (good enough for distribution)
		idx := atomic.AddInt64(&rp.counter, 1) * 31
		return healthy[int(idx)%len(healthy)].pool
	case "least-lag":
		return rp.leastLagReplica(healthy).pool
	default:
		idx := atomic.AddInt64(&rp.counter, 1)
		return healthy[int(idx)%len(healthy)].pool
	}
}

// Primary returns the primary pool (alias for Writer)
func (rp *ReplicaPool) Primary() *Pool {
	return rp.primary
}

// Replica returns a replica pool (alias for Reader)
func (rp *ReplicaPool) Replica() *Pool {
	return rp.Reader()
}

// Close closes all connections
func (rp *ReplicaPool) Close() {
	if rp.primary != nil {
		rp.primary.Close()
	}
	for _, r := range rp.replicas {
		if r.pool != nil {
			r.pool.Close()
		}
	}
	logger.Info("Replica pool closed")
}

// healthyReplicas returns replicas that are healthy and within lag threshold
func (rp *ReplicaPool) healthyReplicas() []*replicaConn {
	healthy := make([]*replicaConn, 0, len(rp.replicas))
	for _, r := range rp.replicas {
		r.mu.RLock()
		isHealthy := r.healthy && r.lag <= rp.config.MaxReplicaLag
		r.mu.RUnlock()
		if isHealthy {
			healthy = append(healthy, r)
		}
	}
	return healthy
}

// leastLagReplica returns the replica with least replication lag
func (rp *ReplicaPool) leastLagReplica(replicas []*replicaConn) *replicaConn {
	if len(replicas) == 0 {
		return nil
	}

	least := replicas[0]
	least.mu.RLock()
	leastLag := least.lag
	least.mu.RUnlock()

	for _, r := range replicas[1:] {
		r.mu.RLock()
		rLag := r.lag
		r.mu.RUnlock()
		if rLag < leastLag {
			least = r
			leastLag = rLag
		}
	}
	return least
}

// healthChecker periodically checks replica health and lag
func (rp *ReplicaPool) healthChecker(ctx context.Context) {
	ticker := time.NewTicker(5 * time.Second)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			rp.checkReplicasHealth(ctx)
		}
	}
}

// checkReplicasHealth checks health and lag of all replicas
func (rp *ReplicaPool) checkReplicasHealth(ctx context.Context) {
	for i, r := range rp.replicas {
		// Check connectivity
		err := r.pool.Pool.Ping(ctx)
		if err != nil {
			r.mu.Lock()
			r.healthy = false
			r.mu.Unlock()
			logger.Warn("Replica health check failed",
				zap.Int("replica_index", i),
				zap.Error(err),
			)
			continue
		}

		// Check replication lag
		lag, err := rp.getReplicationLag(ctx, r.pool)
		if err != nil {
			logger.Warn("Failed to get replication lag",
				zap.Int("replica_index", i),
				zap.Error(err),
			)
			// Still mark as healthy if ping succeeded
			r.mu.Lock()
			r.healthy = true
			r.lag = rp.config.MaxReplicaLag // Assume max lag if can't measure
			r.mu.Unlock()
			continue
		}

		r.mu.Lock()
		r.healthy = true
		r.lag = lag
		r.mu.Unlock()

		if lag > rp.config.MaxReplicaLag {
			logger.Warn("Replica lag exceeds threshold",
				zap.Int("replica_index", i),
				zap.Duration("lag", lag),
				zap.Duration("threshold", rp.config.MaxReplicaLag),
			)
		}
	}
}

// getReplicationLag returns the replication lag in duration
func (rp *ReplicaPool) getReplicationLag(ctx context.Context, replica *Pool) (time.Duration, error) {
	var lagSeconds float64

	query := `
		SELECT
			CASE
				WHEN pg_last_wal_receive_lsn() = pg_last_wal_replay_lsn() THEN 0
				ELSE EXTRACT(EPOCH FROM now() - pg_last_xact_replay_timestamp())
			END AS lag_seconds
	`

	err := replica.Pool.QueryRow(ctx, query).Scan(&lagSeconds)
	if err != nil {
		return 0, fmt.Errorf("query replication lag: %w", err)
	}

	return time.Duration(lagSeconds * float64(time.Second)), nil
}

// Stats returns pool statistics
func (rp *ReplicaPool) Stats() ReplicaPoolStats {
	stats := ReplicaPoolStats{
		Primary: poolStats(rp.primary.Pool),
	}

	for i, r := range rp.replicas {
		r.mu.RLock()
		healthy := r.healthy
		lag := r.lag
		r.mu.RUnlock()
		stats.Replicas = append(stats.Replicas, ReplicaStats{
			Index:   i,
			Healthy: healthy,
			Lag:     lag,
			Pool:    poolStats(r.pool.Pool),
		})
	}

	return stats
}

// ReplicaPoolStats holds statistics for the replica pool
type ReplicaPoolStats struct {
	Primary  PoolStats
	Replicas []ReplicaStats
}

// ReplicaStats holds statistics for a single replica
type ReplicaStats struct {
	Index   int
	Healthy bool
	Lag     time.Duration
	Pool    PoolStats
}

// PoolStats holds pgxpool statistics
type PoolStats struct {
	AcquireCount         int64
	AcquireDuration      time.Duration
	AcquiredConns        int32
	CanceledAcquireCount int64
	ConstructingConns    int32
	EmptyAcquireCount    int64
	IdleConns            int32
	MaxConns             int32
	TotalConns           int32
}

func poolStats(p *pgxpool.Pool) PoolStats {
	s := p.Stat()
	return PoolStats{
		AcquireCount:         s.AcquireCount(),
		AcquireDuration:      s.AcquireDuration(),
		AcquiredConns:        s.AcquiredConns(),
		CanceledAcquireCount: s.CanceledAcquireCount(),
		ConstructingConns:    s.ConstructingConns(),
		EmptyAcquireCount:    s.EmptyAcquireCount(),
		IdleConns:            s.IdleConns(),
		MaxConns:             s.MaxConns(),
		TotalConns:           s.TotalConns(),
	}
}

// WithTx executes function in transaction on primary
func (rp *ReplicaPool) WithTx(ctx context.Context, fn func(tx pgx.Tx) error) error {
	return rp.primary.WithTx(ctx, fn)
}

// ReadAfterWrite returns reader that guarantees consistency after write
// It uses primary for reads for a short period after writes
type ReadAfterWrite struct {
	rp            *ReplicaPool
	writeTime     time.Time
	consistencyWindow time.Duration
}

// NewReadAfterWrite creates a read-after-write consistent reader
func (rp *ReplicaPool) NewReadAfterWrite(consistencyWindow time.Duration) *ReadAfterWrite {
	return &ReadAfterWrite{
		rp:                rp,
		consistencyWindow: consistencyWindow,
	}
}

// MarkWrite marks that a write has occurred
func (raw *ReadAfterWrite) MarkWrite() {
	raw.writeTime = time.Now()
}

// Reader returns appropriate pool based on write time
func (raw *ReadAfterWrite) Reader() *Pool {
	if time.Since(raw.writeTime) < raw.consistencyWindow {
		// Recent write - use primary for consistency
		return raw.rp.primary
	}
	return raw.rp.Reader()
}

// Writer returns the primary pool
func (raw *ReadAfterWrite) Writer() *Pool {
	return raw.rp.primary
}
