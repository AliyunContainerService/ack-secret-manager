package externalsecret

import (
	"context"
	"fmt"

	"golang.org/x/time/rate"
)

type KmsLimiter struct {
	SecretPullLimiter *rate.Limiter
}

// Wait delegates to the underlying rate limiter with nil protection; the
// controller acquires pull permits exclusively through this wrapper.
func (k KmsLimiter) Wait(c context.Context) error {
	if k.SecretPullLimiter == nil {
		return fmt.Errorf("secret pull limiter is empty")
	}
	return k.SecretPullLimiter.Wait(c)
}

type OosLimiter struct {
	SecretPullLimiter *rate.Limiter
}

// Wait delegates to the underlying rate limiter with nil protection; the
// controller acquires pull permits exclusively through this wrapper.
func (o OosLimiter) Wait(c context.Context) error {
	if o.SecretPullLimiter == nil {
		return fmt.Errorf("secret pull limiter is empty")
	}
	return o.SecretPullLimiter.Wait(c)
}
