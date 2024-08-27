//nolint:tagliatelle
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

	"github.com/rs/zerolog"
	"github.com/santhosh-tekuri/jsonschema/v6"
	"gitlab.com/tozd/go/errors"
	"gitlab.com/tozd/go/x"
	"gitlab.com/tozd/identifier"
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

// TODO: How can we make parameters optional?
//	     See: https://gitlab.com/tozd/go/fun/-/issues/3

type openAIFunction struct {
	Name            string          `json:"name"`
	Description     string          `json:"description,omitempty"`
	InputJSONSchema json.RawMessage `json:"parameters"`
	Strict          bool            `json:"strict"`
}

type openAITool struct {
	Type     string         `json:"type"`
	Function openAIFunction `json:"function"`
	tool     TextTooler
}

type openAIRequest struct {
	Messages       []openAIMessage       `json:"messages"`
	Model          string                `json:"model"`
	Seed           int                   `json:"seed"`
	Temperature    float64               `json:"temperature"`
	MaxTokens      int                   `json:"max_tokens"`
	ResponseFormat *openAIResponseFormat `json:"response_format,omitempty"`
	Tools          []openAITool          `json:"tools,omitempty"`
}

type openAIToolCall struct {
	ID       string `json:"id"`
	Type     string `json:"type"`
	Function struct {
		Name      string `json:"name"`
		Arguments string `json:"arguments"`
	} `json:"function"`
}

type openAIMessage struct {
	Role       string           `json:"role"`
	Content    *string          `json:"content,omitempty"`
	Refusal    *string          `json:"refusal,omitempty"`
	ToolCalls  []openAIToolCall `json:"tool_calls,omitempty"`
	ToolCallID string           `json:"tool_call_id,omitempty"`
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
	// Client is a HTTP client to be used for API calls. If not provided
	// a rate-limited retryable HTTP client is initialized instead.
	Client *http.Client `json:"-"`

	// APIKey is the API key to be used for API calls.
	APIKey string `json:"-"`

	// Model is the name of the model to be used.
	Model string `json:"model"`

	// MaxContextLength is the maximum total number of tokens allowed to be used
	// with the underlying AI model (i.e., the maximum context window).
	// If not provided, heuristics are used to determine it automatically.
	MaxContextLength int `json:"maxContextLength"`

	// MaxResponseLength is the maximum number of tokens allowed to be used in
	// a response with the underlying AI model. If not provided, heuristics
	// are used to determine it automatically.
	MaxResponseLength int `json:"maxResponseLength"`

	// See: https://github.com/invopop/jsonschema/issues/148

	// ForceOutputJSONSchema when set to true requests the AI model to force
	// the output JSON Schema for its output. When true, consider using
	// meaningful property names and use "description" JSON Schema field to
	// describe to the AI model what each property is. When true, the JSON
	// Schema must have "title" field to name the JSON Schema and consider
	// using "description" field to describe the JSON Schema itself.
	//
	// There are currently limitations on the JSON Schema imposed by OpenAI,
	// so JSON Schema automatically determined from the Output type fails,
	// e.g., only "object" top-level type can be used, all properties must
	// be required, "additionalProperties" must be set to false, top-level
	// $ref is not supported. This further means that only structs can be
	// used as Output types.
	ForceOutputJSONSchema bool `json:"forceOutputJsonSchema"`

	// Seed is used to control the randomness of the AI model. Default is 0.
	Seed int `json:"seed"`

	// Temperature is how creative should the AI model be.
	// Default is 0 which means not at all.
	Temperature float64 `json:"temperature"`

	messages                    []openAIMessage
	tools                       []openAITool
	outputJSONSchema            json.RawMessage
	outputJSONSchemaName        string
	outputJSONSchemaDescription string
}

func (o OpenAITextProvider) MarshalJSON() ([]byte, error) {
	// We define a new type to not recurse into this same MarshalJSON.
	type P OpenAITextProvider
	t := struct {
		Type string `json:"type"`
		P
	}{
		Type: "openai",
		P:    P(o),
	}
	return x.MarshalWithoutEscapeHTML(t)
}

