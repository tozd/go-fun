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
	"strings"
	"sync"
	"time"

	"github.com/rs/zerolog"
	"gitlab.com/tozd/go/errors"
	"gitlab.com/tozd/go/x"
	"gitlab.com/tozd/identifier"
	"golang.org/x/time/rate"
)

var groqRateLimiter = keyedRateLimiter{ //nolint:gochecknoglobals
	mu:       sync.RWMutex{},
	limiters: map[string]map[string]any{},
}

type groqModel struct {
	ID            string `json:"id"`
	Object        string `json:"object"`
	Created       int64  `json:"created"`
	OwnedBy       string `json:"owned_by"`
	Active        bool   `json:"active"`
	ContextWindow int    `json:"context_window"`
	Error         *struct {
		Message string  `json:"message"`
		Type    string  `json:"type"`
		Code    *string `json:"code,omitempty"`
	} `json:"error,omitempty"`
}

// TODO: How can we make parameters optional?
//	     See: https://gitlab.com/tozd/go/fun/-/issues/3

type groqFunction struct {
	Name            string          `json:"name"`
	Description     string          `json:"description,omitempty"`
	InputJSONSchema json.RawMessage `json:"parameters"`
}

type groqTool struct {
	Type     string       `json:"type"`
	Function groqFunction `json:"function"`

	tool TextTooler
}

type groqRequest struct {
	Messages    []groqMessage `json:"messages"`
	Model       string        `json:"model"`
	Seed        int           `json:"seed"`
	Temperature float64       `json:"temperature"`
	MaxTokens   int           `json:"max_tokens"`
	Tools       []groqTool    `json:"tools,omitempty"`
}

type groqToolCall struct {
	ID       string `json:"id"`
	Type     string `json:"type"`
	Function struct {
		Name      string `json:"name"`
		Arguments string `json:"arguments"`
	} `json:"function"`
}

type groqMessage struct {
	Role       string         `json:"role"`
	Content    *string        `json:"content,omitempty"`
	ToolCalls  []groqToolCall `json:"tool_calls,omitempty"`
	ToolCallID string         `json:"tool_call_id,omitempty"`
}
type groqResponse struct {
	ID                string `json:"id"`
	Object            string `json:"object"`
	Created           int64  `json:"created"`
	Model             string `json:"model"`
	SystemFingerprint string `json:"system_fingerprint"`
	Choices           []struct {
		Index        int         `json:"index"`
		Message      groqMessage `json:"message"`
		FinishReason string      `json:"finish_reason"`
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
		Message string  `json:"message"`
		Type    string  `json:"type"`
		Code    *string `json:"code,omitempty"`
	} `json:"error,omitempty"`
}

var _ TextProvider = (*GroqTextProvider)(nil)

// GroqTextProvider is a [TextProvider] which provides integration with
// text-based [Groq] AI models.
//
// [Groq]: https://groq.com/
type GroqTextProvider struct {
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

	// Seed is used to control the randomness of the AI model. Default is 0.
	Seed int `json:"seed"`

	// Temperature is how creative should the AI model be.
	// Default is 0 which means not at all.
	Temperature float64 `json:"temperature"`

	messages []groqMessage
	tools    []groqTool
}

func (g GroqTextProvider) MarshalJSON() ([]byte, error) {
	// We define a new type to not recurse into this same MarshalJSON.
	type P GroqTextProvider
	t := struct {
		Type string `json:"type"`
		P
	}{
		Type: "groq",
		P:    P(g),
	}
	return x.MarshalWithoutEscapeHTML(t)
}

