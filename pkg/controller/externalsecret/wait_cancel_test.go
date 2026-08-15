package externalsecret

import (
	"context"
	"fmt"
	"testing"
	"time"
)

// TestIsWaitErrFromCancellation covers the Wait-error classification used by
// the rate_limit branch: only a canceled request context disqualifies the
// error from being reported as rate limiting.
func TestIsWaitErrFromCancellation(t *testing.T) {
	tests := []struct {
		name     string
		ctxFunc  func(parent context.Context) (context.Context, context.CancelFunc)
		err      error
		expected bool
	}{
		{
			name:     "canceled request context with cancellation error classifies as cancellation",
			ctxFunc:  context.WithCancel,
			err:      context.Canceled,
			expected: true,
		},
		{
			name:     "canceled request context with any Wait error classifies as cancellation",
			ctxFunc:  context.WithCancel,
			err:      fmt.Errorf("some wait error"),
			expected: true,
		},
		{
			name:     "live request context with timeout error stays rate limiting",
			ctxFunc:  context.WithCancel,
			err:      context.DeadlineExceeded,
			expected: false,
		},
		{
			name:     "live request context with generic error stays rate limiting",
			ctxFunc:  context.WithCancel,
			err:      fmt.Errorf("queue timeout"),
			expected: false,
		},
		{
			name:     "nil error never classifies as cancellation",
			ctxFunc:  context.WithCancel,
			err:      nil,
			expected: false,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			ctx, cancel := tt.ctxFunc(context.Background())
			defer cancel()
			if tt.expected {
				// Simulate manager shutdown canceling the request context
				// while Wait is blocked.
				cancel()
			}
			got := isWaitErrFromCancellation(ctx, tt.err)
			if got != tt.expected {
				t.Errorf("isWaitErrFromCancellation() = %v, expected %v", got, tt.expected)
			}
		})
	}
}

// TestIsWaitErrFromCancellationTimeoutCtx verifies the realistic scenario:
// the Wait call runs on a derived timeout context, but classification must
// key off the REQUEST context, so a wait-timeout with a live request context
// remains rate limiting even if the derived context later expires.
func TestIsWaitErrFromCancellationTimeoutCtx(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	waitTimeoutCtx, waitCancel := context.WithTimeout(ctx, 10*time.Millisecond)
	defer waitCancel()
	time.Sleep(20 * time.Millisecond) // let waitTimeoutCtx expire

	// Wait returns its own context's deadline error, but the request ctx is
	// still alive -> genuine rate-limit condition, not cancellation.
	if isWaitErrFromCancellation(ctx, waitTimeoutCtx.Err()) {
		t.Errorf("expected wait timeout with live request context to stay rate limiting")
	}

	// Now cancel the request context (manager shutdown) -> cancellation.
	cancel()
	if !isWaitErrFromCancellation(ctx, context.Canceled) {
		t.Errorf("expected canceled request context to classify as cancellation")
	}
}
