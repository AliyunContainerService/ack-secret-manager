package common

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/alibabacloud-go/tea/tea"

	"github.com/AliyunContainerService/ack-secret-manager/pkg/utils"
)

// NOTE: the backoff helpers mutate the process-global
// utils.BACKOFF_DEFAULT_RETRY_INTERVAL, so tests rely on serial execution
// and must never call t.Parallel(). Generic retry semantics are covered by
// utils.TestRetryOnTransient; only FetchWithRetry's own contract is here.

// shortenBackoff shrinks the backoff interval for tests; returns a restore
// function.
func shortenBackoff(t *testing.T) func() {
	t.Helper()
	orig := utils.BACKOFF_DEFAULT_RETRY_INTERVAL
	utils.BACKOFF_DEFAULT_RETRY_INTERVAL = time.Millisecond
	return func() { utils.BACKOFF_DEFAULT_RETRY_INTERVAL = orig }
}

// lengthenBackoff: canonical rationale lives on the identically named helper
// in pkg/utils/util_test.go (both implementations are identical).
func lengthenBackoff(t *testing.T) func() {
	t.Helper()
	orig := utils.BACKOFF_DEFAULT_RETRY_INTERVAL
	utils.BACKOFF_DEFAULT_RETRY_INTERVAL = 100 * time.Millisecond
	return func() { utils.BACKOFF_DEFAULT_RETRY_INTERVAL = orig }
}

// newTransientErr returns a *tea.SDKError classified as transient (throttling).
func newTransientErr() error {
	return &tea.SDKError{
		Code:       tea.String(utils.REJECTED_THROTTLING),
		StatusCode: tea.Int(429),
		Message:    tea.String("request throttled"),
	}
}

// newPermanentErr returns a *tea.SDKError classified as permanent (forbidden).
func newPermanentErr() error {
	return &tea.SDKError{
		Code:       tea.String("Forbidden.RAM"),
		StatusCode: tea.Int(403),
		Message:    tea.String("permission denied"),
	}
}

// errWrap wraps an error the way the KMS/OOS fetch paths do.
func errWrap(err error) error {
	return fmt.Errorf("fetch failed: %w", err)
}

// TestFetchWithRetryDefaultMaxAttempts pins the fixed attempt count: a
// permanent transient failure is tried exactly defaultFetchMaxAttempts times
// and the last error is returned unchanged.
func TestFetchWithRetryDefaultMaxAttempts(t *testing.T) {
	defer shortenBackoff(t)()

	calls := 0
	err := FetchWithRetry(context.Background(), func() error {
		calls++
		return newTransientErr()
	})
	if err == nil {
		t.Fatalf("expected error after exhausting attempts, got nil")
	}
	if calls != defaultFetchMaxAttempts {
		t.Fatalf("expected %d attempts (defaultFetchMaxAttempts), got %d", defaultFetchMaxAttempts, calls)
	}
	// Error classification passes through unchanged: the returned error is
	// still transient/retryable.
	if !utils.JudgeNeedRetry(err) {
		t.Errorf("expected last transient error to be returned, got %v", err)
	}
}

// TestFetchWithRetryNonRetryableClassification: a non-SDK error is not
// retryable and is returned immediately after a single attempt (permanent
// tea.SDKError classification is covered by utils.TestJudgeNeedRetry).
func TestFetchWithRetryNonRetryableClassification(t *testing.T) {
	defer shortenBackoff(t)()

	calls := 0
	err := FetchWithRetry(context.Background(), func() error {
		calls++
		return errors.New("unexpected failure")
	})
	if err == nil {
		t.Fatalf("expected error, got nil")
	}
	if calls != 1 {
		t.Fatalf("expected exactly 1 attempt for a non-retryable error, got %d", calls)
	}
	if utils.JudgeNeedRetry(err) {
		t.Errorf("expected returned error to be non-retryable, got %v", err)
	}
}

// TestFetchWithRetryPermanentErrorClassification: a permanent SDK error is
// returned immediately after a single attempt and stays non-retryable.
func TestFetchWithRetryPermanentErrorClassification(t *testing.T) {
	defer shortenBackoff(t)()

	calls := 0
	err := FetchWithRetry(context.Background(), func() error {
		calls++
		return newPermanentErr()
	})
	if err == nil {
		t.Fatalf("expected error, got nil")
	}
	if calls != 1 {
		t.Fatalf("expected exactly 1 attempt for a permanent error, got %d", calls)
	}
	if utils.JudgeNeedRetry(err) {
		t.Errorf("expected returned error to be non-retryable, got %v", err)
	}
}

// TestFetchWithRetryWrappedTransientError: transient errors wrapped via
// fmt.Errorf("%w") are still classified as transient and retried up to the
// fixed attempt count.
func TestFetchWithRetryWrappedTransientError(t *testing.T) {
	defer shortenBackoff(t)()

	calls := 0
	err := FetchWithRetry(context.Background(), func() error {
		calls++
		if calls < defaultFetchMaxAttempts {
			// Wrapped errors must still be classified as transient.
			return errWrap(newTransientErr())
		}
		return nil
	})
	if err != nil {
		t.Fatalf("expected nil error after retrying wrapped transient error, got %v", err)
	}
	if calls != defaultFetchMaxAttempts {
		t.Fatalf("expected %d calls, got %d", defaultFetchMaxAttempts, calls)
	}
}

// TestFetchWithRetryCtxCancelledDuringBackoff: cancellation while waiting
// for the backoff returns a combined error carrying both context.Canceled
// and the last transient error.
func TestFetchWithRetryCtxCancelledDuringBackoff(t *testing.T) {
	defer lengthenBackoff(t)()

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	calls := 0
	err := FetchWithRetry(ctx, func() error {
		calls++
		if calls == 1 {
			cancel()
		}
		return newTransientErr()
	})
	if err == nil {
		t.Fatalf("expected error, got nil")
	}
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("expected context.Canceled, got %v", err)
	}
	// The cancellation error must also carry the last transient error.
	if !strings.Contains(err.Error(), "last transient error") {
		t.Fatalf("expected combined error to contain the last transient error, got %v", err)
	}
	if calls != 1 {
		t.Fatalf("expected 1 call before cancellation, got %d", calls)
	}
}

// TestFetchWithRetryCtxCancelledBeforeStart: with an already-canceled
// context the first attempt still executes and cancellation aborts the
// backoff wait that follows it.
func TestFetchWithRetryCtxCancelledBeforeStart(t *testing.T) {
	// A LONG backoff is required: with ~1ms both the timer and ctx.Done()
	// are ready and Go picks at random, making the call-count assertion
	// flaky (see lengthenBackoff).
	defer lengthenBackoff(t)()

	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	calls := 0
	err := FetchWithRetry(ctx, func() error {
		calls++
		return newTransientErr()
	})
	if err == nil {
		t.Fatalf("expected error, got nil")
	}
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("expected context.Canceled, got %v", err)
	}
	if !strings.Contains(err.Error(), "last transient error") || !strings.Contains(err.Error(), utils.REJECTED_THROTTLING) {
		t.Fatalf("expected combined error to contain the last transient error, got %v", err)
	}
	// The first attempt still executes; cancellation aborts the backoff wait.
	if calls != 1 {
		t.Fatalf("expected 1 call before cancellation, got %d", calls)
	}
}
