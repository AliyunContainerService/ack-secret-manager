package externalsecret

import (
	"context"
	"fmt"

	"golang.org/x/time/rate"
)

// ProviderLimiter rate-limits secret pulls for one provider (KMS and OOS
// each get their own instance).
type ProviderLimiter struct {
	SecretPullLimiter *rate.Limiter
}

// Wait delegates to the rate limiter with nil protection; the controller
// acquires pull permits exclusively through this wrapper.
func (p ProviderLimiter) Wait(c context.Context) error {
	if p.SecretPullLimiter == nil {
		return fmt.Errorf("secret pull limiter is empty")
	}
	return p.SecretPullLimiter.Wait(c)
}
