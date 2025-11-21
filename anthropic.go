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
	"strings"
	"sync"
	"time"

	"github.com/hashicorp/go-retryablehttp"
	"github.com/rs/zerolog"
	"gitlab.com/tozd/go/errors"
	"gitlab.com/tozd/go/x"
	"gitlab.com/tozd/identifier"
)

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

type anthropicThinking struct {
	Type         string `json:"type"`
	BudgetTokens int    `json:"budget_tokens"`
}

type anthropicRequest struct {
	Model       string             `json:"model"`
	Messages    []anthropicMessage `json:"messages"`
	MaxTokens   int                `json:"max_tokens"`
	Thinking    *anthropicThinking `json:"thinking,omitempty"`
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
	Thinking     string                 `json:"thinking,omitempty"`
	Signature    string                 `json:"signature,omitempty"`
	Data         string                 `json:"data,omitempty"`
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
	limitRequests, limitInputTokens, limitOutputTokens,
	remainingRequests, remainingInputTokens, remainingOutputTokens int,
	resetRequests, resetInputTokens, resetOutputTokens time.Time,
	ok bool, errE errors.E,
) {
	limitRequestsStr := resp.Header.Get("Anthropic-Ratelimit-Requests-Limit")         // Request per minute.
	remainingRequestsStr := resp.Header.Get("Anthropic-Ratelimit-Requests-Remaining") // Remaining requests in current window (a minute).
	resetRequestsStr := resp.Header.Get("Anthropic-Ratelimit-Requests-Reset")         // When will requests window reset.

	limitInputTokensStr := resp.Header.Get("Anthropic-Ratelimit-Input-Tokens-Limit")         // Input tokens per minute or day.
	remainingInputTokensStr := resp.Header.Get("Anthropic-Ratelimit-Input-Tokens-Remaining") // Remaining input tokens in current window (a minute).
	resetInputTokensStr := resp.Header.Get("Anthropic-Ratelimit-Input-Tokens-Reset")         // When will input tokens window reset.

	limitOutputTokensStr := resp.Header.Get("Anthropic-Ratelimit-Output-Tokens-Limit")         // Output tokens per minute or day.
	remainingOutputTokensStr := resp.Header.Get("Anthropic-Ratelimit-Output-Tokens-Remaining") // Remaining output tokens in current window (a minute).
	resetOutputTokensStr := resp.Header.Get("Anthropic-Ratelimit-Output-Tokens-Reset")         // When will output tokens window reset.

	if limitRequestsStr == "" || remainingRequestsStr == "" || resetRequestsStr == "" ||
		limitInputTokensStr == "" || remainingInputTokensStr == "" || resetInputTokensStr == "" ||
		limitOutputTokensStr == "" || remainingOutputTokensStr == "" || resetOutputTokensStr == "" {
		// ok == false here.
		return limitRequests, limitInputTokens, limitOutputTokens, remainingRequests,
			remainingInputTokens, remainingOutputTokens, resetRequests, resetInputTokens, resetOutputTokens, ok, errE
	}

	// We have all the headers we want.
	ok = true

	var err error
	limitRequests, err = strconv.Atoi(limitRequestsStr)
	if err != nil {
		errE = errors.WithDetails(err, "value", limitRequestsStr)
		return limitRequests, limitInputTokens, limitOutputTokens, remainingRequests,
			remainingInputTokens, remainingOutputTokens, resetRequests, resetInputTokens, resetOutputTokens, ok, errE
	}
	remainingRequests, err = strconv.Atoi(remainingRequestsStr)
	if err != nil {
		errE = errors.WithDetails(err, "value", remainingRequestsStr)
		return limitRequests, limitInputTokens, limitOutputTokens, remainingRequests,
			remainingInputTokens, remainingOutputTokens, resetRequests, resetInputTokens, resetOutputTokens, ok, errE
	}
	resetRequests, err = time.Parse(time.RFC3339, resetRequestsStr)
	if err != nil {
		errE = errors.WithDetails(err, "value", resetRequestsStr)
		return limitRequests, limitInputTokens, limitOutputTokens, remainingRequests,
			remainingInputTokens, remainingOutputTokens, resetRequests, resetInputTokens, resetOutputTokens, ok, errE
	}

	limitInputTokens, err = strconv.Atoi(limitInputTokensStr)
	if err != nil {
		errE = errors.WithDetails(err, "value", limitInputTokensStr)
		return limitRequests, limitInputTokens, limitOutputTokens, remainingRequests,
			remainingInputTokens, remainingOutputTokens, resetRequests, resetInputTokens, resetOutputTokens, ok, errE
	}
	remainingInputTokens, err = strconv.Atoi(remainingInputTokensStr)
	if err != nil {
		errE = errors.WithDetails(err, "value", remainingInputTokensStr)
		return limitRequests, limitInputTokens, limitOutputTokens, remainingRequests,
			remainingInputTokens, remainingOutputTokens, resetRequests, resetInputTokens, resetOutputTokens, ok, errE
	}
	resetInputTokens, err = time.Parse(time.RFC3339, resetInputTokensStr)
	if err != nil {
		errE = errors.WithDetails(err, "value", resetInputTokensStr)
		return limitRequests, limitInputTokens, limitOutputTokens, remainingRequests,
			remainingInputTokens, remainingOutputTokens, resetRequests, resetInputTokens, resetOutputTokens, ok, errE
	}

	limitOutputTokens, err = strconv.Atoi(limitOutputTokensStr)
	if err != nil {
		errE = errors.WithDetails(err, "value", limitOutputTokensStr)
		return limitRequests, limitInputTokens, limitOutputTokens, remainingRequests,
			remainingInputTokens, remainingOutputTokens, resetRequests, resetInputTokens, resetOutputTokens, ok, errE
	}
	remainingOutputTokens, err = strconv.Atoi(remainingOutputTokensStr)
	if err != nil {
		errE = errors.WithDetails(err, "value", remainingOutputTokensStr)
		return limitRequests, limitInputTokens, limitOutputTokens, remainingRequests,
			remainingInputTokens, remainingOutputTokens, resetRequests, resetInputTokens, resetOutputTokens, ok, errE
	}
	resetOutputTokens, err = time.Parse(time.RFC3339, resetOutputTokensStr)
	if err != nil {
		errE = errors.WithDetails(err, "value", resetOutputTokensStr)
		return limitRequests, limitInputTokens, limitOutputTokens, remainingRequests,
			remainingInputTokens, remainingOutputTokens, resetRequests, resetInputTokens, resetOutputTokens, ok, errE
	}

	return limitRequests, limitInputTokens, limitOutputTokens, remainingRequests,
		remainingInputTokens, remainingOutputTokens, resetRequests, resetInputTokens, resetOutputTokens, ok, errE
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

	// PromptCaching set to true enables prompt caching.
	PromptCaching bool `json:"promptCaching"`

	// ReasoningBudget is the budget of tokens to use for reasoning.
	// Default is 0 which means that reasoning is not enabled.
	ReasoningBudget int `json:"reasoningBudget"`

	// Temperature is how creative should the AI model be.
	// Ignored when extended thinking is enabled.
	// Default is 0 which means not at all.
	Temperature float64 `json:"temperature"`

	rateLimiterKey string
	system         []anthropicSystem
	messages       []anthropicMessage
	tools          []anthropicTool
}

