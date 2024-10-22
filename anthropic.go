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
	"strconv"
	"sync"
	"time"

	"github.com/rs/zerolog"
	"gitlab.com/tozd/go/errors"
	"gitlab.com/tozd/go/x"
	"gitlab.com/tozd/identifier"
)

// Max output tokens for current set of models.
const anthropicMaxResponseTokens = 4096

var anthropicRateLimiter = &keyedRateLimiter{ //nolint:gochecknoglobals
	mu:       sync.RWMutex{},
	limiters: map[string]map[string]any{},
}

type anthropicMessage struct {
	Role    string             `json:"role"`
	Content []anthropicContent `json:"content"`
}

type anthropicSystem struct {
	Type         string                 `json:"type"`
	Text         string                 `json:"text"`
	CacheControl *anthropicCacheControl `json:"cache_control,omitempty"`
}

type anthropicRequest struct {
	Model       string             `json:"model"`
	Messages    []anthropicMessage `json:"messages"`
	MaxTokens   int                `json:"max_tokens"`
	System      []anthropicSystem  `json:"system,omitempty"`
	Temperature float64            `json:"temperature"`
	Tools       []anthropicTool    `json:"tools,omitempty"`
}

type anthropicCacheControl struct {
	Type string `json:"type"`
}

type anthropicContent struct {
	Type         string                 `json:"type"`
	Text         *string                `json:"text,omitempty"`
	ID           string                 `json:"id,omitempty"`
	Name         string                 `json:"name,omitempty"`
	Input        json.RawMessage        `json:"input,omitempty"`
	ToolUseID    string                 `json:"tool_use_id,omitempty"`
	IsError      bool                   `json:"is_error,omitempty"`
	Content      *string                `json:"content,omitempty"`
	CacheControl *anthropicCacheControl `json:"cache_control,omitempty"`
}

type anthropicResponse struct {
	ID         string             `json:"id"`
	Type       string             `json:"type"`
	Role       string             `json:"role"`
	Content    []anthropicContent `json:"content"`
	Model      string             `json:"model"`
	StopReason string             `json:"stop_reason"`
	Usage      struct {
		InputTokens              int  `json:"input_tokens"`
		OutputTokens             int  `json:"output_tokens"`
		CacheCreationInputTokens *int `json:"cache_creation_input_tokens,omitempty"`
		CacheReadInputTokens     *int `json:"cache_read_input_tokens,omitempty"`
	} `json:"usage"`
	Error *struct {
		Message string `json:"message"`
		Type    string `json:"type"`
	} `json:"error,omitempty"`
}

func parseAnthropicRateLimitHeaders(resp *http.Response) ( //nolint:nonamedreturns
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
	resetRequests, err = time.Parse(time.RFC3339, resetRequestsStr)
	if err != nil {
		errE = errors.WithDetails(err, "value", resetRequestsStr)
		return //nolint:nakedret
	}
	resetTokens, err = time.Parse(time.RFC3339, resetTokensStr)
	if err != nil {
		errE = errors.WithDetails(err, "value", resetTokensStr)
		return //nolint:nakedret
	}

	return //nolint:nakedret
}

type anthropicTool struct {
	Name            string                 `json:"name"`
	Description     string                 `json:"description,omitempty"`
	InputJSONSchema json.RawMessage        `json:"input_schema"`
	CacheControl    *anthropicCacheControl `json:"cache_control,omitempty"`

	tool TextTooler
}

var _ TextProvider = (*AnthropicTextProvider)(nil)

