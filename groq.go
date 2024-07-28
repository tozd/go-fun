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

var groqRateLimiter = rateLimiter{}

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

func parseRateLimitHeaders(resp *http.Response) (
	limitRequests, limitTokens int,
	ok bool, errE errors.E,
) {
	limitRequestsStr := resp.Header.Get("x-ratelimit-limit-requests") // Request per day.
	limitTokensStr := resp.Header.Get("x-ratelimit-limit-tokens")     // Tokens per minute.

	var err error
	ok = false

	if limitRequestsStr != "" {
		limitRequests, err = strconv.Atoi(limitRequestsStr)
		if err != nil {
			errE = errors.WithStack(err)
			return
		}
		ok = true
	}
	if limitTokensStr != "" {
		limitTokens, err = strconv.Atoi(limitTokensStr)
		if err != nil {
			errE = errors.WithStack(err)
			return
		}
		ok = true
	}

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
			// Rate limit retries.
			return groqRateLimiter.Take(req.Context(), g.APIKey, map[string]int{
				"rpm": 1,
				"rpd": 1,
				"tpm": g.MaxContextLength, // TODO: Can we provide a better estimate?
			})
		}
		client.CheckRetry = func(ctx context.Context, resp *http.Response, err error) (bool, error) {
			limitRequests, limitTokens, ok, errE := parseRateLimitHeaders(resp)
			if errE != nil {
				return false, errE
			}
			if ok {
				// Requests per minute.
				// TODO: Remove hard-coded value.
				rpm := float64(30) / time.Minute.Seconds()
				// Request per day.
				rpd := float64(limitRequests) / (24 * time.Hour).Seconds()
				// Tokens per minute.
				tpm := float64(limitTokens) / time.Minute.Seconds()
				groqRateLimiter.Set(g.APIKey, map[string]rateLimit{
					"rpm": {
						Limit: rate.Limit(rpm),
						Burst: 30,
					},
					"rpd": {
						Limit: rate.Limit(rpd),
						Burst: limitRequests,
					},
					"tpm": {
						Limit: rate.Limit(tpm),
						Burst: limitTokens,
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
	// Rate limit the initial request.
	errE := groqRateLimiter.Take(req.Context(), g.APIKey, map[string]int{
		"rpm": 1,
		"rpd": 1,
		"tpm": g.MaxContextLength, // TODO: Can we provide a better estimate?
	})
	if errE != nil {
		return errE
	}
	resp, err := g.Client.Do(req)
	if err != nil {
		return errors.WithStack(err)
	}
	defer resp.Body.Close()
	defer io.Copy(io.Discard, resp.Body)

	var model groqModel
	errE = x.DecodeJSONWithoutUnknownFields(resp.Body, &model)
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

	request, errE := x.MarshalWithoutEscapeHTML(map[string]interface{}{
		"messages":    messages,
		"model":       g.Model,
		"seed":        g.Seed,
		"temperature": g.Temperature,
		"max_tokens":  g.MaxContextLength, // TODO: Can we provide a better estimate?
	})
	if errE != nil {
		return "", errE
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, "https://api.groq.com/openai/v1/chat/completions", bytes.NewReader(request))
	if err != nil {
		return "", errors.WithStack(err)
	}
	req.Header.Add("Authorization", fmt.Sprintf("Bearer %s", g.APIKey))
	resp, err := g.Client.Do(req)
	if err != nil {
		return "", errors.WithStack(err)
	}
	defer resp.Body.Close()
	defer io.Copy(io.Discard, resp.Body)

	var response groqResponse
	errE = x.DecodeJSONWithoutUnknownFields(resp.Body, &response)
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
