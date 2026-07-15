package grpc

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	grpcgo "google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

const (
	getMethod    = "/test.Inventory/GetItem"
	createMethod = "/test.Inventory/CreateItem"
)

func invokeWithConfig(ctx context.Context, cfg ClientConfig, method string, invoker grpcgo.UnaryInvoker) error {
	return retryInterceptor(cfg)(ctx, method, nil, nil, nil, invoker)
}

func TestRetryInterceptor_DoesNotRetryMethodsByDefault(t *testing.T) {
	attempts := 0
	err := invokeWithConfig(context.Background(), ClientConfig{MaxRetries: 3}, createMethod,
		func(context.Context, string, any, any, *grpcgo.ClientConn, ...grpcgo.CallOption) error {
			attempts++
			return status.Error(codes.Unavailable, "transport unavailable")
		})

	require.Error(t, err)
	assert.Equal(t, 1, attempts)
}

func TestRetryInterceptor_DefaultTimeoutBoundsRetryEnabledMethod(t *testing.T) {
	var deadline time.Time
	err := invokeWithConfig(context.Background(), ClientConfig{
		MaxRetries:       3,
		RetryableMethods: []string{getMethod},
	}, getMethod, func(ctx context.Context, _ string, _, _ any, _ *grpcgo.ClientConn, _ ...grpcgo.CallOption) error {
		var ok bool
		deadline, ok = ctx.Deadline()
		require.True(t, ok)
		return nil
	})

	require.NoError(t, err)
	remaining := time.Until(deadline)
	assert.Greater(t, remaining, defaultRetryTotalTimeout-time.Second)
	assert.LessOrEqual(t, remaining, defaultRetryTotalTimeout)
}

func TestRetryInterceptor_NonRetriedCallKeepsNoDeadline(t *testing.T) {
	err := invokeWithConfig(context.Background(), ClientConfig{MaxRetries: 3}, createMethod,
		func(ctx context.Context, _ string, _, _ any, _ *grpcgo.ClientConn, _ ...grpcgo.CallOption) error {
			_, hasDeadline := ctx.Deadline()
			assert.False(t, hasDeadline)
			return nil
		})

	require.NoError(t, err)
}

func TestRetryInterceptor_RetriesOnlyExplicitlySafeMethods(t *testing.T) {
	tests := []struct {
		name string
		cfg  ClientConfig
	}{
		{
			name: "exact allowlist",
			cfg:  ClientConfig{MaxRetries: 2, RetryableMethods: []string{getMethod}},
		},
		{
			name: "service wildcard allowlist",
			cfg:  ClientConfig{MaxRetries: 2, RetryableMethods: []string{"/test.Inventory/*"}},
		},
		{
			name: "explicit all-method opt-in",
			cfg:  ClientConfig{MaxRetries: 2, RetryAllMethods: true},
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			attempts := 0
			err := invokeWithConfig(context.Background(), tc.cfg, getMethod,
				func(context.Context, string, any, any, *grpcgo.ClientConn, ...grpcgo.CallOption) error {
					attempts++
					if attempts == 1 {
						return status.Error(codes.Unavailable, "transport unavailable")
					}
					return nil
				})

			require.NoError(t, err)
			assert.Equal(t, 2, attempts)
		})
	}
}

func TestRetryInterceptor_DoesNotRetryUnsafeCodesGlobally(t *testing.T) {
	for _, code := range []codes.Code{codes.Internal, codes.Aborted, codes.ResourceExhausted} {
		t.Run(code.String(), func(t *testing.T) {
			attempts := 0
			err := invokeWithConfig(context.Background(), ClientConfig{
				MaxRetries:      3,
				RetryAllMethods: true,
			}, createMethod, func(context.Context, string, any, any, *grpcgo.ClientConn, ...grpcgo.CallOption) error {
				attempts++
				return status.Error(code, "not transport-safe")
			})

			require.Error(t, err)
			assert.Equal(t, 1, attempts)
		})
	}
}

func TestRetryInterceptor_ConfiguredTimeoutBoundsCallAndBackoff(t *testing.T) {
	started := time.Now()
	attempts := 0
	err := invokeWithConfig(context.Background(), ClientConfig{
		Timeout:          25 * time.Millisecond,
		MaxRetries:       3,
		RetryWaitTime:    2 * time.Second,
		RetryableMethods: []string{getMethod},
	}, getMethod, func(context.Context, string, any, any, *grpcgo.ClientConn, ...grpcgo.CallOption) error {
		attempts++
		return status.Error(codes.Unavailable, "transport unavailable")
	})

	require.Error(t, err)
	assert.Equal(t, codes.DeadlineExceeded, status.Code(err))
	assert.Equal(t, 1, attempts)
	assert.Less(t, time.Since(started), 500*time.Millisecond)
}

func TestRetryInterceptor_PreservesShorterCallerDeadline(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Millisecond)
	defer cancel()

	started := time.Now()
	err := invokeWithConfig(ctx, ClientConfig{
		Timeout:          time.Second,
		MaxRetries:       3,
		RetryWaitTime:    2 * time.Second,
		RetryableMethods: []string{getMethod},
	}, getMethod, func(context.Context, string, any, any, *grpcgo.ClientConn, ...grpcgo.CallOption) error {
		return status.Error(codes.Unavailable, "transport unavailable")
	})

	require.Error(t, err)
	assert.Equal(t, codes.DeadlineExceeded, status.Code(err))
	assert.Less(t, time.Since(started), 500*time.Millisecond)
}

func TestBoundedRetryDelay_UsesEqualJitterWithinCap(t *testing.T) {
	for range 100 {
		delay := boundedRetryDelay(100*time.Millisecond, 250*time.Millisecond, 10)
		assert.GreaterOrEqual(t, delay, 125*time.Millisecond)
		assert.LessOrEqual(t, delay, 250*time.Millisecond)
	}
}
