package fun

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"slices"
	"sync"
	"time"

	"github.com/santhosh-tekuri/jsonschema/v6"
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

type openAIJSONSchema struct {
	Description string          `json:"description,omitempty"`
	Name        string          `json:"name"`
	Schema      json.RawMessage `json:"schema"`
	Strict      bool            `json:"strict"`
}

type openAIResponseFormat struct {
	Type       string           `json:"type"`
	JSONSchema openAIJSONSchema `json:"json_schema"`
}

type openAIRequest struct {
	Messages       []ChatMessage         `json:"messages"`
	Model          string                `json:"model"`
	Seed           int                   `json:"seed"`
	Temperature    float64               `json:"temperature"`
	MaxTokens      int                   `json:"max_tokens"`
	ResponseFormat *openAIResponseFormat `json:"response_format,omitempty"`
}

type openAIMessage struct {
	Role    string  `json:"role"`
	Content *string `json:"content,omitempty"`
	Refusal *string `json:"refusal,omitempty"`
}

type openAIResponse struct {
	ID                string  `json:"id"`
	Object            string  `json:"object"`
	Created           int64   `json:"created"`
	Model             string  `json:"model"`
	SystemFingerprint string  `json:"system_fingerprint"`
	ServiceTier       *string `json:"service_tier,omitempty"`
	Choices           []struct {
		Index        int           `json:"index"`
		Message      openAIMessage `json:"message"`
		FinishReason string        `json:"finish_reason"`
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

	ForceOutputJSONSchema bool

	Seed        int
	Temperature float64

	messages                    []ChatMessage
	outputJSONSchema            json.RawMessage
	outputJSONSchemaName        string
	outputJSONSchemaDescription string
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
	recorder := GetTextProviderRecorder(ctx)

	messages := slices.Clone(o.messages)
	messages = append(messages, message)

	if recorder != nil {
		for _, message := range messages {
			message := message
			o.recordMessage(recorder, openAIMessage{
				Role:    message.Role,
				Content: &message.Content,
				Refusal: nil,
			})
		}
	}

	oReq := openAIRequest{
		Messages:       messages,
		Model:          o.Model,
		Seed:           o.Seed,
		Temperature:    o.Temperature,
		MaxTokens:      o.MaxResponseLength, // TODO: Can we provide a better estimate?
		ResponseFormat: nil,
	}

	if o.outputJSONSchema != nil {
		oReq.ResponseFormat = &openAIResponseFormat{
			Type: "json_schema",
			JSONSchema: openAIJSONSchema{
				Description: o.outputJSONSchemaDescription,
				Name:        o.outputJSONSchemaName,
				Schema:      o.outputJSONSchema,
				Strict:      true,
			},
		}
	}

	request, errE := x.MarshalWithoutEscapeHTML(oReq)
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

	if requestID == "" {
		return "", errors.WithStack(ErrMissingRequestID)
	}

	var response openAIResponse
	errE = x.DecodeJSON(resp.Body, &response)
	if errE != nil {
		errors.Details(errE)["apiRequest"] = requestID
		return "", errE
	}

	if response.Error != nil {
		return "", errors.WithDetails(
			ErrAPIResponseError,
			"body", response.Error,
			"apiRequest", requestID,
		)
	}

	if len(response.Choices) != 1 {
		return "", errors.WithDetails(
			ErrUnexpectedNumberOfMessages,
			"number", len(response.Choices),
			"apiRequest", requestID,
		)
	}

	if recorder != nil {
		recorder.addUsage(
			requestID,
			o.MaxContextLength,
			o.MaxResponseLength,
			response.Usage.PromptTokens,
			response.Usage.CompletionTokens,
		)

		o.recordMessage(recorder, response.Choices[0].Message)
	}

	if response.Usage.TotalTokens >= o.MaxContextLength {
		return "", errors.WithDetails(
			ErrUnexpectedNumberOfTokens,
			"content", *response.Choices[0].Message.Content,
			"prompt", response.Usage.PromptTokens,
			"response", response.Usage.CompletionTokens,
			"total", response.Usage.TotalTokens,
			"maxTotal", o.MaxContextLength,
			"maxResponse", o.MaxResponseLength,
			"apiRequest", requestID,
		)
	}

	if response.Choices[0].Message.Role != roleAssistant {
		return "", errors.WithDetails(
			ErrUnexpectedRole,
			"role", response.Choices[0].Message.Role,
			"apiRequest", requestID,
		)
	}

	if response.Choices[0].FinishReason != stopReason {
		return "", errors.WithDetails(
			ErrUnexpectedStop,
			"reason", response.Choices[0].FinishReason,
			"apiRequest", requestID,
		)
	}

	if response.Choices[0].Message.Refusal != nil {
		return "", errors.WithDetails(
			ErrRefused,
			"refusal", *response.Choices[0].Message.Refusal,
			"apiRequest", requestID,
		)
	}

	if response.Choices[0].Message.Content == nil {
		return "", errors.WithDetails(
			ErrUnexpectedMessageType,
			"apiRequest", requestID,
		)
	}

	return *response.Choices[0].Message.Content, nil
}

// InitOutputJSONSchema implements WithOutputJSONSchema interface.
func (o *OpenAITextProvider) InitOutputJSONSchema(_ context.Context, schema []byte) errors.E {
	if !o.ForceOutputJSONSchema {
		return nil
	}

	if schema == nil {
		return errors.Errorf(`%w: output JSON Schema is missing`, ErrInvalidJSONSchema)
	}

	o.outputJSONSchema = schema

	s, err := jsonschema.UnmarshalJSON(bytes.NewReader(schema))
	if err != nil {
		return errors.WithStack(err)
	}

	o.outputJSONSchemaName = getString(s, "title")
	o.outputJSONSchemaDescription = getString(s, "description")

	if o.outputJSONSchemaName == "" {
		return errors.Errorf(`%w: JSON Schema is missing "title" field which is used for required JSON Schema "name" for OpenAI API`, ErrInvalidJSONSchema)
	}

	return nil
}

func (o *OpenAITextProvider) recordMessage(recorder *TextProviderRecorder, message openAIMessage) {
	if message.Content != nil {
		recorder.addMessage(message.Role, *message.Content)
	} else if message.Refusal != nil {
		recorder.addMessage(message.Role, *message.Refusal, "isRefusal", "true")
	}
}
