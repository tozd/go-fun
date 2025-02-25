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
	Messages            []groqMessage `json:"messages"`
	Model               string        `json:"model"`
	Seed                int           `json:"seed"`
	Temperature         float64       `json:"temperature"`
	MaxCompletionTokens int           `json:"max_completion_tokens"`
	Tools               []groqTool    `json:"tools,omitempty"`
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
		Message          string  `json:"message"`
		Type             string  `json:"type"`
		Code             *string `json:"code,omitempty"`
		FailedGeneration *string `json:"failed_generation,omitempty"`
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

	// RequestsPerMinuteLimit is the RPM limit for the used model. Default is 30.
	RequestsPerMinuteLimit int `json:"requestsPerMinuteLimit"`

	// MaxContextLength is the maximum total number of tokens allowed to be used
	// with the underlying AI model (i.e., the maximum context window).
	// If not provided, heuristics are used to determine it automatically.
	MaxContextLength int `json:"maxContextLength"`

	// MaxResponseLength is the maximum number of tokens allowed to be used in
	// a response with the underlying AI model. If not provided, heuristics
	// are used to determine it automatically.
	MaxResponseLength int `json:"maxResponseLength"`

	// MaxExchanges is the maximum number of exchanges with the AI model per chat
	// to obtain the final response. Default is 10.
	MaxExchanges int `json:"maxExchanges"`

	// Seed is used to control the randomness of the AI model. Default is 0.
	Seed int `json:"seed"`

	// Temperature is how creative should the AI model be.
	// Default is 0 which means not at all.
	Temperature float64 `json:"temperature"`

	rateLimiterKey string
	messages       []groqMessage
	tools          []groqTool
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
		g.messages = append(g.messages, groqMessage{
			Role:       message.Role,
			Content:    &message.Content,
			ToolCalls:  nil,
			ToolCallID: "",
		})
	}

	g.rateLimiterKey = fmt.Sprintf("%s-%s", g.APIKey, g.Model)

	if g.Client == nil {
		g.Client = newClient(
			func(req *http.Request) error {
				if req.URL.Path == "/openai/v1/chat/completions" {
					ctx := req.Context() //nolint:govet
					estimatedInputTokens, _ := getEstimatedTokens(ctx)
					// Rate limit retries.
					return groqRateLimiter.Take(ctx, g.rateLimiterKey, map[string]int{
						"rpm": 1,
						"rpd": 1,
						"tpm": estimatedInputTokens,
					})
				}
				return nil
			},
			parseRateLimitHeaders,
			func(limitRequests, limitTokens, remainingRequests, remainingTokens int, resetRequests, resetTokens time.Time) {
				groqRateLimiter.Set(g.rateLimiterKey, map[string]any{
					// TODO: Correctly implement this rate limit.
					//       Currently there are not headers for this limit, so we are simulating it with a token bucket rate limit.
					"rpm": tokenBucketRateLimit{
						Limit: rate.Limit(float64(g.RequestsPerMinuteLimit) / time.Minute.Seconds()), // Requests per minute.
						Burst: g.RequestsPerMinuteLimit,
					},
					"rpd": resettingRateLimit{
						Limit:     limitRequests,
						Remaining: remainingRequests,
						Window:    24 * time.Hour, //nolint:mnd
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

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, "https://api.groq.com/openai/v1/models/"+g.Model, nil)
	if err != nil {
		return errors.WithStack(err)
	}
	req.Header.Add("Authorization", "Bearer "+g.APIKey)
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

	if g.RequestsPerMinuteLimit == 0 {
		// RPM limit on the free tier is the default.
		g.RequestsPerMinuteLimit = 30
	}

	if g.MaxContextLength == 0 {
		g.MaxContextLength = g.maxContextLength(model)
	}

	if g.MaxResponseLength == 0 {
		g.MaxResponseLength = g.maxResponseTokens(model)
	}

	if g.MaxExchanges == 0 {
		g.MaxExchanges = 10
	}

	return nil
}

// Chat implements [TextProvider] interface.
func (g *GroqTextProvider) Chat(ctx context.Context, message ChatMessage) (string, errors.E) { //nolint:maintidx
	callID := identifier.New().String()

	var callRecorder *TextRecorderCall
	if recorder := GetTextRecorder(ctx); recorder != nil {
		callRecorder = recorder.newCall(callID, g)
		defer recorder.recordCall(callRecorder)
	}

	logger := zerolog.Ctx(ctx).With().Str("fun", callID).Logger()
	ctx = logger.WithContext(ctx)

	messages := slices.Clone(g.messages)
	messages = append(messages, groqMessage{
		Role:       message.Role,
		Content:    &message.Content,
		ToolCalls:  nil,
		ToolCallID: "",
	})

	if callRecorder != nil {
		for _, message := range messages {
			g.recordMessage(callRecorder, message)
		}

		callRecorder.notify("", nil)
	}

	for range g.MaxExchanges {
		request, errE := x.MarshalWithoutEscapeHTML(groqRequest{
			Messages:            messages,
			Model:               g.Model,
			Seed:                g.Seed,
			Temperature:         g.Temperature,
			MaxCompletionTokens: g.MaxResponseLength,
			Tools:               g.tools,
		})
		if errE != nil {
			return "", errE
		}

		estimatedInputTokens, estimatedOutputTokens := g.estimatedTokens(messages)

		req, err := http.NewRequestWithContext(
			withEstimatedTokens(ctx, estimatedInputTokens, estimatedOutputTokens),
			http.MethodPost,
			"https://api.groq.com/openai/v1/chat/completions",
			bytes.NewReader(request),
		)
		if err != nil {
			return "", errors.WithStack(err)
		}
		req.Header.Add("Authorization", "Bearer "+g.APIKey)
		req.Header.Add("Content-Type", "application/json")
		// Rate limit the initial request.
		errE = groqRateLimiter.Take(ctx, g.rateLimiterKey, map[string]int{
			"rpm": 1,
			"rpd": 1,
			"tpm": estimatedInputTokens,
		})
		if errE != nil {
			return "", errE
		}
		start := time.Now()
		resp, err := g.Client.Do(req)
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

		var response groqResponse
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
				ErrUnexpectedMessage,
				"number", len(response.Choices),
				"apiRequest", apiRequest,
			)
		}

		if callRecorder != nil {
			callRecorder.addUsedTokens(
				apiRequest,
				g.MaxContextLength,
				g.MaxResponseLength,
				response.Usage.PromptTokens,
				response.Usage.CompletionTokens,
				nil,
				nil,
				nil,
			)
			callRecorder.addUsedTime(
				apiRequest,
				time.Duration(response.Usage.PromptTime*float64(time.Second)),
				time.Duration(response.Usage.CompletionTime*float64(time.Second)),
				apiCallDuration,
			)

			g.recordMessage(callRecorder, response.Choices[0].Message)

			callRecorder.notify("", nil)
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
					ErrUnexpectedMessage,
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
				messages = append(messages, groqMessage{
					Role:       roleTool,
					Content:    nil,
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
					g.callToolWrapper(toolCtx, apiRequest, toolCall, result, callRecorder, toolMessage)
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
		if response.Choices[0].Message.Content == nil {
			return "", errors.WithDetails(
				ErrUnexpectedMessageType,
				"apiRequest", apiRequest,
			)
		}

		return *response.Choices[0].Message.Content, nil
	}

	return "", errors.WithDetails(
		ErrMaxExchangesReached,
		"maxExchanges", g.MaxExchanges,
	)
}

func (g *GroqTextProvider) estimatedTokens(messages []groqMessage) (int, int) {
	// We estimate inputTokens from training messages (including system message) by
	// dividing number of characters by 4.
	inputTokens := 0
	for _, message := range messages {
		if message.Content != nil {
			inputTokens += len(*message.Content) / 4 //nolint:mnd
			for _, tool := range message.ToolCalls {
				inputTokens += len(tool.Function.Name) / 4      //nolint:mnd
				inputTokens += len(tool.Function.Arguments) / 4 //nolint:mnd
			}
		}
	}
	for _, tool := range g.tools {
		inputTokens += len(tool.Function.Name) / 4            //nolint:mnd
		inputTokens += len(tool.Function.Description) / 4     //nolint:mnd
		inputTokens += len(tool.Function.InputJSONSchema) / 4 //nolint:mnd
	}
	return inputTokens, 0
}

func (g *GroqTextProvider) maxContextLength(model groqModel) int {
	return model.ContextWindow
}

func (g *GroqTextProvider) maxResponseTokens(model groqModel) int {
	// See: https://console.groq.com/docs/models
	// TODO: Why are real limits smaller than what is in the documentation (8000 and not 8192, for example)? If you try 8192 you get an API error.
	if strings.Contains(model.ID, "llama-3.3") {
		return 32_000 //nolint:mnd
	}
	if strings.Contains(model.ID, "llama-3.2") {
		return 8_000 //nolint:mnd
	}
	if strings.Contains(model.ID, "llama-3.1") {
		return 8_000 //nolint:mnd
	}
	if strings.Contains(model.ID, "deepseek-r1-distill") {
		return 16_000 //nolint:mnd
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

func (g *GroqTextProvider) callToolWrapper( //nolint:dupl
	ctx context.Context, apiRequest string, toolCall groqToolCall, result *groqMessage, callRecorder *TextRecorderCall, toolMessage *TextRecorderMessage,
) {
	if callRecorder != nil {
		defer func() {
			callRecorder.notify("", nil)
		}()
	}

	defer func() {
		if err := recover(); err != nil {
			content := fmt.Sprintf("Error: %s", err)
			result.Content = &content

			toolMessage.setContent(content, true)
		}
	}()

	defer func() {
		toolMessage.setToolCalls(GetTextRecorder(ctx).Calls())
	}()

	logger := zerolog.Ctx(ctx).With().Str("tool", toolCall.ID).Logger()
	ctx = logger.WithContext(ctx)

	output, duration, errE := g.callTool(ctx, toolCall)
	if errE != nil {
		zerolog.Ctx(ctx).Warn().Err(errE).Str("name", toolCall.Function.Name).Str("apiRequest", apiRequest).
			Str("tool", toolCall.ID).RawJSON("input", json.RawMessage(toolCall.Function.Arguments)).Msg("tool error")
		content := "Error: " + errE.Error()
		result.Content = &content

		toolMessage.setContent(content, true)
	} else {
		result.Content = &output

		toolMessage.setContent(output, false)
	}

	toolMessage.setToolDuration(duration)
}

func (g *GroqTextProvider) callTool(ctx context.Context, toolCall groqToolCall) (string, Duration, errors.E) {
	var tool TextTooler
	for _, t := range g.tools {
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

func (g *GroqTextProvider) recordMessage(recorder *TextRecorderCall, message groqMessage) {
	if message.Role == roleTool {
		panic(errors.New("recording tool result message should not happen"))
	} else if message.Content != nil {
		recorder.addMessage(message.Role, *message.Content, "", "", false)
	}
	for _, tool := range message.ToolCalls {
		recorder.addMessage(roleToolUse, tool.Function.Arguments, tool.ID, tool.Function.Name, false)
	}
}
