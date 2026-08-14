package common

import (
	"context"

	"github.com/AliyunContainerService/ack-secret-manager/pkg/utils"
)

// defaultFetchMaxAttempts is the total number of fetch attempts (including
// the initial call) made by FetchWithRetry.
const defaultFetchMaxAttempts = 3

// FetchWithRetry retries transient errors up to defaultFetchMaxAttempts total
// attempts with exponential backoff (interruptible via ctx; see
// utils.RetryOnTransient). Callers MUST pass the reconcile request-scoped ctx
// so backoff waits are canceled with the reconcile. Retries do not acquire
// extra limiter tokens. The SDK built-in Autoretry must stay disabled to
// avoid double-retry.
func FetchWithRetry(ctx context.Context, fn func() error) error {
	return utils.RetryOnTransient(ctx, defaultFetchMaxAttempts, fn)
}