// Init implements [TextProvider] interface.
func (g *GroqTextProvider) Init(ctx context.Context, messages []ChatMessage) errors.E {
	if g.messages != nil {
		return errors.WithStack(ErrAlreadyInitialized)
	}
	g.messages = []groqMessage{}

	for _, message := range messages {
		message := message
		g.messages = append(g.messages, groqMessage{
			Role:       message.Role,
			Content:    &message.Content,
			ToolCalls:  nil,
			ToolCallID: "",
		})
	}

	if g.Client == nil {
		g.Client = newClient(
			func(req *http.Request) error {
				if req.URL.Path == "/openai/v1/chat/completions" {
					// Rate limit retries.
					return groqRateLimiter.Take(req.Context(), g.APIKey, map[string]int{
						"rpm": 1,
						"rpd": 1,
						"tpm": g.MaxContextLength, // TODO: Can we provide a better estimate?
					})
				}
				return nil
			},
			parseRateLimitHeaders,
			func(limitRequests, limitTokens, remainingRequests, remainingTokens int, resetRequests, resetTokens time.Time) {
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
			},
		)
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, fmt.Sprintf("https://api.groq.com/openai/v1/models/%s", g.Model), nil)
	if err != nil {
		return errors.WithStack(err)
	}
	req.Header.Add("Authorization", fmt.Sprintf("Bearer %s", g.APIKey))
	req.Header.Add("Content-Type", "application/json")
	// This endpoint does not have rate limiting.
	resp, err := g.Client.Do(req)
	var apiRequest string
	if resp != nil {
		apiRequest = resp.Header.Get("X-Request-Id")
	}
	if err != nil {
		errE := errors.Prefix(err, ErrAPIRequestFailed)
		if apiRequest != "" {
			errors.Details(errE)["apiRequest"] = apiRequest
		}
		return errE
	}
	defer resp.Body.Close()
	defer io.Copy(io.Discard, resp.Body) //nolint:errcheck

	if apiRequest == "" {
		return errors.WithStack(ErrMissingRequestID)
	}

	var model groqModel
	errE := x.DecodeJSON(resp.Body, &model)
	if errE != nil {
		errors.Details(errE)["apiRequest"] = apiRequest
		return errE
	}

	if model.Error != nil {
		return errors.WithDetails(
			ErrAPIResponseError,
			"body", model.Error,
			"apiRequest", apiRequest,
		)
	}

	if !model.Active {
		return errors.WithDetails(
			ErrModelNotActive,
			"apiRequest", apiRequest,
		)
	}

	if g.MaxContextLength == 0 {
		g.MaxContextLength = g.maxContextLength(model)
	}
	if g.MaxContextLength > g.maxContextLength(model) {
		return errors.WithDetails(
			ErrMaxContextLengthOverModel,
			"maxTotal", g.MaxContextLength,
			"model", g.maxContextLength(model),
			"apiRequest", apiRequest,
		)
	}

	if g.MaxResponseLength == 0 {
		g.MaxResponseLength = g.maxResponseTokens(model)
	}
	if g.MaxResponseLength > g.MaxContextLength {
		return errors.WithDetails(
			ErrMaxResponseLengthOverContext,
			"maxTotal", g.MaxContextLength,
			"maxResponse", g.MaxResponseLength,
			"apiRequest", apiRequest,
		)
	}

	return nil
}