// AnthropicTextProvider is a [TextProvider] which provides integration with
// text-based [Anthropic] AI models.
//
// [Anthropic]: https://www.anthropic.com/
type AnthropicTextProvider struct {
	// Client is a HTTP client to be used for API calls. If not provided
	// a rate-limited retryable HTTP client is initialized instead.
	Client *http.Client `json:"-"`

	// APIKey is the API key to be used for API calls.
	APIKey string `json:"-"`

	// Model is the name of the model to be used.
	Model string `json:"model"`

	// PromptCaching set to true enables prompt caching.
	PromptCaching bool `json:"promptCaching"`

	// Temperature is how creative should the AI model be.
	// Default is 0 which means not at all.
	Temperature float64 `json:"temperature"`

	rateLimiterKey string
	system         []anthropicSystem
	messages       []anthropicMessage
	tools          []anthropicTool
}

func (a AnthropicTextProvider) MarshalJSON() ([]byte, error) {
	// We define a new type to not recurse into this same MarshalJSON.
	type P AnthropicTextProvider
	t := struct {
		Type string `json:"type"`
		P
	}{
		Type: "anthropic",
		P:    P(a),
	}
	return x.MarshalWithoutEscapeHTML(t)
}

// Init implements [TextProvider] interface.
func (a *AnthropicTextProvider) Init(_ context.Context, messages []ChatMessage) errors.E {
	if a.messages != nil {
		return errors.WithStack(ErrAlreadyInitialized)
	}
	a.messages = []anthropicMessage{}

	for _, message := range messages {
		if message.Role == roleSystem {
			if a.system != nil {
				return errors.WithStack(ErrMultipleSystemMessages)
			}
			a.system = []anthropicSystem{
				{
					Type:         "text",
					Text:         message.Content,
					CacheControl: nil,
				},
			}
		} else {
			a.messages = append(a.messages, anthropicMessage{
				Role: message.Role,
				Content: []anthropicContent{
					{ //nolint:exhaustruct
						Type:         typeText,
						Text:         &message.Content,
						CacheControl: nil,
					},
				},
			})
		}
	}

	if a.PromptCaching {
		// We want to set a cache breakpoint as late as possible. And the order is tools, system, then messages.
		if len(a.messages) > 0 {
			a.messages[len(a.messages)-1].Content[len(a.messages[len(a.messages)-1].Content)-1].CacheControl = &anthropicCacheControl{
				Type: "ephemeral",
			}
		} else if len(a.system) > 0 {
			a.system[len(a.system)-1].CacheControl = &anthropicCacheControl{
				Type: "ephemeral",
			}
		}
	}

	a.rateLimiterKey = fmt.Sprintf("%s-%s", a.APIKey, a.Model)

	if a.Client == nil {
		a.Client = newClient(
			func(req *http.Request) error {
				ctx := req.Context()
				estimatedTokens := getEstimatedTokens(ctx)
				// Rate limit retries.
				return anthropicRateLimiter.Take(ctx, a.rateLimiterKey, map[string]int{
					"rpm": 1,
					"tpd": estimatedTokens,
					"tpm": estimatedTokens,
				})
			},
			parseAnthropicRateLimitHeaders,
			func(limitRequests, limitTokens, remainingRequests, remainingTokens int, resetRequests, resetTokens time.Time) {
				rateLimits := map[string]any{
					"rpm": resettingRateLimit{
						Limit:     limitRequests,
						Remaining: remainingRequests,
						Window:    time.Minute,
						Resets:    resetRequests,
					},
				}
				// Token rate limit headers can be returned for both minute or day, whichever is smaller,
				// so we use heuristics to determine which one it is.
				if limitTokens <= 100_000 { //nolint:mnd
					// Even the free plan has tpd larger than 100,000, so if the limit is less, we know that it is tpm.
					rateLimits["tpm"] = resettingRateLimit{
						Limit:     limitTokens,
						Remaining: remainingTokens,
						Window:    time.Minute,
						Resets:    resetTokens,
					}
				} else if limitTokens/limitRequests >= 2000 { //nolint:mnd
					// If the ratio between token limit and rpm is larger than 2000, we know it is tpd.
					rateLimits["tpd"] = resettingRateLimit{
						Limit:     limitTokens,
						Remaining: remainingTokens,
						Window:    24 * time.Hour, //nolint:mnd
						Resets:    resetTokens,
					}
				} else {
					// Otherwise it is tpm.
					rateLimits["tpm"] = resettingRateLimit{
						Limit:     limitTokens,
						Remaining: remainingTokens,
						Window:    time.Minute,
						Resets:    resetTokens,
					}
				}
				anthropicRateLimiter.Set(a.rateLimiterKey, rateLimits)
			},
		)
	}

	return nil
}