// Init implements [TextProvider] interface.
func (o *OpenAITextProvider) Init(_ context.Context, messages []ChatMessage) errors.E {
	if o.messages != nil {
		return errors.WithStack(ErrAlreadyInitialized)
	}
	o.messages = []openAIMessage{}

	for _, message := range messages {
		message := message
		o.messages = append(o.messages, openAIMessage{
			Role:       message.Role,
			Content:    &message.Content,
			Refusal:    nil,
			ToolCalls:  nil,
			ToolCallID: "",
		})
	}

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

// Chat implements [TextProvider] interface.
func (o *OpenAITextProvider) Chat(ctx context.Context, message ChatMessage) (string, errors.E) { //nolint:maintidx
	callID := identifier.New().String()

	var callRecorder *TextRecorderCall
	if recorder := GetTextRecorder(ctx); recorder != nil {
		callRecorder = recorder.newCall(callID, o)
		defer recorder.recordCall(callRecorder)
	}

	logger := zerolog.Ctx(ctx).With().Str("fun", callID).Logger()
	ctx = logger.WithContext(ctx)

	messages := slices.Clone(o.messages)
	messages = append(messages, openAIMessage{
		Role:       message.Role,
		Content:    &message.Content,
		Refusal:    nil,
		ToolCalls:  nil,
		ToolCallID: "",
	})

	if callRecorder != nil {
		for _, message := range messages {
			o.recordMessage(callRecorder, message)
		}

		callRecorder.notify("", nil)
	}

	for {
		oReq := openAIRequest{
			Messages:       messages,
			Model:          o.Model,
			Seed:           o.Seed,
			Temperature:    o.Temperature,
			MaxTokens:      o.MaxResponseLength, // TODO: Can we provide a better estimate?
			ResponseFormat: nil,
			Tools:          o.tools,
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
		start := time.Now()
		resp, err := o.Client.Do(req)
		var apiRequest string
		if resp != nil {
			apiRequest = resp.Header.Get("X-Request-Id")
		}
		if err != nil {
			errE = errors.Prefix(err, ErrAPIRequestFailed)
			if apiRequest != "" {
				errors.Details(errE)["apiRequest"] = apiRequest
			}
			return "", errE
		}
		defer resp.Body.Close()
		defer io.Copy(io.Discard, resp.Body) //nolint:errcheck

		if apiRequest == "" {
			return "", errors.WithStack(ErrMissingRequestID)
		}

		var response openAIResponse
		errE = x.DecodeJSON(resp.Body, &response)
		if errE != nil {
			errors.Details(errE)["apiRequest"] = apiRequest
			return "", errE
		}

		apiCallDuration := time.Since(start)

		if response.Error != nil {
			return "", errors.WithDetails(
				ErrAPIResponseError,
				"body", response.Error,
				"apiRequest", apiRequest,
			)
		}

		if len(response.Choices) != 1 {
			return "", errors.WithDetails(
				ErrUnexpectedNumberOfMessages,
				"number", len(response.Choices),
				"apiRequest", apiRequest,
			)
		}

		if callRecorder != nil {
			callRecorder.addUsedTokens(
				apiRequest,
				o.MaxContextLength,
				o.MaxResponseLength,
				response.Usage.PromptTokens,
				response.Usage.CompletionTokens,
				nil,
				nil,
			)
			callRecorder.addUsedTime(
				apiRequest,
				0,
				0,
				apiCallDuration,
			)

			o.recordMessage(callRecorder, response.Choices[0].Message)

			callRecorder.notify("", nil)
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
				"apiRequest", apiRequest,
			)
		}

		if response.Choices[0].Message.Role != roleAssistant {
			return "", errors.WithDetails(
				ErrUnexpectedRole,
				"role", response.Choices[0].Message.Role,
				"apiRequest", apiRequest,
			)
		}

		if response.Choices[0].FinishReason == "tool_calls" {
			if len(response.Choices[0].Message.ToolCalls) == 0 {
				return "", errors.WithDetails(
					ErrUnexpectedNumberOfMessages,
					"number", len(response.Choices[0].Message.ToolCalls),
					"apiRequest", apiRequest,
				)
			}

			// We have already recorded this message above.
			messages = append(messages, response.Choices[0].Message)

			// We make space for tool results (one per tool call) so that the messages slice
			// does not grow when appending below and invalidate pointers goroutines keep.
			messages = slices.Grow(messages, len(response.Choices[0].Message.ToolCalls))

			if callRecorder != nil {
				// We grow the slice inside call recorder as well.
				callRecorder.prepareForToolMessages(len(response.Choices[0].Message.ToolCalls))
			}

			var wg sync.WaitGroup
			for _, toolCall := range response.Choices[0].Message.ToolCalls {
				toolCall := toolCall

				messages = append(messages, openAIMessage{
					Role:       roleTool,
					Content:    nil,
					Refusal:    nil,
					ToolCalls:  nil,
					ToolCallID: toolCall.ID,
				})
				result := &messages[len(messages)-1]

				toolCtx := ctx
				var toolMessage *TextRecorderMessage
				if callRecorder != nil {
					toolCtx, toolMessage = callRecorder.startToolMessage(ctx, toolCall.ID)
				}
				wg.Add(1)
				go func() {
					defer wg.Done()
					o.callToolWrapper(toolCtx, apiRequest, toolCall, result, callRecorder, toolMessage)
				}()
			}

			wg.Wait()

			continue
		}

		if response.Choices[0].FinishReason != stopReason {
			return "", errors.WithDetails(
				ErrUnexpectedStop,
				"reason", response.Choices[0].FinishReason,
				"apiRequest", apiRequest,
			)
		}

		if response.Choices[0].Message.Refusal != nil {
			return "", errors.WithDetails(
				ErrRefused,
				"refusal", *response.Choices[0].Message.Refusal,
				"apiRequest", apiRequest,
			)
		}

		if response.Choices[0].Message.Content == nil {
			return "", errors.WithDetails(
				ErrUnexpectedMessageType,
				"apiRequest", apiRequest,
			)
		}

		return *response.Choices[0].Message.Content, nil
	}
}

// InitOutputJSONSchema implements [WithOutputJSONSchema] interface.
func (o *OpenAITextProvider) InitOutputJSONSchema(_ context.Context, schema []byte) errors.E {
	if !o.ForceOutputJSONSchema {
		return nil
	}

	if schema == nil {
		return errors.Errorf(`%w: output JSON Schema is missing`, ErrInvalidJSONSchema)
	}

	if o.outputJSONSchema != nil {
		return errors.WithStack(ErrAlreadyInitialized)
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

// InitTools implements [WithTools] interface.
func (o *OpenAITextProvider) InitTools(ctx context.Context, tools map[string]TextTooler) errors.E {
	if o.tools != nil {
		return errors.WithStack(ErrAlreadyInitialized)
	}
	o.tools = []openAITool{}

	for name, tool := range tools {
		errE := tool.Init(ctx)
		if errE != nil {
			errors.Details(errE)["name"] = name
			return errE
		}

		o.tools = append(o.tools, openAITool{
			Type: "function",
			Function: openAIFunction{
				Name:            name,
				Description:     tool.GetDescription(),
				InputJSONSchema: tool.GetInputJSONSchema(),
				Strict:          true,
			},
			tool: tool,
		})
	}

	return nil
}

func (o *OpenAITextProvider) callToolWrapper(ctx context.Context, apiRequest string, toolCall openAIToolCall, result *openAIMessage, callRecorder *TextRecorderCall, toolMessage *TextRecorderMessage) {
	defer func() {
		if callRecorder != nil {
			callRecorder.notify("", nil)
		}
	}()

	defer func() {
		if err := recover(); err != nil {
			content := fmt.Sprintf("Error: %s", err)
			result.Content = &content

			if toolMessage != nil {
				toolMessage.Content = &content
				toolMessage.IsError = true
			}
		}
	}()

	defer func() {
		if toolMessage != nil {
			toolMessage.ToolCalls = GetTextRecorder(ctx).Calls()
		}
	}()

	logger := zerolog.Ctx(ctx).With().Str("tool", toolCall.ID).Logger()
	ctx = logger.WithContext(ctx)

	output, duration, errE := o.callTool(ctx, toolCall)
	if errE != nil {
		zerolog.Ctx(ctx).Warn().Err(errE).Str("name", toolCall.Function.Name).Str("apiRequest", apiRequest).
			Str("tool", toolCall.ID).RawJSON("input", json.RawMessage(toolCall.Function.Arguments)).Msg("tool error")
		content := fmt.Sprintf("Error: %s", errE.Error())
		result.Content = &content

		if toolMessage != nil {
			toolMessage.Content = &content
			toolMessage.IsError = true
		}
	} else {
		result.Content = &output

		if toolMessage != nil {
			toolMessage.Content = &output
		}
	}

	if toolMessage != nil {
		toolMessage.ToolDuration = duration
		toolMessage.start = time.Time{}
	}
}

func (o *OpenAITextProvider) callTool(ctx context.Context, toolCall openAIToolCall) (string, Duration, errors.E) {
	var tool TextTooler
	for _, t := range o.tools {
		if t.Function.Name == toolCall.Function.Name {
			tool = t.tool
			break
		}
	}
	if tool == nil {
		return "", 0, errors.Errorf("%w: %s", ErrToolNotFound, toolCall.Function.Name)
	}

	start := time.Now()
	output, errE := tool.Call(ctx, json.RawMessage(toolCall.Function.Arguments))
	duration := time.Since(start)
	return output, Duration(duration), errE
}

func (o *OpenAITextProvider) recordMessage(recorder *TextRecorderCall, message openAIMessage) {
	if message.Role == roleTool {
		panic(errors.New("recording tool result message should not happen"))
	} else {
		if message.Content != nil {
			recorder.addMessage(message.Role, *message.Content, "", "", 0, nil, false, false)
		} else if message.Refusal != nil {
			recorder.addMessage(message.Role, *message.Refusal, "", "", 0, nil, false, true)
		}
	}
	for _, tool := range message.ToolCalls {
		recorder.addMessage(roleToolUse, tool.Function.Arguments, tool.ID, tool.Function.Name, 0, nil, false, false)
	}
}
