package fun

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"net/http"
	"slices"
	"strconv"
	"time"

	"github.com/hashicorp/go-retryablehttp"
	"gitlab.com/tozd/go/errors"
	"gitlab.com/tozd/go/x"
	"golang.org/x/time/rate"
)

var groqRateLimiter = keyedRateLimiter{}

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

func parseGroqRateLimitHeaders(resp *http.Response) (
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
		return
	}

	// We have all the headers we want.
	ok = true

	var err error
	limitRequests, err = strconv.Atoi(limitRequestsStr)
	if err != nil {
		errE = errors.WithStack(err)
		return
	}
	limitTokens, err = strconv.Atoi(limitTokensStr)
	if err != nil {
		errE = errors.WithStack(err)
		return
	}
	remainingRequests, err = strconv.Atoi(remainingRequestsStr)
	if err != nil {
		errE = errors.WithStack(err)
		return
	}
	remainingTokens, err = strconv.Atoi(remainingTokensStr)
	if err != nil {
		errE = errors.WithStack(err)
		return
	}
	resetRequestsDuration, err := time.ParseDuration(resetRequestsStr)
	if err != nil {
		errE = errors.WithStack(err)
		return
	}
	resetRequests = now.Add(resetRequestsDuration)
	resetTokensDuration, err := time.ParseDuration(resetTokensStr)
	if err != nil {
		errE = errors.WithStack(err)
		return
	}
	resetTokens = now.Add(resetTokensDuration)

	return
}

var _ TextProvider = (*GroqTextProvider)(nil)

// GroqTextProvider implements TextProvider interface.
type GroqTextProvider struct {
	Client           *http.Client
	APIKey           string
	Model            string
	MaxContextLength int

	Seed        int
	Temperature float64

	messages []ChatMessage
}

func (g *GroqTextProvider) Init(ctx context.Context, messages []ChatMessage) errors.E {
	if g.messages != nil {
		return errors.New("already initialized")
	}
	g.messages = messages

	if g.Client == nil {
		client := retryablehttp.NewClient()
		// TODO: Configure logger.
		client.Logger = nil
		client.RetryWaitMin = 100 * time.Millisecond
		client.RetryWaitMax = 5 * time.Second
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
						Limit: rate.Limit(rate.Limit(float64(30) / time.Minute.Seconds())), // Requests per minute.
						Burst: 1,
					},
					"rpd": resettingRateLimit{
						Limit:     limitRequests,
						Remaining: remainingRequests,
						Window:    24 * time.Hour,
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
			return retryablehttp.ErrorPropagatedRetryPolicy(ctx, resp, err)
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
	if err != nil {
		return errors.WithStack(err)
	}
	defer resp.Body.Close()
	defer io.Copy(io.Discard, resp.Body)

	var model groqModel
	errE := x.DecodeJSON(resp.Body, &model)
	if errE != nil {
		return errE
	}

	if model.Error != nil {
		return errors.Errorf("error response: %s", model.Error.Message)
	}

	if !model.Active {
		return errors.New("model not active")
	}

	if g.MaxContextLength == 0 {
		g.MaxContextLength = model.ContextWindow
	}

	if g.MaxContextLength > model.ContextWindow {
		return errors.New("max context length is larger than what model supports")
	}

	return nil
}

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
	errE = groqRateLimiter.Take(req.Context(), g.APIKey, map[string]int{
		"rpm": 1,
		"rpd": 1,
		"tpm": g.MaxContextLength, // TODO: Can we provide a better estimate?
	})
	if errE != nil {
		return "", errE
	}
	resp, err := g.Client.Do(req)
	if err != nil {
		return "", errors.WithStack(err)
	}
	defer resp.Body.Close()
	defer io.Copy(io.Discard, resp.Body)

	var response groqResponse
	errE = x.DecodeJSON(resp.Body, &response)
	if errE != nil {
		return "", errE
	}

	if response.Error != nil {
		return "", errors.Errorf("error response: %s", response.Error.Message)
	}

	if len(response.Choices) != 1 {
		return "", errors.New("unexpected number of choices")
	}
	if response.Choices[0].FinishReason != "stop" {
		return "", errors.New("not done")
	}

	if response.Usage.TotalTokens >= g.MaxContextLength {
		return "", errors.New("hit max context length")
	}

	// TODO: Log/expose response.Usage.

	return response.Choices[0].Message.Content, nil
}
