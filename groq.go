package fun

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"net/http"
	"slices"
	"strconv"
	"sync"
	"time"

	"github.com/hashicorp/go-retryablehttp"
	"github.com/rs/zerolog"
	"gitlab.com/tozd/go/errors"
	"gitlab.com/tozd/go/x"
	"golang.org/x/time/rate"
)

var groqRateLimiter = keyedRateLimiter{ //nolint:gochecknoglobals
	mu:       sync.RWMutex{},
	limiters: map[string]map[string]any{},
}

type groqModel struct {
	ID            string     `json:"id"`
	Object        string     `json:"object"`
	Created       int64      `json:"created"`
	OwnedBy       string     `json:"owned_by"`
	Active        bool       `json:"active"`
	ContextWindow int        `json:"context_window"`
	PublicApps    []struct{} `json:"public_apps,omitempty"`
	Error         *struct {
		Message string `json:"message"`
		Type    string `json:"type"`
		Code    string `json:"code"`
	} `json:"error,omitempty"`
}

type groqRequest struct {
	Messages    []ChatMessage `json:"messages"`
	Model       string        `json:"model"`
	Seed        int           `json:"seed"`
	Temperature float64       `json:"temperature"`
	MaxTokens   int           `json:"max_tokens"`
}

type groqResponse struct {
	ID                string  `json:"id"`
	Object            string  `json:"object"`
	Created           int64   `json:"created"`
	Model             string  `json:"model"`
	SystemFingerprint *string `json:"system_fingerprint,omitempty"`
	Choices           []struct {
		Index   int `json:"index"`
		Message struct {
			Role    string `json:"role"`
			Content string `json:"content"`
		} `json:"message"`
		FinishReason     string    `json:"finish_reason"`
		LogProbabilities []float64 `json:"logprobs,omitempty"`
	} `json:"choices"`
	Usage struct {
		PromptTokens     int     `json:"prompt_tokens"`
		CompletionTokens int     `json:"completion_tokens"`
		TotalTokens      int     `json:"total_tokens"`
		PromptTime       float64 `json:"prompt_time"`
		CompletionTime   float64 `json:"completion_time"`
		TotalTime        float64 `json:"total_time"`
	} `json:"usage"`
	XGroq struct {
		ID string `json:"id"`
	} `json:"x_groq"`
	Error *struct {
		Message string `json:"message"`
		Type    string `json:"type"`
		Code    string `json:"code"`
	} `json:"error,omitempty"`
}

func parseGroqRateLimitHeaders(resp *http.Response) ( //nolint:nonamedreturns
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

var _ TextProvider = (*GroqTextProvider)(nil)

// GroqTextProvider is a [TextProvider] which provides integration with
// text-based [Groq] AI models.
//
// [Groq]: https://groq.com/
type GroqTextProvider struct {
	Client           *http.Client
	APIKey           string
	Model            string
	MaxContextLength int

	Seed        int
	Temperature float64

	messages []ChatMessage
}

// Init implements TextProvider interface.
func (g *GroqTextProvider) Init(ctx context.Context, messages []ChatMessage) errors.E {
	if g.messages != nil {
		return errors.WithStack(ErrAlreadyInitialized)
	}
	g.messages = messages

	if g.Client == nil {
		client := retryablehttp.NewClient()
		// TODO: Configure logger which should log to a logger in ctx.
		//       See: https://github.com/hashicorp/go-retryablehttp/issues/182
		client.Logger = nil
		client.RetryWaitMin = retryWaitMin
		client.RetryWaitMax = retryWaitMax
		client.PrepareRetry = func(req *http.Request) error {
			if req.URL.Path == "/openai/v1/chat/completions" {
				// Rate limit retries.
				return groqRateLimiter.Take(req.Context(), g.APIKey, map[string]int{
					"rpm": 1,
					"rpd": 1,
					"tpm": g.MaxContextLength, // TODO: Can we provide a better estimate?
				})
			}
			return nil
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
				zerolog.Ctx(ctx).Warn().Str("body", string(body)).Msg("hit rate limit")
			}
			limitRequests, limitTokens, remainingRequests, remainingTokens, resetRequests, resetTokens, ok, errE := parseGroqRateLimitHeaders(resp)
			if errE != nil {
				return false, errE
			}
			if ok {
				groqRateLimiter.Set(g.APIKey, map[string]any{
					"rpm": tokenBucketRateLimit{
						// TODO: Correctly implement this rate limit.
						//       Currently there are not headers for this limit, so we are simulating it with a token
						//       bucket rate limit with burst 1. This means that if we have a burst of requests and then
						//       a pause we do not process them as fast as we could.
						//       See: https://console.groq.com/docs/rate-limits
						//nolint:gomnd
						Limit: rate.Limit(float64(30) / time.Minute.Seconds()), // Requests per minute.
						Burst: 1,
					},
					"rpd": resettingRateLimit{
						Limit:     limitRequests,
						Remaining: remainingRequests,
						Window:    24 * time.Hour, //nolint:gomnd
						Resets:    resetRequests,
					},
					"tpm": resettingRateLimit{
						Limit:     limitTokens,
						Remaining: remainingTokens,
						Window:    time.Minute,
						Resets:    resetTokens,
					},
				})
			}
			check, err := retryablehttp.ErrorPropagatedRetryPolicy(ctx, resp, err)
			return check, errors.WithStack(err)
		}
		g.Client = client.StandardClient()
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, fmt.Sprintf("https://api.groq.com/openai/v1/models/%s", g.Model), nil)
	if err != nil {
		return errors.WithStack(err)
	}
	req.Header.Add("Authorization", fmt.Sprintf("Bearer %s", g.APIKey))
	// This endpoint does not have rate limiting.
	resp, err := g.Client.Do(req)
	var requestID string
	if resp != nil {
		requestID = resp.Header.Get("X-Request-Id")
	}
	if err != nil {
		errE := errors.WrapWith(err, ErrAPIRequestFailed)
		if requestID != "" {
			errors.Details(errE)["apiRequest"] = requestID
		}
		return errE
	}
	defer resp.Body.Close()
	defer io.Copy(io.Discard, resp.Body) //nolint:errcheck

	var model groqModel
	errE := x.DecodeJSON(resp.Body, &model)
	if errE != nil {
		if requestID != "" {
			errors.Details(errE)["apiRequest"] = requestID
		}
		return errE
	}

	if model.Error != nil {
		errE := errors.WithDetails(ErrAPIResponseError, "error", model.Error)
		if requestID != "" {
			errors.Details(errE)["apiRequest"] = requestID
		}
		return errE
	}

	if !model.Active {
		errE := errors.WithStack(ErrModelNotActive)
		if requestID != "" {
			errors.Details(errE)["apiRequest"] = requestID
		}
		return errE
	}

	if g.MaxContextLength == 0 {
		g.MaxContextLength = model.ContextWindow
	}

	if g.MaxContextLength > model.ContextWindow {
		errE := errors.WithDetails(
			ErrMaxContextLengthOverModel,
			"max", g.MaxContextLength,
			"model", model.ContextWindow,
		)
		if requestID != "" {
			errors.Details(errE)["apiRequest"] = requestID
		}
		return errE
	}

	return nil
}

