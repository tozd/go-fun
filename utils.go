package fun

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"time"

	"github.com/hashicorp/go-retryablehttp"
	"github.com/rs/zerolog"
	"gitlab.com/tozd/go/errors"
)

const (
	retryWaitMin = 100 * time.Millisecond //nolint:revive
	retryWaitMax = 5 * time.Second
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
