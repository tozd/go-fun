package fun

import (
	"context"
	"sync"
	"time"

	"github.com/rs/zerolog"
	"gitlab.com/tozd/go/errors"
	"golang.org/x/time/rate"
)

var errTooLargeRequest = errors.Base("max limit smaller than requested n")

type keyedRateLimiter struct {
	mu       sync.RWMutex
	limiters map[string]map[string]any
}

type resettingRateLimiter struct {
	mu        sync.Mutex
	limit     int
	remaining int
	window    time.Duration
	resets    time.Time
	setC      chan struct{}
}

func (r *resettingRateLimiter) Take(ctx context.Context, n int) (time.Duration, errors.E) {
	delay := time.Duration(0)
	for {
		ok, d, errE := r.wait(ctx, n)
		delay += d
		if errE != nil {
			return delay, errE
		}
		if ok {
			return delay, nil
		}
	}
}

func (r *resettingRateLimiter) reserve(n int, now time.Time) (bool, time.Time, <-chan struct{}, errors.E) {
	r.mu.Lock()
	defer r.mu.Unlock()

	if r.limit < n {
		return false, time.Time{}, nil, errors.WithDetails(
			errTooLargeRequest,
			"limit", r.limit,
			"n", n,
		)
	}

	if r.resets.Compare(now) <= 0 {
		r.remaining = r.limit
		r.resets = now.Add(r.window)
	}

	if r.remaining >= n {
		r.remaining -= n
		return true, time.Time{}, nil, nil
	}

	return false, r.resets, r.setC, nil
}

func (r *resettingRateLimiter) wait(ctx context.Context, n int) (bool, time.Duration, errors.E) {
	now := time.Now()

	// Check if ctx is already cancelled.
	select {
	case <-ctx.Done():
		return false, 0, errors.WithStack(ctx.Err())
	default:
	}

	ok, resets, setC, errE := r.reserve(n, now)
	if ok || errE != nil {
		return ok, 0, errE
	}

	delay := resets.Sub(now)
	if delay <= 0 {
		// We do not have to wait at all, let's retry. This should never happen
		// because reserve should handle it already, but just in case.
		return false, 0, nil
	}
	timer := time.NewTimer(delay)
	defer timer.Stop()

	select {
	case <-setC:
		// Rate limit was set, let's see if we can reserve now.
		return false, time.Since(now), nil
	case <-timer.C:
		// We have waited enough.
		return false, delay, nil
	case <-ctx.Done():
		// Context was canceled.
		return false, time.Since(now), errors.WithStack(ctx.Err())
	}
}

func (r *resettingRateLimiter) Set(limit, remaining int, window time.Duration, resets time.Time) {
	r.mu.Lock()
	defer r.mu.Unlock()

	r.limit = limit
	r.remaining = remaining
	r.window = window
	r.resets = resets

	// We signal that rate limit was set and create a new channel for the next time.
	close(r.setC)
	r.setC = make(chan struct{})
}