// Chat implements [TextProvider] interface.
func (a *AnthropicTextProvider) Chat(ctx context.Context, message ChatMessage) (string, errors.E) { //nolint:maintidx
	callID := identifier.New().String()

	var callRecorder *TextRecorderCall
	if recorder := GetTextRecorder(ctx); recorder != nil {
		callRecorder = recorder.newCall(callID, a)
		defer recorder.recordCall(callRecorder)
	}

	logger := zerolog.Ctx(ctx).With().Str("fun", callID).Logger()
	ctx = logger.WithContext(ctx)

	messages := slices.Clone(a.messages)
	messages = append(messages, anthropicMessage{
		Role: message.Role,
		Content: []anthropicContent{
			{ //nolint:exhaustruct
				Type: typeText,
				Text: &message.Content,
			},
		},
	})

	if callRecorder != nil {
		for _, system := range a.system {
			callRecorder.addMessage(roleSystem, system.Text, "", "", false)
		}

		for _, message := range messages {
			a.recordMessage(callRecorder, message)
		}

		callRecorder.notify("", nil)
	}

	for {
		request, errE := x.MarshalWithoutEscapeHTML(anthropicRequest{
			Model:       a.Model,
			Messages:    messages,
			MaxTokens:   anthropicMaxResponseTokens,
			System:      a.system,
			Temperature: a.Temperature,
			Tools:       a.tools,
		})
		if errE != nil {
			return "", errE
		}

		estimatedTokens := a.estimatedTokens(messages)

		req, err := http.NewRequestWithContext(
			withEstimatedTokens(ctx, estimatedTokens),
			http.MethodPost,
			"https://api.anthropic.com/v1/messages",
			bytes.NewReader(request),
		)
		if err != nil {
			return "", errors.WithStack(err)
		}
		req.Header.Add("X-Api-Key", a.APIKey)
		req.Header.Add("Anthropic-Version", "2023-06-01")
		req.Header.Add("Content-Type", "application/json")
		if a.PromptCaching {
			req.Header.Add("Anthropic-Beta", "prompt-caching-2024-07-31")
		}
		// Rate limit the initial request.
		errE = anthropicRateLimiter.Take(ctx, a.rateLimiterKey, map[string]int{
			"rpm": 1,
			"tpd": estimatedTokens,
			"tpm": estimatedTokens,
		})
		if errE != nil {
			return "", errE
		}
		start := time.Now()
		resp, err := a.Client.Do(req)
		var apiRequest string
		if resp != nil {
			apiRequest = resp.Header.Get("Request-Id")
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

		var response anthropicResponse
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

		if callRecorder != nil {
			callRecorder.addUsedTokens(
				apiRequest,
				estimatedTokens,
				anthropicMaxResponseTokens,
				response.Usage.InputTokens,
				response.Usage.OutputTokens,
				response.Usage.CacheCreationInputTokens,
				response.Usage.CacheReadInputTokens,
			)
			callRecorder.addUsedTime(
				apiRequest,
				0,
				0,
				apiCallDuration,
			)

			a.recordMessage(callRecorder, anthropicMessage{
				Role:    response.Role,
				Content: response.Content,
			})

			callRecorder.notify("", nil)
		}

		if response.Usage.InputTokens+response.Usage.OutputTokens > estimatedTokens {
			return "", errors.WithDetails(
				ErrUnexpectedNumberOfTokens,
				"prompt", response.Usage.InputTokens,
				"response", response.Usage.OutputTokens,
				"total", response.Usage.InputTokens+response.Usage.OutputTokens,
				"maxTotal", estimatedTokens,
				"maxResponse", anthropicMaxResponseTokens,
				"apiRequest", apiRequest,
			)
		}

		if response.Role != roleAssistant {
			return "", errors.WithDetails(
				ErrUnexpectedRole,
				"role", response.Role,
				"apiRequest", apiRequest,
			)
		}

		if response.StopReason == roleToolUse {
			if len(response.Content) == 0 {
				return "", errors.WithDetails(
					ErrUnexpectedNumberOfMessages,
					"number", len(response.Content),
					"apiRequest", apiRequest,
				)
			}

			// We have already recorded this message above.
			messages = append(messages, anthropicMessage{
				Role:    roleAssistant,
				Content: response.Content,
			})

			// We make space for tool results (one per tool call) so that the Content slice
			// does not grow when appending below and invalidate pointers goroutines keep.
			messages = append(messages, anthropicMessage{
				Role:    roleUser,
				Content: make([]anthropicContent, 0, len(response.Content)),
			})

			if callRecorder != nil {
				// We grow the slice inside call recorder as well.
				callRecorder.prepareForToolMessages(len(response.Content))
			}

			ct, cancel := context.WithCancel(ctx)

			var wg sync.WaitGroup
			for _, content := range response.Content {
				switch content.Type {
				case typeText:
					// We do nothing.
				case roleToolUse:
					messages[len(messages)-1].Content = append(messages[len(messages)-1].Content, anthropicContent{ //nolint:exhaustruct
						Type:      roleToolResult,
						ToolUseID: content.ID,
					})
					result := &messages[len(messages)-1].Content[len(messages[len(messages)-1].Content)-1]

					toolCtx := ct
					var toolMessage *TextRecorderMessage
					if callRecorder != nil {
						toolCtx, toolMessage = callRecorder.startToolMessage(ct, content.ID)
					}
					wg.Add(1)
					go func() {
						defer wg.Done()
						a.callToolWrapper(toolCtx, apiRequest, content, result, callRecorder, toolMessage)
					}()
				default:
					cancel()
					return "", errors.WithDetails(
						ErrUnexpectedMessageType,
						"type", content.Type,
						"apiRequest", apiRequest,
					)
				}
			}

			wg.Wait()
			cancel()

			if len(messages[len(messages)-1].Content) == 0 {
				return "", errors.WithDetails(
					ErrToolCallsWithoutCalls,
					"apiRequest", apiRequest,
				)
			}

			continue
		}

		if response.StopReason != "end_turn" {
			return "", errors.WithDetails(
				ErrUnexpectedStop,
				"reason", response.StopReason,
				"apiRequest", apiRequest,
			)
		}

		// Model sometimes returns no content when the last message to the agent
		// was the tool result and that concluded the conversation.
		if len(response.Content) == 0 {
			return "", nil
		}

		if len(response.Content) != 1 {
			return "", errors.WithDetails(
				ErrUnexpectedNumberOfMessages,
				"number", len(response.Content),
				"apiRequest", apiRequest,
			)
		}
		if response.Content[0].Type != typeText {
			return "", errors.WithDetails(
				ErrUnexpectedMessageType,
				"type", response.Content[0].Type,
				"apiRequest", apiRequest,
			)
		}

		if response.Content[0].Text == nil {
			return "", errors.WithDetails(
				ErrUnexpectedMessageType,
				"apiRequest", apiRequest,
			)
		}

		return *response.Content[0].Text, nil
	}
}

func (a *AnthropicTextProvider) estimatedTokens(messages []anthropicMessage) int {
	// We estimate tokens from training messages (including system message) by
	// dividing number of characters by 4.
	tokens := 0
	for _, message := range messages {
		for _, content := range message.Content {
			if content.Text != nil {
				tokens += len(*content.Text) / 4 //nolint:mnd
			}
			tokens += len(content.Input) / 4 //nolint:mnd
			if content.Content != nil {
				tokens += len(*content.Content) / 4 //nolint:mnd
			}
		}
	}
	for _, system := range a.system {
		tokens += len(system.Text) / 4 //nolint:mnd
	}
	for _, tool := range a.tools {
		tokens += len(tool.Name) / 4            //nolint:mnd
		tokens += len(tool.Description) / 4     //nolint:mnd
		tokens += len(tool.InputJSONSchema) / 4 //nolint:mnd
	}
	// Each output can be up to anthropicMaxResponseTokens so we assume final output
	// is at most that, with input the same.
	return tokens + 2*anthropicMaxResponseTokens
}

// InitTools implements [WithTools] interface.
func (a *AnthropicTextProvider) InitTools(ctx context.Context, tools map[string]TextTooler) errors.E {
	if a.tools != nil {
		return errors.WithStack(ErrAlreadyInitialized)
	}
	a.tools = []anthropicTool{}

	for name, tool := range tools {
		errE := tool.Init(ctx)
		if errE != nil {
			errors.Details(errE)["name"] = name
			return errE
		}

		a.tools = append(a.tools, anthropicTool{
			Name:            name,
			Description:     tool.GetDescription(),
			InputJSONSchema: tool.GetInputJSONSchema(),
			CacheControl:    nil,
			tool:            tool,
		})
	}

	// We want to set a cache breakpoint as late as possible. And the order is tools, system, then messages.
	if a.PromptCaching && len(a.messages) == 0 && len(a.system) == 0 {
		a.tools[len(a.tools)-1].CacheControl = &anthropicCacheControl{
			Type: "ephemeral",
		}
	}

	return nil
}

func (a *AnthropicTextProvider) callToolWrapper(
	ctx context.Context, apiRequest string, toolCall anthropicContent, result *anthropicContent, callRecorder *TextRecorderCall, toolMessage *TextRecorderMessage,
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
			result.IsError = true

			toolMessage.setContent(content, true)
		}
	}()

	defer func() {
		toolMessage.setToolCalls(GetTextRecorder(ctx).Calls())
	}()

	logger := zerolog.Ctx(ctx).With().Str("tool", toolCall.ID).Logger()
	ctx = logger.WithContext(ctx)

	output, duration, errE := a.callTool(ctx, toolCall)
	if errE != nil {
		zerolog.Ctx(ctx).Warn().Err(errE).Str("name", toolCall.Name).Str("apiRequest", apiRequest).
			Str("tool", toolCall.ID).RawJSON("input", toolCall.Input).Msg("tool error")
		content := "Error: " + errE.Error()
		result.Content = &content
		result.IsError = true

		toolMessage.setContent(content, true)
	} else {
		result.Content = &output

		toolMessage.setContent(output, false)
	}

	toolMessage.setToolDuration(duration)
}

func (a *AnthropicTextProvider) callTool(ctx context.Context, toolCall anthropicContent) (string, Duration, errors.E) {
	var tool TextTooler
	for _, t := range a.tools {
		if t.Name == toolCall.Name {
			tool = t.tool
			break
		}
	}
	if tool == nil {
		return "", 0, errors.Errorf("%w: %s", ErrToolNotFound, toolCall.Name)
	}

	start := time.Now()
	output, errE := tool.Call(ctx, toolCall.Input)
	duration := time.Since(start)
	return output, Duration(duration), errE
}

func (a *AnthropicTextProvider) recordMessage(recorder *TextRecorderCall, message anthropicMessage) {
	for _, content := range message.Content {
		switch content.Type {
		case roleToolResult:
			panic(errors.New("recording tool result message should not happen"))
		case typeText:
			if content.Text != nil {
				recorder.addMessage(message.Role, *content.Text, "", "", false)
			}
		case roleToolUse:
			recorder.addMessage(roleToolUse, string(content.Input), content.ID, content.Name, false)
		}
	}
}
