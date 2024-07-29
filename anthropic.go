package fun

import (
	"bytes"
	"context"
	"io"
	"net/http"
	"slices"
	"strconv"
	"time"

	"github.com/hashicorp/go-retryablehttp"
	"gitlab.com/tozd/go/errors"
	"gitlab.com/tozd/go/x"
)

// Max output tokens for current set of models.
const anthropicMaxOutputTokens = 4096

var anthropicRateLimiter = keyedRateLimiter{}

type anthropicRequest struct {
	Model       string        `json:"model"`
	Messages    []ChatMessage `json:"messages"`
	MaxTokens   int           `json:"max_tokens"`
	System      string        `json:"system,omitempty"`
	Temperature float64       `json:"temperature"`
}

type anthropicResponse struct {
	ID      string `json:"id"`
	Type    string `json:"type"`
	Role    string `json:"role"`
	Content []struct {
		Type string `json:"type"`
		Text string `json:"text"`
	} `json:"content"`
	Model        string  `json:"model"`
	StopReason   *string `json:"stop_reason,omitempty"`
	StopSequence *string `json:"stop_sequence,omitempty"`
	Usage        struct {
		InputTokens  int `json:"input_tokens"`
		OutputTokens int `json:"output_tokens"`
	} `json:"usage"`
	Error *struct {
		Message string `json:"message"`
		Type    string `json:"type"`
	} `json:"error,omitempty"`
}

func parseAnthropicRateLimitHeaders(resp *http.Response) (
	limitRequests, limitTokens,
	remainingRequests, remainingTokens int,
	resetRequests, resetTokens time.Time,
	ok bool, errE errors.E,
) {
	limitRequestsStr := resp.Header.Get("Anthropic-Ratelimit-Requests-Limit")         // Request per minute.
	limitTokensStr := resp.Header.Get("Anthropic-Ratelimit-Tokens-Limit")             // Tokens per minute or day.
	remainingRequestsStr := resp.Header.Get("Anthropic-Ratelimit-Requests-Remaining") // Remaining requests in current window (a minute).
	remainingTokensStr := resp.Header.Get("Anthropic-Ratelimit-Tokens-Remaining")     // Remaining tokens in current window (a minute or a day).
	resetRequestsStr := resp.Header.Get("Anthropic-Ratelimit-Requests-Reset")         // When will requests window reset.
	resetTokensStr := resp.Header.Get("Anthropic-Ratelimit-Tokens-Reset")             // When will tokens window reset.

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
	resetRequests, err = time.Parse(time.RFC3339, resetRequestsStr)
	if err != nil {
		errE = errors.WithStack(err)
		return
	}
	resetTokens, err = time.Parse(time.RFC3339, resetTokensStr)
	if err != nil {
		errE = errors.WithStack(err)
		return
	}

	return
}

var _ TextProvider = (*AnthropicTextProvider)(nil)

type AnthropicTextProvider struct {
	Client *http.Client
	APIKey string
	Model  string

	Temperature float64

	system   string
	messages []ChatMessage
}

