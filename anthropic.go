//nolint:tagliatelle
package fun

import (
	"bytes"
	"context"
	"encoding/json"
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

	system   []anthropicSystem
	messages []anthropicMessage
	tools    []anthropicTool
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
		message := message
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

	if a.Client == nil {
		a.Client = newClient(
			func(req *http.Request) error {
				ctx := req.Context()
				estimatedTokens := getEstimatedTokens(ctx)
				// Rate limit retries.
				return anthropicRateLimiter.Take(ctx, a.APIKey, map[string]int{
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
				if limitTokens <= 100_000 { //nolint:gomnd
					// Even the free plan has tpd larger than 100,000, so if the limit is less, we know that it is tpm.
					rateLimits["tpm"] = resettingRateLimit{
						Limit:     limitTokens,
						Remaining: remainingTokens,
						Window:    time.Minute,
						Resets:    resetTokens,
					}
				} else if limitTokens/limitRequests >= 2000 { //nolint:gomnd
					// If the ratio between token limit and rpm is larger than 2000, we know it is tpd.
					rateLimits["tpd"] = resettingRateLimit{
						Limit:     limitTokens,
						Remaining: remainingTokens,
						Window:    24 * time.Hour, //nolint:gomnd
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
				anthropicRateLimiter.Set(a.APIKey, rateLimits)
			},
		)
	}

	return nil
}

// Chat implements [TextProvider] interface.
func (a *AnthropicTextProvider) Chat(ctx context.Context, message ChatMessage) (string, errors.E) { //nolint:maintidx
	callID := identifier.New().String()
	logger := zerolog.Ctx(ctx).With().Str("fun", callID).Logger()
	ctx = logger.WithContext(ctx)

	var callRecorder *TextRecorderCall
	if recorder := GetTextRecorder(ctx); recorder != nil {
		callRecorder = &TextRecorderCall{
			ID:         callID,
			Provider:   a,
			Messages:   nil,
			UsedTokens: nil,
			UsedTime:   nil,
		}
		defer recorder.recordCall(callRecorder)
	}

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
			callRecorder.addMessage(roleSystem, system.Text, "", "", false, false, nil)
		}

		for _, message := range messages {
			a.recordMessage(callRecorder, message, nil)
		}
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
		req.Header.Add("x-api-key", a.APIKey)
		req.Header.Add("anthropic-version", "2023-06-01")
		req.Header.Add("Content-Type", "application/json")
		if a.PromptCaching {
			req.Header.Add("anthropic-beta", "prompt-caching-2024-07-31")
		}
		// Rate limit the initial request.
		errE = anthropicRateLimiter.Take(ctx, a.APIKey, map[string]int{
			"rpm": 1,
			"tpd": estimatedTokens,
			"tpm": estimatedTokens,
		})
		if errE != nil {
			return "", errE
		}
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

			a.recordMessage(callRecorder, anthropicMessage{
				Role:    response.Role,
				Content: response.Content,
			}, nil)
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

			outputContent := []anthropicContent{}
			outputCalls := [][]TextRecorderCall{}

			for _, content := range response.Content {
				switch content.Type {
				case typeText:
					// We do nothing.
				case roleToolUse:
					output, calls, errE := a.callTool(ctx, content)
					if errE != nil {
						e := zerolog.Ctx(ctx).Warn().Err(errE).Str("name", content.Name).Str("apiRequest", apiRequest).Str("tool", content.ID)
						if content.Input != nil {
							e = e.RawJSON("input", content.Input)
						}
						e.Msg("tool error")
						c := errE.Error()
						outputContent = append(outputContent, anthropicContent{ //nolint:exhaustruct
							Type:      roleToolResult,
							ToolUseID: content.ID,
							IsError:   true,
							Content:   &c,
						})
					} else {
						outputContent = append(outputContent, anthropicContent{ //nolint:exhaustruct
							Type:      roleToolResult,
							ToolUseID: content.ID,
							Content:   &output,
						})
					}
					outputCalls = append(outputCalls, calls)
				default:
					return "", errors.WithDetails(
						ErrUnexpectedMessageType,
						"type", content.Type,
						"apiRequest", apiRequest,
					)
				}
			}

			if len(outputContent) == 0 {
				return "", errors.WithDetails(
					ErrToolCallsWithoutCalls,
					"apiRequest", apiRequest,
				)
			}

			messages = append(messages, anthropicMessage{
				Role:    roleUser,
				Content: outputContent,
			})

			if callRecorder != nil {
				a.recordMessage(callRecorder, messages[len(messages)-1], outputCalls)
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
				tokens += len(*content.Text) / 4 //nolint:gomnd
			}
			tokens += len(content.Input) / 4 //nolint:gomnd
			if content.Content != nil {
				tokens += len(*content.Content) / 4 //nolint:gomnd
			}
		}
	}
	for _, system := range a.system {
		tokens += len(system.Text) / 4 //nolint:gomnd
	}
	for _, tool := range a.tools {
		tokens += len(tool.Name) / 4            //nolint:gomnd
		tokens += len(tool.Description) / 4     //nolint:gomnd
		tokens += len(tool.InputJSONSchema) / 4 //nolint:gomnd
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

func (a *AnthropicTextProvider) callTool(ctx context.Context, content anthropicContent) (string, []TextRecorderCall, errors.E) {
	var tool TextTooler
	for _, t := range a.tools {
		if t.Name == content.Name {
			tool = t.tool
			break
		}
	}
	if tool == nil {
		return "", nil, errors.Errorf("%w: %s", ErrToolNotFound, content.Name)
	}

	logger := zerolog.Ctx(ctx).With().Str("tool", content.ID).Logger()
	ctx = logger.WithContext(ctx)

	if recorder := GetTextRecorder(ctx); recorder != nil {
		// If recorder is present in the current content, we create a new context with
		// a new recorder so that we can record a tool implemented with Text.
		ctx = WithTextRecorder(ctx)
	}

	output, errE := tool.Call(ctx, content.Input)
	// If there is no recorder, Calls returns nil.
	// Calls returns nil as well if the tool was not implemented with Text.
	return output, GetTextRecorder(ctx).Calls(), errE
}

func (a *AnthropicTextProvider) recordMessage(recorder *TextRecorderCall, message anthropicMessage, outputCalls [][]TextRecorderCall) {
	for i, content := range message.Content {
		var calls []TextRecorderCall
		if len(outputCalls) > 0 {
			calls = outputCalls[i]
		}
		switch content.Type {
		case typeText:
			if content.Text != nil {
				recorder.addMessage(message.Role, *content.Text, "", "", false, false, nil)
			}
		case roleToolUse:
			recorder.addMessage(roleToolUse, string(content.Input), content.ID, content.Name, false, false, nil)
		case roleToolResult:
			if content.Content != nil {
				recorder.addMessage(roleToolResult, *content.Content, content.ToolUseID, "", content.IsError, false, calls)
			}
		}
	}
}