// Chat implements [TextProvider] interface.
func (g *GroqTextProvider) Chat(ctx context.Context, message ChatMessage) (string, errors.E) {
	callID := identifier.New().String()
	logger := zerolog.Ctx(ctx).With().Str("fun", callID).Logger()
	ctx = logger.WithContext(ctx)

	var callRecorder *TextRecorderCall
	if recorder := GetTextRecorder(ctx); recorder != nil {
		callRecorder = &TextRecorderCall{
			ID:         callID,
			Provider:   g,
			Messages:   nil,
			UsedTokens: nil,
			UsedTime:   nil,
		}
		defer recorder.recordCall(callRecorder)
	}

	messages := slices.Clone(g.messages)
	messages = append(messages, groqMessage{
		Role:       message.Role,
		Content:    &message.Content,
		ToolCalls:  nil,
		ToolCallID: "",
	})

	if callRecorder != nil {
		for _, message := range messages {
			g.recordMessage(callRecorder, message, nil, false)
		}
	}

	for {
		request, errE := x.MarshalWithoutEscapeHTML(groqRequest{
			Messages:    messages,
			Model:       g.Model,
			Seed:        g.Seed,
			Temperature: g.Temperature,
			MaxTokens:   g.MaxResponseLength, // TODO: Can we provide a better estimate?
			Tools:       g.tools,
		})
		if errE != nil {
			return "", errE
		}

		req, err := http.NewRequestWithContext(ctx, http.MethodPost, "https://api.groq.com/openai/v1/chat/completions", bytes.NewReader(request))
		if err != nil {
			return "", errors.WithStack(err)
		}
		req.Header.Add("Authorization", fmt.Sprintf("Bearer %s", g.APIKey))
		req.Header.Add("Content-Type", "application/json")
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

		var response groqResponse
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

		if callRecorder != nil {
			callRecorder.addUsedTokens(
				requestID,
				g.MaxContextLength,
				g.MaxResponseLength,
				response.Usage.PromptTokens,
				response.Usage.CompletionTokens,
				nil,
				nil,
			)
			callRecorder.addUsedTime(
				requestID,
				time.Duration(response.Usage.PromptTime*float64(time.Second)),
				time.Duration(response.Usage.CompletionTime*float64(time.Second)),
			)

			g.recordMessage(callRecorder, response.Choices[0].Message, nil, false)
		}

		if response.Usage.TotalTokens >= g.MaxContextLength {
			return "", errors.WithDetails(
				ErrUnexpectedNumberOfTokens,
				"content", response.Choices[0].Message.Content,
				"prompt", response.Usage.PromptTokens,
				"response", response.Usage.CompletionTokens,
				"total", response.Usage.TotalTokens,
				"maxTotal", g.MaxContextLength,
				"maxResponse", g.MaxResponseLength,
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

		if response.Choices[0].FinishReason == "tool_calls" {
			if len(response.Choices[0].Message.ToolCalls) == 0 {
				return "", errors.WithDetails(
					ErrUnexpectedNumberOfMessages,
					"number", len(response.Choices[0].Message.ToolCalls),
					"apiRequest", requestID,
				)
			}

			// We have already recorded this message above.
			messages = append(messages, response.Choices[0].Message)

			for _, toolCall := range response.Choices[0].Message.ToolCalls {
				isError := false
				output, calls, errE := g.callTool(ctx, toolCall)
				if errE != nil {
					zerolog.Ctx(ctx).Warn().Err(errE).Str("name", toolCall.Function.Name).Str("apiRequest", requestID).
						Str("tool", toolCall.ID).RawJSON("input", json.RawMessage(toolCall.Function.Arguments)).Msg("tool error")
					content := fmt.Sprintf("Error: %s", errE.Error())
					messages = append(messages, groqMessage{
						Role:       roleTool,
						Content:    &content,
						ToolCalls:  nil,
						ToolCallID: toolCall.ID,
					})
					isError = true
				} else {
					messages = append(messages, groqMessage{
						Role:       roleTool,
						Content:    &output,
						ToolCalls:  nil,
						ToolCallID: toolCall.ID,
					})
				}

				if callRecorder != nil {
					g.recordMessage(callRecorder, messages[len(messages)-1], calls, isError)
				}
			}

			continue
		}

		if response.Choices[0].FinishReason != stopReason {
			return "", errors.WithDetails(
				ErrUnexpectedStop,
				"reason", response.Choices[0].FinishReason,
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
}

func (g *GroqTextProvider) maxContextLength(model groqModel) int {
	// llama3-70b-8192 has only 6000 tokens per minute limit so a larger context length cannot be used.
	if model.ID == "llama3-70b-8192" {
		return 6000 //nolint:gomnd
	}
	return model.ContextWindow
}

func (g *GroqTextProvider) maxResponseTokens(model groqModel) int {
	// "During preview launch, we are limiting all 3.1 models to max_tokens of 8k and 405b to 16k input tokens."
	// See: https://console.groq.com/docs/models
	if strings.Contains(model.ID, "llama-3.1-405b") {
		return 16000 //nolint:gomnd
	} else if strings.Contains(model.ID, "llama-3.1") {
		return 8000 //nolint:gomnd
	}
	return g.maxContextLength(model)
}

// InitTools implements [WithTools] interface.
func (g *GroqTextProvider) InitTools(ctx context.Context, tools map[string]TextTooler) errors.E {
	if g.tools != nil {
		return errors.WithStack(ErrAlreadyInitialized)
	}
	g.tools = []groqTool{}

	for name, tool := range tools {
		errE := tool.Init(ctx)
		if errE != nil {
			errors.Details(errE)["name"] = name
			return errE
		}

		g.tools = append(g.tools, groqTool{
			Type: "function",
			Function: groqFunction{
				Name:            name,
				Description:     tool.GetDescription(),
				InputJSONSchema: tool.GetInputJSONSchema(),
			},
			tool: tool,
		})
	}

	return nil
}

func (g *GroqTextProvider) callTool(ctx context.Context, toolCall groqToolCall) (string, []TextRecorderCall, errors.E) {
	var tool TextTooler
	for _, t := range g.tools {
		if t.Function.Name == toolCall.Function.Name {
			tool = t.tool
			break
		}
	}
	if tool == nil {
		return "", nil, errors.Errorf("%w: %s", ErrToolNotFound, toolCall.Function.Name)
	}

	logger := zerolog.Ctx(ctx).With().Str("tool", toolCall.ID).Logger()
	ctx = logger.WithContext(ctx)

	if recorder := GetTextRecorder(ctx); recorder != nil {
		// If recorder is present in the current content, we create a new context with
		// a new recorder so that we can record a tool implemented with Text.
		ctx = WithTextRecorder(ctx)
	}

	output, errE := tool.Call(ctx, json.RawMessage(toolCall.Function.Arguments))
	// If there is no recorder, Calls returns nil.
	// Calls returns nil as well if the tool was not implemented with Text.
	return output, GetTextRecorder(ctx).Calls(), errE
}

func (g *GroqTextProvider) recordMessage(recorder *TextRecorderCall, message groqMessage, calls []TextRecorderCall, isError bool) {
	if message.Role == roleTool {
		if message.Content != nil {
			recorder.addMessage(roleToolResult, *message.Content, message.ToolCallID, "", isError, false, calls)
		}
	} else {
		if message.Content != nil {
			recorder.addMessage(message.Role, *message.Content, "", "", false, false, nil)
		}
	}
	for _, tool := range message.ToolCalls {
		recorder.addMessage(roleToolUse, tool.Function.Arguments, tool.ID, tool.Function.Name, false, false, nil)
	}
}
