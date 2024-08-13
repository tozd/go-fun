package fun

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"net/http"
	"slices"
	"sync"
	"time"

	"github.com/rs/zerolog"
	"gitlab.com/tozd/go/errors"
	"gitlab.com/tozd/go/x"
)

//nolint:gomnd
var openAIModels = map[string]struct { //nolint:gochecknoglobals
	MaxContextLength  int
	MaxResponseLength int
}{
	"gpt-4o-2024-08-06": {
		MaxContextLength:  128_000,
		MaxResponseLength: 16_384,
	},
	"gpt-4o-2024-05-13": {
		MaxContextLength:  128_000,
		MaxResponseLength: 4_096,
	},
	"gpt-4o-mini-2024-07-18": {
		MaxContextLength:  128_000,
		MaxResponseLength: 16_384,
	},
	"gpt-4-turbo-2024-04-09": {
		MaxContextLength:  128_000,
		MaxResponseLength: 4_096,
	},
}

var openAIRateLimiter = keyedRateLimiter{ //nolint:gochecknoglobals
	mu:       sync.RWMutex{},
	limiters: map[string]map[string]any{},
}

type openAIRequest struct {
	Messages    []ChatMessage `json:"messages"`
	Model       string        `json:"model"`
	Seed        int           `json:"seed"`
	Temperature float64       `json:"temperature"`
	MaxTokens   int           `json:"max_tokens"`
}

type openAIResponse struct {
	ID                string  `json:"id"`
	Object            string  `json:"object"`
	Created           int64   `json:"created"`
	Model             string  `json:"model"`
	SystemFingerprint string  `json:"system_fingerprint"`
	ServiceTier       *string `json:"service_tier,omitempty"`
	Choices           []struct {
		Index   int `json:"index"`
		Message struct {
			Role    string  `json:"role"`
			Content *string `json:"content,omitempty"`
			Refusal *string `json:"refusal,omitempty"`
		} `json:"message"`
		FinishReason string `json:"finish_reason"`
	} `json:"choices"`
	Usage struct {
		PromptTokens     int `json:"prompt_tokens"`
		CompletionTokens int `json:"completion_tokens"`
		TotalTokens      int `json:"total_tokens"`
	} `json:"usage"`
	Error *struct {
		Message string  `json:"message"`
		Type    string  `json:"type"`
		Code    *string `json:"code,omitempty"`
		Param   *string `json:"param,omitempty"`
	} `json:"error,omitempty"`
}

var _ TextProvider = (*OpenAITextProvider)(nil)

// OpenAITextProvider is a [TextProvider] which provides integration with
// text-based [OpenAI] AI models.
//
// [OpenAI]: https://openai.com/
type OpenAITextProvider struct {
	Client            *http.Client
	APIKey            string
	Model             string
	MaxContextLength  int
	MaxResponseLength int

	Seed        int
	Temperature float64

	messages []ChatMessage
}

// Init implements TextProvider interface.
func (o *OpenAITextProvider) Init(_ context.Context, messages []ChatMessage) errors.E {
	if o.messages != nil {
		return errors.WithStack(ErrAlreadyInitialized)
	}
	o.messages = messages

	if o.Client == nil {
		o.Client = newClient(
			func(req *http.Request) error {
				// Rate limit retries.
				return openAIRateLimiter.Take(req.Context(), o.APIKey, map[string]int{
					"rpm": 1,
					"tpm": o.MaxContextLength, // TODO: Can we provide a better estimate?
				})
			},
			parseRateLimitHeaders,
			func(limitRequests, limitTokens, remainingRequests, remainingTokens int, resetRequests, resetTokens time.Time) {
				openAIRateLimiter.Set(o.APIKey, map[string]any{
					"rpm": resettingRateLimit{
						Limit:     limitRequests,
						Remaining: remainingRequests,
						Window:    time.Minute,
						Resets:    resetRequests,
					},
					"tpm": resettingRateLimit{
						Limit:     limitTokens,
						Remaining: remainingTokens,
						Window:    time.Minute,
						Resets:    resetTokens,
					},
				})
			},
		)
	}

	if o.MaxContextLength == 0 {
		o.MaxContextLength = openAIModels[o.Model].MaxContextLength
	}
	if o.MaxContextLength == 0 {
		return errors.New("MaxContextLength not set")
	}

	if o.MaxResponseLength == 0 {
		o.MaxResponseLength = openAIModels[o.Model].MaxResponseLength
	}
	if o.MaxResponseLength == 0 {
		return errors.New("MaxResponseLength not set")
	}

	return nil
}