func newResettingRateLimiter(limit, remaining int, window time.Duration, resets time.Time) *resettingRateLimiter {
	return &resettingRateLimiter{
		mu:        sync.Mutex{},
		limit:     limit,
		remaining: remaining,
		window:    window,
		resets:    resets,
		setC:      make(chan struct{}),
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

// This re-implements rate.Limiter.wait but with returning the wait time.
// See:https://github.com/golang/go/issues/68719
func wait(ctx context.Context, limiter *rate.Limiter, n int) (time.Duration, errors.E) {
	now := time.Now()

	// Check if ctx is already cancelled.
	select {
	case <-ctx.Done():
		return 0, errors.WithStack(ctx.Err())
	default:
	}

	r := limiter.ReserveN(now, n)
	if !r.OK() {
		return 0, errors.Errorf("rate: Wait(n=%d) exceeds limiter's burst", n)
	}

	// Wait if necessary.
	delay := r.DelayFrom(now)
	if delay == 0 {
		return 0, nil
	}

	// Determine wait limit.
	if deadline, ok := ctx.Deadline(); ok && deadline.Before(now.Add(delay)) {
		// We cancel the reservation because we will not be using it.
		r.CancelAt(now)
		return delay, errors.Errorf("rate: Wait(n=%d) would exceed context deadline", n)
	}

	timer := time.NewTimer(delay)
	defer timer.Stop()

	select {
	case <-timer.C:
		// We can proceed.
		return delay, nil
	case <-ctx.Done():
		// Context was canceled before we could proceed. Cancel the
		// reservation, which may permit other events to proceed sooner.
		r.Cancel()
		return time.Since(now), errors.WithStack(ctx.Err())
	}
}

func (r *keyedRateLimiter) get(key, k string) any {
	r.mu.RLock()
	defer r.mu.RUnlock()

	if r.limiters == nil {
		return nil
	}
	if r.limiters[key] == nil {
		return nil
	}

	return r.limiters[key][k]
}

func (r *keyedRateLimiter) getOrCreate(key, k string, create func() any) any {
	r.mu.Lock()
	defer r.mu.Unlock()

	if r.limiters == nil {
		r.limiters = make(map[string]map[string]any)
	}
	if r.limiters[key] == nil {
		r.limiters[key] = make(map[string]any)
	}

	if r.limiters[key][k] == nil {
		r.limiters[key][k] = create()
	}

	return r.limiters[key][k]
}

func (r *keyedRateLimiter) Take(ctx context.Context, key string, ns map[string]int) errors.E {
	delay := time.Duration(0)
	limits := []string{}

	for k, n := range ns {
		limiter := r.get(key, k)
		if limiter != nil {
			switch limiter := limiter.(type) {
			case *rate.Limiter:
				d, err := wait(ctx, limiter, n)
				if err != nil {
					return errors.WithStack(err)
				}
				delay += d
				if d > 0 {
					limits = append(limits, k)
				}
			case *resettingRateLimiter:
				d, errE := limiter.Take(ctx, n)
				if errE != nil {
					return errE
				}
				delay += d
				if d > 0 {
					limits = append(limits, k)
				}
			default:
				panic(errors.Errorf("invalid limiter type: %T", limiter))
			}
		}
	}

	if delay != 0 {
		zerolog.Ctx(ctx).Debug().Dur("delay", delay).Strs("limits", limits).Msg("rate limited")
	}

	return nil
}

func (r *keyedRateLimiter) Set(key string, rateLimits map[string]any) {
	now := time.Now()

	for k, rl := range rateLimits {
		limiter := r.getOrCreate(key, k, func() any {
			switch rateLimit := rl.(type) {
			case tokenBucketRateLimit:
				return rate.NewLimiter(rateLimit.Limit, rateLimit.Burst)
			case resettingRateLimit:
				return newResettingRateLimiter(rateLimit.Limit, rateLimit.Remaining, rateLimit.Window, rateLimit.Resets)
			default:
				panic(errors.Errorf("invalid rate limit type: %T", rl))
			}
		})
		switch l := limiter.(type) {
		case *rate.Limiter:
			rateLimit, ok := rl.(tokenBucketRateLimit)
			if !ok {
				panic(errors.Errorf("mismatch between limiter type (%T) and rate limit type (%T)", l, rl))
			}
			if l.Limit() != rateLimit.Limit {
				l.SetLimitAt(now, rateLimit.Limit)
			}
			if l.Burst() != rateLimit.Burst {
				l.SetBurstAt(now, rateLimit.Burst)
			}
		case *resettingRateLimiter:
			rateLimit, ok := rl.(resettingRateLimit)
			if !ok {
				panic(errors.Errorf("mismatch between limiter type (%T) and rate limit type (%T)", l, rl))
			}
			l.Set(rateLimit.Limit, rateLimit.Remaining, rateLimit.Window, rateLimit.Resets)
		default:
			panic(errors.Errorf("invalid limiter type: %T", limiter))
		}
	}
}
