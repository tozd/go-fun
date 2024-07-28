package fun

import (
	"context"

	"gitlab.com/tozd/go/errors"
	"golang.org/x/time/rate"
)

type rateLimiter struct {
	limiters map[string]map[string]*rate.Limiter
}

type rateLimit struct {
	Limit rate.Limit
	Burst int
}

func (r *rateLimiter) Take(ctx context.Context, key string, ns map[string]int) errors.E {
	if r.limiters == nil {
		return nil
	}
	if r.limiters[key] == nil {
		return nil
	}

	for k, n := range ns {
		if r.limiters[key][k] != nil {
			err := r.limiters[key][k].WaitN(ctx, n)
			if err != nil {
				return errors.WithStack(err)
			}
		}
	}

	return nil
}

func (r *rateLimiter) Set(key string, rateLimits map[string]rateLimit) {
	if r.limiters == nil {
		r.limiters = make(map[string]map[string]*rate.Limiter)
	}
	if r.limiters[key] == nil {
		r.limiters[key] = make(map[string]*rate.Limiter)
	}

	for k, rl := range rateLimits {
		if r.limiters[key][k] == nil {
			r.limiters[key][k] = rate.NewLimiter(rl.Limit, rl.Burst)
		} else {
			r.limiters[key][k].SetLimit(rl.Limit)
			r.limiters[key][k].SetBurst(rl.Burst)
		}
	}
}