// Chat implements TextProvider interface.
func (o *OpenAITextProvider) Chat(ctx context.Context, message ChatMessage) (string, errors.E) {
	messages := slices.Clone(o.messages)
	messages = append(messages, message)

	request, errE := x.MarshalWithoutEscapeHTML(openAIRequest{
		Messages:    messages,
		Model:       o.Model,
		Seed:        o.Seed,
		Temperature: o.Temperature,
		MaxTokens:   o.MaxResponseLength, // TODO: Can we provide a better estimate?
	})
	if errE != nil {
		return "", errE
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, "https://api.openai.com/v1/chat/completions", bytes.NewReader(request))
	if err != nil {
		return "", errors.WithStack(err)
	}
	req.Header.Add("Authorization", fmt.Sprintf("Bearer %s", o.APIKey))
	req.Header.Add("Content-Type", "application/json")
	// Rate limit the initial request.
	errE = openAIRateLimiter.Take(ctx, o.APIKey, map[string]int{
		"rpm": 1,
		"tpm": o.MaxContextLength, // TODO: Can we provide a better estimate?
	})
	if errE != nil {
		return "", errE
	}
	resp, err := o.Client.Do(req)
	var requestID string
	if resp != nil {
		requestID = resp.Header.Get("X-Request-Id")
	}
	if err != nil {
		errE = errors.Prefix(err, ErrAPIRequestFailed)
		if requestID != "" {
			errors.Details(errE)["apiRequest"] = requestID
		}
		return "", errE
	}
	defer resp.Body.Close()
	defer io.Copy(io.Discard, resp.Body) //nolint:errcheck

	var response openAIResponse
	errE = x.DecodeJSON(resp.Body, &response)
	if errE != nil {
		if requestID != "" {
			errors.Details(errE)["apiRequest"] = requestID
		}
		return "", errE
	}

	if response.Error != nil {
		errE = errors.WithDetails(ErrAPIResponseError, "body", response.Error)
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

	if response.Choices[0].Message.Refusal != nil {
		errE = errors.WithDetails(
			ErrRefused,
			"refusal", *response.Choices[0].Message.Refusal,
		)
		if requestID != "" {
			errors.Details(errE)["apiRequest"] = requestID
		}
		return "", errE
	}

	if response.Choices[0].Message.Content == nil {
		errE = errors.WithStack(ErrNotTextMessage)
		if requestID != "" {
			errors.Details(errE)["apiRequest"] = requestID
		}
		return "", errE
	}

	if response.Usage.TotalTokens >= o.MaxContextLength {
		errE = errors.WithDetails(
			ErrUnexpectedNumberOfTokens,
			"content", *response.Choices[0].Message.Content,
			"prompt", response.Usage.PromptTokens,
			"response", response.Usage.CompletionTokens,
			"total", response.Usage.TotalTokens,
			"maxTotal", o.MaxContextLength,
			"maxResponse", o.MaxResponseLength,
		)
		if requestID != "" {
			errors.Details(errE)["apiRequest"] = requestID
		}
		return "", errE
	}

	tokens := zerolog.Dict()
	tokens.Int("maxTotal", o.MaxContextLength)
	tokens.Int("maxResponse", o.MaxResponseLength)
	tokens.Int("prompt", response.Usage.PromptTokens)
	tokens.Int("response", response.Usage.CompletionTokens)
	tokens.Int("total", response.Usage.TotalTokens)
	e := zerolog.Ctx(ctx).Debug().Str("model", o.Model).Dict("tokens", tokens)
	if requestID != "" {
		e = e.Str("apiRequest", requestID)
	}
	e.Msg("usage")

	return *response.Choices[0].Message.Content, nil
}
