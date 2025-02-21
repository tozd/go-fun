package fun

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"strconv"
	"time"

	"github.com/hashicorp/go-retryablehttp"
	"github.com/rs/zerolog"
	"gitlab.com/tozd/go/errors"
)

const (
	retryWaitMin = 100 * time.Millisecond
	retryWaitMax = 5 * time.Second
	httpTimeout  = 5 * time.Minute
)

const applicationJSONHeader = "application/json"

func retryErrorHandler(resp *http.Response, err error, numTries int) (*http.Response, error) {
	var body []byte
	if resp != nil {
		body, _ = io.ReadAll(resp.Body)
		resp.Body.Close()
	}
	var errE errors.E
	if err != nil {
		errE = errors.Prefix(err, ErrGaveUpRetry)
	} else {
		errE = errors.WithStack(ErrGaveUpRetry)
	}
	errors.Details(errE)["attempts"] = numTries
	if body != nil {
		if resp.Header.Get("Content-Type") == applicationJSONHeader && json.Valid(body) {
			errors.Details(errE)["body"] = json.RawMessage(body)
		} else {
			errors.Details(errE)["body"] = string(body)
		}
	}
	return resp, errE
}

func newClient(
	prepareRetry retryablehttp.PrepareRetry,
	parseRateLimitHeaders func(resp *http.Response) (int, int, int, int, time.Time, time.Time, bool, errors.E),
	setRateLimit func(int, int, int, int, time.Time, time.Time),
) *http.Client {
	client := retryablehttp.NewClient()
	// TODO: Configure logger which should log to a logger in ctx.
	//       See: https://github.com/hashicorp/go-retryablehttp/issues/182
	//       See: https://gitlab.com/tozd/go/fun/-/issues/1
	client.Logger = nil
	client.RetryWaitMin = retryWaitMin
	client.RetryWaitMax = retryWaitMax
	client.HTTPClient.Timeout = httpTimeout
	if prepareRetry != nil {
		client.PrepareRetry = prepareRetry
	}
	client.CheckRetry = func(ctx context.Context, resp *http.Response, err error) (bool, error) {
		if err != nil {
			check, err := retryablehttp.ErrorPropagatedRetryPolicy(ctx, resp, err) //nolint:govet
			return check, errors.WithStack(err)
		}
		if resp.StatusCode == http.StatusTooManyRequests {
			// We read the body and provide it back.
			body, _ := io.ReadAll(resp.Body)
			resp.Body.Close()
			resp.Body = io.NopCloser(bytes.NewReader(body))
			if resp.Header.Get("Content-Type") == applicationJSONHeader && json.Valid(body) {
				zerolog.Ctx(ctx).Warn().RawJSON("body", body).Msg("hit rate limit")
			} else {
				zerolog.Ctx(ctx).Warn().Str("body", string(body)).Msg("hit rate limit")
			}
		}
		var limitRequests, limitTokens, remainingRequests, remainingTokens int
		var resetRequests, resetTokens time.Time
		var ok bool
		if parseRateLimitHeaders != nil {
			var errE errors.E
			limitRequests, limitTokens, remainingRequests, remainingTokens, resetRequests, resetTokens, ok, errE = parseRateLimitHeaders(resp)
			if errE != nil {
				return false, errE
			}
		}
		if ok && setRateLimit != nil {
			setRateLimit(limitRequests, limitTokens, remainingRequests, remainingTokens, resetRequests, resetTokens)
		}
		check, err := retryablehttp.ErrorPropagatedRetryPolicy(ctx, resp, err)
		return check, errors.WithStack(err)
	}
	client.ErrorHandler = retryErrorHandler
	return client.StandardClient()
}

func parseRateLimitHeaders(resp *http.Response) ( //nolint:nonamedreturns
	limitRequests, limitTokens,
	remainingRequests, remainingTokens int,
	resetRequests, resetTokens time.Time,
	ok bool, errE errors.E,
) {
	// We use current time and not Date header in response, because Date header has just second
	// precision, but reset headers can be in milliseconds, so it seems better to use
	// current local time, so that we do not reset the window too soon.
	now := time.Now()

	limitRequestsStr := resp.Header.Get("X-Ratelimit-Limit-Requests")         // Request per day.
	limitTokensStr := resp.Header.Get("X-Ratelimit-Limit-Tokens")             // Tokens per minute.
	remainingRequestsStr := resp.Header.Get("X-Ratelimit-Remaining-Requests") // Remaining requests in current window (a day).
	remainingTokensStr := resp.Header.Get("X-Ratelimit-Remaining-Tokens")     // Remaining tokens in current window (a minute).
	resetRequestsStr := resp.Header.Get("X-Ratelimit-Reset-Requests")         // When will requests window reset.
	resetTokensStr := resp.Header.Get("X-Ratelimit-Reset-Tokens")             // When will tokens window reset.

	if limitRequestsStr == "" || limitTokensStr == "" || remainingRequestsStr == "" || remainingTokensStr == "" || resetRequestsStr == "" || resetTokensStr == "" {
		// ok == false here.
		return //nolint:nakedret
	}

	// We have all the headers we want.
	ok = true

	var err error
	limitRequests, err = strconv.Atoi(limitRequestsStr)
	if err != nil {
		errE = errors.WithDetails(err, "value", limitRequestsStr)
		return //nolint:nakedret
	}
	limitTokens, err = strconv.Atoi(limitTokensStr)
	if err != nil {
		errE = errors.WithDetails(err, "value", limitTokensStr)
		return //nolint:nakedret
	}
	remainingRequests, err = strconv.Atoi(remainingRequestsStr)
	if err != nil {
		errE = errors.WithDetails(err, "value", remainingRequestsStr)
		return //nolint:nakedret
	}
	remainingTokens, err = strconv.Atoi(remainingTokensStr)
	if err != nil {
		errE = errors.WithDetails(err, "value", remainingTokensStr)
		return //nolint:nakedret
	}
	resetRequestsDuration, err := time.ParseDuration(resetRequestsStr)
	if err != nil {
		errE = errors.WithDetails(err, "value", resetRequestsStr)
		return //nolint:nakedret
	}
	resetRequests = now.Add(resetRequestsDuration)
	resetTokensDuration, err := time.ParseDuration(resetTokensStr)
	if err != nil {
		errE = errors.WithDetails(err, "value", resetTokensStr)
		return //nolint:nakedret
	}
	resetTokens = now.Add(resetTokensDuration)

	return //nolint:nakedret
}

func getString(data any, name string) string {
	m, ok := data.(map[string]any)
	if !ok {
		return ""
	}
	s, ok := m[name].(string)
	if !ok {
		return ""
	}
	return s
}

// contextKey is a value for use with context.WithValue. It's used as
// a pointer so it fits in an interface{} without allocation.
type contextKey struct {
	name string
}

var estimatedTokensContextKey = &contextKey{"estimated-tokens"} //nolint:gochecknoglobals

type estimatedTokens struct {
	Input  int
	Output int
}

func withEstimatedTokens(ctx context.Context, estimatedInputTokens, estimatedOutputTokens int) context.Context {
	return context.WithValue(ctx, estimatedTokensContextKey, estimatedTokens{estimatedInputTokens, estimatedOutputTokens})
}

func getEstimatedTokens(ctx context.Context) (int, int) {
	et := ctx.Value(estimatedTokensContextKey).(estimatedTokens) //nolint:forcetypeassert,errcheck
	return et.Input, et.Output
}
