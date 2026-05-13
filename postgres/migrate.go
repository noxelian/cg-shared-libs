package postgres

import (
	"context"
	"errors"
	"fmt"
	"path/filepath"
	"time"

	"github.com/golang-migrate/migrate/v4"
	_ "github.com/golang-migrate/migrate/v4/database/postgres"
	_ "github.com/golang-migrate/migrate/v4/source/file"
	"github.com/4ubak/cg-shared-libs/logger"
	"go.uber.org/zap"
)

// RunMigrations applies database migrations with automatic dirty state recovery.
// It supports context cancellation and has a default timeout of 30 seconds.
//
// Parameters:
//   - ctx: Context for cancellation and timeout control
//   - cfg: PostgreSQL configuration
//   - migrationsPath: Path to migrations directory (relative or absolute)
//
// The function will:
//   - Automatically fix dirty migration state by forcing version and rolling back
//   - Apply all pending migrations
//   - Log detailed information about the migration process
func RunMigrations(ctx context.Context, cfg Config, migrationsPath string) error {
	// Create context with timeout if not already set
	var cancel context.CancelFunc
	if _, ok := ctx.Deadline(); !ok {
		ctx, cancel = context.WithTimeout(ctx, 30*time.Second)
	} else {
		cancel = func() {}
	}
	defer cancel()

	// Resolve absolute path
	absPath, err := filepath.Abs(migrationsPath)
	if err != nil {
		return fmt.Errorf("resolve migrations path: %w", err)
	}

	// Create migrator
	m, err := migrate.New(
		fmt.Sprintf("file://%s", absPath),
		cfg.DSN(),
	)
	if err != nil {
		return fmt.Errorf("init migrator: %w", err)
	}
	defer func() {
		sourceErr, dbErr := m.Close()
		if sourceErr != nil {
			logger.Warn("failed to close migration source", zap.Error(sourceErr))
		}
		if dbErr != nil {
			logger.Warn("failed to close migration database", zap.Error(dbErr))
		}
	}()

	// Check for context cancellation
	select {
	case <-ctx.Done():
		return fmt.Errorf("migration cancelled: %w", ctx.Err())
	default:
	}

	// Auto-fix dirty state
	var wasDirty bool
	var dirtyVersion uint
	if version, dirty, err := m.Version(); err == nil && dirty {
		logger.Warn("migration state is dirty, forcing current version",
			zap.Uint("version", version),
		)
		wasDirty = true
		dirtyVersion = version
		if err := m.Force(int(version)); err != nil {
			return fmt.Errorf("force migration version: %w", err)
		}
		// Rollback the failed migration one step so Up() can reapply it cleanly.
		// Using Steps(-1) (not Migrate(version-1)) — golang-migrate v4's Migrate(0)
		// always errors with "no migration found for version 0".
		if version > 0 {
			logger.Info("rolling back one step to reapply dirty migration",
				zap.Uint("from_version", version),
			)
			if err := m.Steps(-1); err != nil && !errors.Is(err, migrate.ErrNoChange) {
				return fmt.Errorf("rollback dirty migration v%d: %w", version, err)
			}
		}
	} else if err != nil && !errors.Is(err, migrate.ErrNilVersion) {
		return fmt.Errorf("read migration version: %w", err)
	}

	// Check for context cancellation again
	select {
	case <-ctx.Done():
		return fmt.Errorf("migration cancelled: %w", ctx.Err())
	default:
	}

	// Apply migrations
	if err := m.Up(); err != nil {
		if errors.Is(err, migrate.ErrNoChange) {
			if wasDirty {
				logger.Info("PostgreSQL migrations are up to date (dirty state was fixed)",
					zap.Uint("previous_dirty_version", dirtyVersion),
				)
			} else {
				logger.Info("PostgreSQL migrations are up to date")
			}
		} else {
			return fmt.Errorf("apply migrations: %w", err)
		}
	} else {
		if wasDirty {
			logger.Info("PostgreSQL migrations applied (dirty state was fixed and migration reapplied)",
				zap.Uint("previous_dirty_version", dirtyVersion),
			)
		} else {
			logger.Info("PostgreSQL migrations applied")
		}
	}

	return nil
}