// MarshalJSON implements json.Marshaler interface for AnthropicTextProvider.
func (a AnthropicTextProvider) MarshalJSON() ([]byte, error) {
	// We define a new type to not recurse into this same MarshalJSON.
	type P AnthropicTextProvider
	t := struct {
		P

		Type string `json:"type"`
	}{
		P:    P(a),
		Type: "anthropic",
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
				estimatedInputTokens, estimatedOutputTokens := getEstimatedTokens(ctx)
				// Rate limit retries.
				return anthropicRateLimiter.Take(ctx, a.rateLimiterKey, map[string]int{
					"rpm":  1,
					"itpd": estimatedInputTokens,
					"otpm": estimatedOutputTokens,
				})
			},
			nil,
			nil,
		)
		a.Client.Transport.(*retryablehttp.RoundTripper).Client.CheckRetry = func(ctx context.Context, resp *http.Response, err error) (bool, error) { //nolint:forcetypeassert,errcheck,lll
			if err != nil {
				check, err := retryablehttp.ErrorPropagatedRetryPolicy(ctx, resp, err)
				return check, errors.WithStack(err)
			}
			if resp.StatusCode == http.StatusTooManyRequests {
				// We read the body and provide it back.
				body, _ := io.ReadAll(resp.Body)
				resp.Body.Close() //nolint:errcheck,gosec
				resp.Body = io.NopCloser(bytes.NewReader(body))
				if resp.Header.Get("Content-Type") == applicationJSONHeader && json.Valid(body) {
					zerolog.Ctx(ctx).Warn().RawJSON("body", body).Msg("hit rate limit")
				} else {
					zerolog.Ctx(ctx).Warn().Str("body", string(body)).Msg("hit rate limit")
				}
			}
			limitRequests, limitInputTokens, limitOutputTokens,
				remainingRequests, remainingInputTokens, remainingOutputTokens,
				resetRequests, resetInputTokens, resetOutputTokens, ok, errE := parseAnthropicRateLimitHeaders(resp)
			if errE != nil {
				return false, errE
			}
			if ok {
				anthropicRateLimiter.Set(a.rateLimiterKey, map[string]any{
					"rpm": resettingRateLimit{
						Limit:     limitRequests,
						Remaining: remainingRequests,
						Window:    time.Minute,
						Resets:    resetRequests,
					},
					"itpd": resettingRateLimit{
						Limit:     limitInputTokens,
						Remaining: remainingInputTokens,
						Window:    time.Minute,
						Resets:    resetInputTokens,
					},
					"otpd": resettingRateLimit{
						Limit:     limitOutputTokens,
						Remaining: remainingOutputTokens,
						Window:    time.Minute,
						Resets:    resetOutputTokens,
					},
				})
			}
			check, err := retryablehttp.ErrorPropagatedRetryPolicy(ctx, resp, err)
			return check, errors.WithStack(err)
		}
	}

	if a.MaxContextLength == 0 {
		a.MaxContextLength = a.maxContextLength()
	}

	if a.MaxResponseLength == 0 {
		a.MaxResponseLength = a.maxResponseTokens()
	}

	if a.MaxExchanges == 0 {
		a.MaxExchanges = 10
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
	lastCacheBreakpoint := len(messages) - 1

	if callRecorder != nil {
		for _, system := range a.system {
			callRecorder.addMessage(roleSystem, system.Text, "", "", false)
		}

		for _, message := range messages {
			errE := a.recordMessage(callRecorder, message)
			if errE != nil {
				return "", errE
			}
		}

		callRecorder.notify("", nil)
	}

	for range a.MaxExchanges {
		if len(a.tools) > 0 && a.PromptCaching {
			// If tools are defined and prompt caching is enabled, we can improve performance by
			// setting 2 cache breakpoints. Together with the cache breakpoint set during provider's
			// initialization, this uses up to 3 from 4 available cache breakpoints.
			// First we clear any cache breakpoints we might have set during this call.
			for i := len(a.messages); i < len(messages); i++ {
				messages[i].Content[len(messages[i].Content)-1].CacheControl = nil
			}
			// We set one cache breakpoint where we set the cache breakpoint the last
			// time (to facilitate cache read for the current exchange). For the first
			// exchange this will be the same as the second cache breaking point.
			messages[lastCacheBreakpoint].Content[len(messages[lastCacheBreakpoint].Content)-1].CacheControl = &anthropicCacheControl{
				Type: "ephemeral",
			}
			// We set second cache breaking at the end of currently available messages
			// (to facilitate cache write in preparation for the next exchange).
			messages[len(messages)-1].Content[len(messages[len(messages)-1].Content)-1].CacheControl = &anthropicCacheControl{
				Type: "ephemeral",
			}
			lastCacheBreakpoint = len(messages) - 1
		}

		temperature := a.Temperature
		var thinking *anthropicThinking
		if a.ReasoningBudget > 0 {
			thinking = &anthropicThinking{
				Type:         "enabled",
				BudgetTokens: a.ReasoningBudget,
			}
			// Temperature must be 1 when extended thinking is enabled.
			temperature = 1
		}
		request, errE := x.MarshalWithoutEscapeHTML(anthropicRequest{
			Model:       a.Model,
			Messages:    messages,
			MaxTokens:   a.MaxResponseLength,
			Thinking:    thinking,
			System:      a.system,
			Temperature: temperature,
			Tools:       a.tools,
		})
		if errE != nil {
			return "", errE
		}

		estimatedInputTokens, estimatedOutputTokens := a.estimatedTokens(messages)

		req, err := http.NewRequestWithContext(
			withEstimatedTokens(ctx, estimatedInputTokens, estimatedOutputTokens),
			http.MethodPost,
			"https://api.anthropic.com/v1/messages",
			bytes.NewReader(request),
		)
		if err != nil {
			return "", errors.WithStack(err)
		}
		req.Header.Add("X-Api-Key", a.APIKey)
		req.Header.Add("Anthropic-Version", "2023-06-01")
		req.Header.Add("Anthropic-Beta", "output-128k-2025-02-19")
		req.Header.Add("Content-Type", "application/json")
		// Rate limit the initial request.
		errE = anthropicRateLimiter.Take(ctx, a.rateLimiterKey, map[string]int{
			"rpm":  1,
			"itpd": estimatedInputTokens,
			"otpm": estimatedOutputTokens,
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
		defer resp.Body.Close()              //nolint:errcheck
		defer io.Copy(io.Discard, resp.Body) //nolint:errcheck

		if apiRequest == "" {
			body, _ := io.ReadAll(resp.Body)
			return "", errors.WithDetails(ErrMissingRequestID, body, string(body))
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
				a.MaxContextLength,
				a.MaxResponseLength,
				response.Usage.InputTokens,
				response.Usage.OutputTokens,
				response.Usage.CacheCreationInputTokens,
				response.Usage.CacheReadInputTokens,
				// TODO: Anthropic does not support extended thinking usage currently.
				nil,
			)
			callRecorder.addUsedTime(
				apiRequest,
				0,
				0,
				apiCallDuration,
			)

			errE := a.recordMessage(callRecorder, anthropicMessage{
				Role:    response.Role,
				Content: response.Content,
			})
			if errE != nil {
				return "", errE
			}

			callRecorder.notify("", nil)
		}

		if response.Usage.InputTokens+response.Usage.OutputTokens >= a.MaxContextLength {
			return "", errors.WithDetails(
				ErrUnexpectedNumberOfTokens,
				"prompt", response.Usage.InputTokens,
				"response", response.Usage.OutputTokens,
				"total", response.Usage.InputTokens+response.Usage.OutputTokens,
				"maxTotal", a.MaxContextLength,
				"maxResponse", a.MaxResponseLength,
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
				errE := errors.Errorf("%w: expected tool calls", ErrUnexpectedMessage)
				errors.Details(errE)["number"] = len(response.Content)
				errors.Details(errE)["apiRequest"] = apiRequest
				return "", errE
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
				case typeText, roleThinking, roleRedactedThinking:
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

		var text *string
		for _, content := range response.Content {
			if content.Type == roleThinking {
				continue
			}
			if content.Type == roleRedactedThinking {
				continue
			}
			if content.Type != typeText {
				return "", errors.WithDetails(
					ErrUnexpectedMessageType,
					"type", content.Type,
					"apiRequest", apiRequest,
				)
			}
			if content.Text == nil {
				errE := errors.Errorf("%w: message content is nil", ErrUnexpectedMessageType)
				errors.Details(errE)["apiRequest"] = apiRequest
				return "", errE
			}
			if text != nil {
				errE := errors.Errorf("%w: not just one response", ErrUnexpectedMessage)
				errors.Details(errE)["apiRequest"] = apiRequest
				return "", errE
			}
			text = content.Text
		}

		if text == nil {
			errE := errors.Errorf("%w: message content is nil", ErrUnexpectedMessageType)
			errors.Details(errE)["apiRequest"] = apiRequest
			return "", errE
		}

		return *text, nil
	}

	return "", errors.WithDetails(
		ErrMaxExchangesReached,
		"maxExchanges", a.MaxExchanges,
	)
}

func (a *AnthropicTextProvider) estimatedTokens(messages []anthropicMessage) (int, int) {
	// We estimate inputTokens from training messages (including system message) by
	// dividing number of characters by 4.
	inputTokens := 0
	for _, message := range messages {
		for _, content := range message.Content {
			if content.Text != nil {
				inputTokens += len(*content.Text) / 4 //nolint:mnd
			}
			inputTokens += len(content.Input) / 4 //nolint:mnd
			if content.Content != nil {
				inputTokens += len(*content.Content) / 4 //nolint:mnd
			}
		}
	}
	for _, system := range a.system {
		inputTokens += len(system.Text) / 4 //nolint:mnd
	}
	for _, tool := range a.tools {
		inputTokens += len(tool.Name) / 4            //nolint:mnd
		inputTokens += len(tool.Description) / 4     //nolint:mnd
		inputTokens += len(tool.InputJSONSchema) / 4 //nolint:mnd
	}
	// TODO: Can we provide a better estimate for output tokens?
	return inputTokens, a.MaxResponseLength
}

func (a *AnthropicTextProvider) maxContextLength() int {
	// Currently this is the same for all Anthropic models.
	return 200_000 //nolint:mnd
}

func (a *AnthropicTextProvider) maxResponseTokens() int {
	if strings.Contains(a.Model, "3-7") {
		if a.ReasoningBudget > 0 {
			// This is the maximum without output-128k-2025-02-19 beta header and we still use it.
			// One can manually set MaxResponseLength to a different (e.g., higher) value.
			return 64000 //nolint:mnd
		}
		return 8192 //nolint:mnd
	}
	if strings.Contains(a.Model, "3-5") {
		return 8192 //nolint:mnd
	}
	return 4096 //nolint:mnd
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

func (a *AnthropicTextProvider) recordMessage(recorder *TextRecorderCall, message anthropicMessage) errors.E {
	for _, content := range message.Content {
		switch content.Type {
		case roleToolResult:
			return errors.New("recording tool result message should not happen")
		case typeText:
			if content.Text != nil {
				recorder.addMessage(message.Role, *content.Text, "", "", false)
			}
		case roleToolUse:
			recorder.addMessage(roleToolUse, string(content.Input), content.ID, content.Name, false)
		case roleThinking:
			recorder.addMessage(roleThinking, content.Thinking, "", "", false)
		case roleRedactedThinking:
			recorder.addMessage(roleRedactedThinking, "", "", "", false)
		default:
			return errors.WithDetails(
				ErrUnexpectedMessageType,
				"type", content.Type,
			)
		}
	}
	return nil
}
