package fun

import (
	"context"
	"sync"
	"time"

	"gitlab.com/tozd/go/errors"
	"golang.org/x/time/rate"
)

type keyedRateLimiter struct {
	limiters map[string]map[string]any
}

type resettingRateLimiter struct {
	mu        sync.Mutex
	limit     int
	remaining int
	window    time.Duration
	resets    time.Time
}

func (r *resettingRateLimiter) Take(ctx context.Context, n int) errors.E {
	r.mu.Lock()
	defer r.mu.Unlock()

	if r.limit < n {
		errE := errors.New("max limit smaller than requested n")
		errors.Details(errE)["limit"] = r.limit
		errors.Details(errE)["n"] = n
		return errE
	}

	for {
		ok, errE := r.wait(ctx, n)
		if errE != nil {
			return errE
		}
		if ok {
			return nil
		}
	}
}

func (r *resettingRateLimiter) wait(ctx context.Context, n int) (bool, errors.E) {
	now := time.Now()
	if r.resets.Compare(now) <= 0 {
		r.remaining = r.limit
		r.resets = now.Add(r.window)
	}

	if r.remaining >= n {
		r.remaining -= n
		return true, nil
	}

	// We do not use now from above but current time.Now to be more precise.
	wait := time.Until(r.resets)
	if wait <= 0 {
		// We do not have to wait at all.
		return false, nil
	}
	timer := time.NewTimer(wait)
	defer timer.Stop()

	select {
	case <-timer.C:
		// We have waited enough.
		return false, nil
	case <-ctx.Done():
		// Context was canceled.
		return false, errors.WithStack(ctx.Err())
	}
}

func (r *resettingRateLimiter) Set(limit, remaining int, window time.Duration, resets time.Time) {
	r.mu.Lock()
	defer r.mu.Unlock()

	r.limit = limit
	r.remaining = remaining
	r.window = window
	r.resets = resets
}

func newResettingRateLimiter(limit, remaining int, window time.Duration, resets time.Time) *resettingRateLimiter {
	return &resettingRateLimiter{
		limit:     limit,
		remaining: remaining,
		window:    window,
		resets:    resets,
	}
}

type resettingRateLimit struct {
	Limit     int
	Remaining int
	Window    time.Duration
	Resets    time.Time
}

type tokenBucketRateLimit struct {
	Limit rate.Limit
	Burst int
}

func (r *keyedRateLimiter) Take(ctx context.Context, key string, ns map[string]int) errors.E {
	if r.limiters == nil {
		return nil
	}
	if r.limiters[key] == nil {
		return nil
	}

	for k, n := range ns {
		if r.limiters[key][k] != nil {
			switch limiter := r.limiters[key][k].(type) {
			case *rate.Limiter:
				err := limiter.WaitN(ctx, n)
				if err != nil {
					return errors.WithStack(err)
				}
			case *resettingRateLimiter:
				errE := limiter.Take(ctx, n)
				if errE != nil {
					return errE
				}
			default:
				panic(errors.Errorf("invalid limiter type: %T", r.limiters[key][k]))
			}
		}
	}

	return nil
}

func (r *keyedRateLimiter) Set(key string, rateLimits map[string]any) {
	if r.limiters == nil {
		r.limiters = make(map[string]map[string]any)
	}
	if r.limiters[key] == nil {
		r.limiters[key] = make(map[string]any)
	}

	now := time.Now()

	for k, rl := range rateLimits {
		if r.limiters[key][k] == nil {
			switch rateLimit := rl.(type) {
			case tokenBucketRateLimit:
				r.limiters[key][k] = rate.NewLimiter(rateLimit.Limit, rateLimit.Burst)
			case resettingRateLimit:
				r.limiters[key][k] = newResettingRateLimiter(rateLimit.Limit, rateLimit.Remaining, rateLimit.Window, rateLimit.Resets)
			default:
				panic(errors.Errorf("invalid rate limit type: %T", rl))
			}
		} else {
			switch limiter := r.limiters[key][k].(type) {
			case *rate.Limiter:
				rateLimit, ok := rl.(tokenBucketRateLimit)
				if !ok {
					panic(errors.Errorf("mismatch between limiter type (%T) and rate limit type (%T)", limiter, rl))
				}
				if limiter.Limit() != rateLimit.Limit {
					limiter.SetLimitAt(now, rateLimit.Limit)
				}
				if limiter.Burst() != rateLimit.Burst {
					limiter.SetBurstAt(now, rateLimit.Burst)
				}
			case *resettingRateLimiter:
				rateLimit, ok := rl.(resettingRateLimit)
				if !ok {
					panic(errors.Errorf("mismatch between limiter type (%T) and rate limit type (%T)", limiter, rl))
				}
				limiter.Set(rateLimit.Limit, rateLimit.Remaining, rateLimit.Window, rateLimit.Resets)
			default:
				panic(errors.Errorf("invalid limiter type: %T", r.limiters[key][k]))
			}
		}
	}
}