func (a *AnthropicTextProvider) Init(ctx context.Context, messages []ChatMessage) errors.E {
	if a.messages != nil {
		return errors.New("already initialized")
	}
	assistantOnlyMessages := []ChatMessage{}
	for _, message := range messages {
		if message.Role == "system" {
			if a.system != "" {
				return errors.New("multiple system messages")
			}
			a.system = message.Content
		} else {
			assistantOnlyMessages = append(assistantOnlyMessages, message)
		}
	}
	a.messages = assistantOnlyMessages

	if a.Client == nil {
		client := retryablehttp.NewClient()
		// TODO: Configure logger.
		client.Logger = nil
		client.RetryWaitMin = 100 * time.Millisecond
		client.RetryWaitMax = 5 * time.Second
		client.PrepareRetry = func(req *http.Request) error {
			estimatedTokens := a.estimatedTokens()
			// Rate limit retries.
			return anthropicRateLimiter.Take(req.Context(), a.APIKey, map[string]int{
				"rpm": 1,
				"tpd": estimatedTokens,
				"tpm": estimatedTokens,
			})
		}
		client.CheckRetry = func(ctx context.Context, resp *http.Response, err error) (bool, error) {
			limitRequests, limitTokens, remainingRequests, remainingTokens, resetRequests, resetTokens, ok, errE := parseAnthropicRateLimitHeaders(resp)
			if errE != nil {
				return false, errE
			}
			if ok {
				rateLimits := map[string]any{
					"rpm": resettingRateLimit{
						Limit:     limitRequests,
						Remaining: remainingRequests,
						Window:    time.Minute,
						Resets:    resetRequests,
					},
				}
				// Token rate limit headers can be returned for both minute or day, whichever is smaller. Except for
				// the free tier, tokens per day are equal or larger than 1,000,000, so we compare to determine which one it is.
				if limitTokens >= 1_000_000 {
					rateLimits["tpd"] = resettingRateLimit{
						Limit:     limitTokens,
						Remaining: remainingTokens,
						Window:    24 * time.Hour,
						Resets:    resetTokens,
					}
				} else {
					rateLimits["tpm"] = resettingRateLimit{
						Limit:     limitTokens,
						Remaining: remainingTokens,
						Window:    time.Minute,
						Resets:    resetTokens,
					}
				}
				anthropicRateLimiter.Set(a.APIKey, rateLimits)
			}
			return retryablehttp.ErrorPropagatedRetryPolicy(ctx, resp, err)
		}
		a.Client = client.StandardClient()
	}

	return nil
}

func (a *AnthropicTextProvider) Chat(ctx context.Context, message ChatMessage) (string, errors.E) {
	messages := slices.Clone(a.messages)
	messages = append(messages, message)

	request, errE := x.MarshalWithoutEscapeHTML(anthropicRequest{
		Model:       a.Model,
		Messages:    messages,
		MaxTokens:   anthropicMaxOutputTokens,
		System:      a.system,
		Temperature: a.Temperature,
	})
	if errE != nil {
		return "", errE
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, "https://api.anthropic.com/v1/messages", bytes.NewReader(request))
	if err != nil {
		return "", errors.WithStack(err)
	}
	req.Header.Add("x-api-key", a.APIKey)
	req.Header.Add("anthropic-version", "2023-06-01")
	estimatedTokens := a.estimatedTokens()
	// Rate limit the initial request.
	errE = anthropicRateLimiter.Take(req.Context(), a.APIKey, map[string]int{
		"rpm": 1,
		"tpd": estimatedTokens,
		"tpm": estimatedTokens,
	})
	if errE != nil {
		return "", errE
	}
	resp, err := a.Client.Do(req)
	if err != nil {
		return "", errors.WithStack(err)
	}
	defer resp.Body.Close()
	defer io.Copy(io.Discard, resp.Body)

	var response anthropicResponse
	errE = x.DecodeJSON(resp.Body, &response)
	if errE != nil {
		return "", errE
	}

	if response.Error != nil {
		return "", errors.Errorf("error response: %s", response.Error.Message)
	}

	if len(response.Content) != 1 {
		return "", errors.New("unexpected number of content")
	}
	if response.Content[0].Type != "text" {
		return "", errors.New("not text content")
	}

	if response.StopReason == nil {
		return "", errors.New("missing stop reason")

	}
	if *response.StopReason != "end_turn" {
		return "", errors.Errorf("unexpected stop reason: %s", *response.StopReason)
	}
	if response.Usage.InputTokens+response.Usage.OutputTokens > estimatedTokens {
		return "", errors.New("used tokens over estimated tokens")
	}

	// TODO: Log/expose response.Usage.

	return response.Content[0].Text, nil
}

func (a *AnthropicTextProvider) estimatedTokens() int {
	// We estimate tokens from training messages (including system message) by
	// dividing number of characters by 4.
	messages := 0
	for _, message := range a.messages {
		messages += len(message.Content) / 4
	}
	if a.system != "" {
		messages += len(a.system) / 4
	}
	// Each output can be up to anthropicMaxOutputTokens so we assume final output
	// is at most that, with input the same.
	return messages + 2*anthropicMaxOutputTokens
}