// Chat implements TextProvider interface.
func (g *GroqTextProvider) Chat(ctx context.Context, message ChatMessage) (string, errors.E) {
	messages := slices.Clone(g.messages)
	messages = append(messages, message)

	request, errE := x.MarshalWithoutEscapeHTML(groqRequest{
		Messages:    messages,
		Model:       g.Model,
		Seed:        g.Seed,
		Temperature: g.Temperature,
		MaxTokens:   g.MaxContextLength, // TODO: Can we provide a better estimate?
	})
	if errE != nil {
		return "", errE
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, "https://api.groq.com/openai/v1/chat/completions", bytes.NewReader(request))
	if err != nil {
		return "", errors.WithStack(err)
	}
	req.Header.Add("Authorization", fmt.Sprintf("Bearer %s", g.APIKey))
	// Rate limit the initial request.
	errE = groqRateLimiter.Take(ctx, g.APIKey, map[string]int{
		"rpm": 1,
		"rpd": 1,
		"tpm": g.MaxContextLength, // TODO: Can we provide a better estimate?
	})
	if errE != nil {
		return "", errE
	}
	resp, err := g.Client.Do(req)
	var requestID string
	if resp != nil {
		requestID = resp.Header.Get("X-Request-Id")
	}
	if err != nil {
		errE = errors.WrapWith(err, ErrAPIRequestFailed)
		if requestID != "" {
			errors.Details(errE)["apiRequest"] = requestID
		}
		return "", errE
	}
	defer resp.Body.Close()
	defer io.Copy(io.Discard, resp.Body) //nolint:errcheck

	var response groqResponse
	errE = x.DecodeJSON(resp.Body, &response)
	if errE != nil {
		if requestID != "" {
			errors.Details(errE)["apiRequest"] = requestID
		}
		return "", errE
	}

	if response.Error != nil {
		errE = errors.WithDetails(ErrAPIResponseError, "error", response.Error)
		if requestID != "" {
			errors.Details(errE)["apiRequest"] = requestID
		}
		return "", errE
	}

	if len(response.Choices) != 1 {
		errE = errors.WithDetails(ErrUnexpectedNumberOfMessages, "number", len(response.Choices))
		if requestID != "" {
			errors.Details(errE)["apiRequest"] = requestID
		}
		return "", errE
	}
	if response.Choices[0].FinishReason != "stop" {
		errE = errors.WithDetails(ErrNotDone, "reason", response.Choices[0].FinishReason)
		if requestID != "" {
			errors.Details(errE)["apiRequest"] = requestID
		}
		return "", errE
	}

	if response.Usage.TotalTokens >= g.MaxContextLength {
		errE = errors.WithDetails(
			ErrUnexpectedNumberOfTokens,
			"prompt", response.Usage.PromptTokens,
			"response", response.Usage.CompletionTokens,
			"total", response.Usage.TotalTokens,
			"max", g.MaxContextLength,
		)
		if requestID != "" {
			errors.Details(errE)["apiRequest"] = requestID
		}
		return "", errE
	}

	tokens := zerolog.Dict()
	tokens.Int("max", g.MaxContextLength)
	tokens.Int("prompt", response.Usage.PromptTokens)
	tokens.Int("response", response.Usage.CompletionTokens)
	tokens.Int("total", response.Usage.TotalTokens)
	duration := zerolog.Dict()
	duration.Dur("prompt", time.Duration(response.Usage.PromptTime*float64(time.Second)))
	duration.Dur("response", time.Duration(response.Usage.CompletionTime*float64(time.Second)))
	duration.Dur("total", time.Duration(response.Usage.TotalTime*float64(time.Second)))
	e := zerolog.Ctx(ctx).Info().Dict("duration", duration).Str("model", g.Model).Dict("tokens", tokens)
	if requestID != "" {
		e = e.Str("apiRequest", requestID)
	}
	e.Msg("usage")

	return response.Choices[0].Message.Content, nil
}
