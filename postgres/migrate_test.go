package postgres

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

func TestRunMigrations_ContextCancellation(t *testing.T) {
	cfg := Config{
		Host:     "localhost",
		Port:     5432,
		User:     "fake_user",
		Password: "fake_password",
		Database: "fake_db",
		SSLMode:  "disable",
	}

	// Test with cancelled context
	ctx, cancel := context.WithCancel(context.Background())
	cancel() // Cancel immediately

	err := RunMigrations(ctx, cfg, "migrations")
	if err == nil {
		t.Fatal("expected error for cancelled context, got nil")
	}

	if ctx.Err() == nil {
		t.Error("expected context to be cancelled")
	}
}

func TestRunMigrations_ContextTimeout(t *testing.T) {
	cfg := Config{
		Host:     "localhost",
		Port:     5432,
		User:     "fake_user",
		Password: "fake_password",
		Database: "fake_db",
		SSLMode:  "disable",
	}

	// Test with very short timeout
	ctx, cancel := context.WithTimeout(context.Background(), 1*time.Nanosecond)
	defer cancel()

	// Wait a bit to ensure timeout
	time.Sleep(10 * time.Millisecond)

	err := RunMigrations(ctx, cfg, "migrations")
	if err == nil {
		t.Fatal("expected error for timeout context, got nil")
	}
}

func TestRunMigrations_InvalidPath(t *testing.T) {
	cfg := Config{
		Host:     "localhost",
		Port:     5432,
		User:     "fake_user",
		Password: "fake_password",
		Database: "fake_db",
		SSLMode:  "disable",
	}

	// Test with invalid path (this will fail at filepath.Abs or migrate.New)
	ctx := context.Background()
	err := RunMigrations(ctx, cfg, "/nonexistent/path/to/migrations")
	
	// We expect an error, but the exact error depends on the system
	// The important thing is that it doesn't panic
	if err == nil {
		t.Error("expected error for invalid path, got nil")
	}
}

func TestRunMigrations_DefaultTimeout(t *testing.T) {
	cfg := Config{
		Host:     "localhost",
		Port:     5432,
		User:     "fake_user",
		Password: "fake_password",
		Database: "fake_db",
		SSLMode:  "disable",
	}

	// Test that default timeout is applied (30 seconds)
	ctx := context.Background()
	
	// This should create a context with 30s timeout internally
	// We can't easily test the exact timeout value, but we can verify
	// that the function doesn't panic and handles the context properly
	err := RunMigrations(ctx, cfg, "migrations")
	
	// We expect an error (connection failure or path issue), but not a panic
	// The important thing is that context handling works
	require.Error(t, err, "expected error when connecting to non-existent database")
}
