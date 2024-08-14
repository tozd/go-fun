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

type anthropicRequest struct {
	Model       string             `json:"model"`
	Messages    []anthropicMessage `json:"messages"`
	MaxTokens   int                `json:"max_tokens"`
	System      string             `json:"system,omitempty"`
	Temperature float64            `json:"temperature"`
	Tools       []anthropicTool    `json:"tools,omitempty"`
}

type anthropicContent struct {
	Type      string          `json:"type"`
	Text      string          `json:"text,omitempty"`
	ID        string          `json:"id,omitempty"`
	Name      string          `json:"name,omitempty"`
	Input     json.RawMessage `json:"input,omitempty"`
	ToolUseID string          `json:"tool_use_id,omitempty"`
	IsError   bool            `json:"is_error,omitempty"`
	Content   string          `json:"content,omitempty"`
}

type anthropicResponse struct {
	ID         string             `json:"id"`
	Type       string             `json:"type"`
	Role       string             `json:"role"`
	Content    []anthropicContent `json:"content"`
	Model      string             `json:"model"`
	StopReason string             `json:"stop_reason"`
	Usage      struct {
		InputTokens  int `json:"input_tokens"`
		OutputTokens int `json:"output_tokens"`
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
	Name            string          `json:"name"`
	Description     string          `json:"description,omitempty"`
	InputJSONSchema json.RawMessage `json:"input_schema"`
}

var _ TextProvider = (*AnthropicTextProvider)(nil)

// AnthropicTextProvider is a [TextProvider] which provides integration with
// text-based [Anthropic] AI models.
//
// [Anthropic]: https://www.anthropic.com/
type AnthropicTextProvider struct {
	Client *http.Client
	APIKey string
	Model  string

	Tools map[string]Tooler

	Temperature float64

	system   string
	messages []anthropicMessage
	tools    []anthropicTool
}

// Init implements TextProvider interface.
func (a *AnthropicTextProvider) Init(ctx context.Context, messages []ChatMessage) errors.E {
	if a.messages != nil {
		return errors.WithStack(ErrAlreadyInitialized)
	}
	messagesWithoutSystem := []anthropicMessage{}
	for _, message := range messages {
		if message.Role == "system" {
			if a.system != "" {
				return errors.WithStack(ErrMultipleSystemMessages)
			}
			a.system = message.Content
		} else {
			messagesWithoutSystem = append(messagesWithoutSystem, anthropicMessage{
				Role: message.Role,
				Content: []anthropicContent{
					{ //nolint:exhaustruct
						Type: "text",
						Text: message.Content,
					},
				},
			})
		}
	}
	a.messages = messagesWithoutSystem

	for name, tool := range a.Tools {
		errE := tool.Init(ctx)
		if errE != nil {
			errors.Details(errE)["name"] = name
			return errE
		}

		a.tools = append(a.tools, anthropicTool{
			Name:            name,
			Description:     tool.GetDescription(),
			InputJSONSchema: tool.GetInputJSONSchema(),
		})
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
				// Token rate limit headers can be returned for both minute or day, whichever is smaller. Except for
				// the free tier, tokens per day are equal or larger than 1,000,000, so we compare to determine which one it is.
				if limitTokens >= 1_000_000 { //nolint:gomnd
					rateLimits["tpd"] = resettingRateLimit{
						Limit:     limitTokens,
						Remaining: remainingTokens,
						Window:    24 * time.Hour, //nolint:gomnd
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
			},
		)
	}

	return nil
}

// Chat implements TextProvider interface.
func (a *AnthropicTextProvider) Chat(ctx context.Context, message ChatMessage) (string, errors.E) { //nolint:maintidx
	messages := slices.Clone(a.messages)
	messages = append(messages, anthropicMessage{
		Role: message.Role,
		Content: []anthropicContent{
			{ //nolint:exhaustruct
				Type: "text",
				Text: message.Content,
			},
		},
	})

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
		var requestID string
		if resp != nil {
			requestID = resp.Header.Get("Request-Id")
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

		var response anthropicResponse
		errE = x.DecodeJSON(resp.Body, &response)
		if errE != nil {
			if requestID != "" {
				errors.Details(errE)["apiRequest"] = requestID
			}
			return "", errE
		}

		if response.Error != nil {
			errE = errors.WithDetails(ErrAPIResponseError, "payload", response.Error)
			if requestID != "" {
				errors.Details(errE)["apiRequest"] = requestID
			}
			return "", errE
		}

		tokens := zerolog.Dict()
		tokens.Int("maxTotal", estimatedTokens)
		tokens.Int("maxResponse", anthropicMaxResponseTokens)
		tokens.Int("prompt", response.Usage.InputTokens)
		tokens.Int("response", response.Usage.OutputTokens)
		tokens.Int("total", response.Usage.InputTokens+response.Usage.OutputTokens)
		e := zerolog.Ctx(ctx).Debug().Str("model", a.Model).Dict("tokens", tokens)
		if requestID != "" {
			e = e.Str("apiRequest", requestID)
		}
		e.Msg("usage")

		if response.Usage.InputTokens+response.Usage.OutputTokens > estimatedTokens {
			errE = errors.WithDetails(
				ErrUnexpectedNumberOfTokens,
				"prompt", response.Usage.InputTokens,
				"response", response.Usage.OutputTokens,
				"total", response.Usage.InputTokens+response.Usage.OutputTokens,
				"maxTotal", estimatedTokens,
				"maxResponse", anthropicMaxResponseTokens,
			)
			if requestID != "" {
				errors.Details(errE)["apiRequest"] = requestID
			}
			return "", errE
		}

		if response.StopReason == "tool_use" {
			if len(response.Content) == 0 {
				errE = errors.WithDetails(ErrUnexpectedNumberOfMessages, "number", len(response.Content))
				if requestID != "" {
					errors.Details(errE)["apiRequest"] = requestID
				}
				return "", errE
			}

			messages = append(messages, anthropicMessage{
				Role:    "assistant",
				Content: response.Content,
			})

			outputContent := []anthropicContent{}

			for _, content := range response.Content {
				switch content.Type {
				case "text":
					// We do nothing.
				case "tool_use":
					output, errE := a.callTool(ctx, content) //nolint:govet
					if errE != nil {
						e := zerolog.Ctx(ctx).Warn().Err(errE).Str("name", content.Name)
						if content.Input != nil {
							e = e.RawJSON("input", content.Input)
						}
						if requestID != "" {
							e = e.Str("apiRequest", requestID)
						}
						e.Msg("tool error")
						outputContent = append(outputContent, anthropicContent{ //nolint:exhaustruct
							Type:      "tool_result",
							ToolUseID: content.ID,
							IsError:   true,
							Content:   errE.Error(),
						})
					} else {
						outputContent = append(outputContent, anthropicContent{ //nolint:exhaustruct
							Type:      "tool_result",
							ToolUseID: content.ID,
							Content:   output,
						})
					}
				default:
					errE = errors.WithDetails(ErrUnexpectedMessageType, "type", content.Type)
					if requestID != "" {
						errors.Details(errE)["apiRequest"] = requestID
					}
					return "", errE
				}
			}

			if len(outputContent) == 0 {
				errE = errors.WithStack(ErrToolCallsWithoutCalls)
				if requestID != "" {
					errors.Details(errE)["apiRequest"] = requestID
				}
				return "", errE
			}

			messages = append(messages, anthropicMessage{
				Role:    "user",
				Content: outputContent,
			})

			continue
		}

		if response.StopReason != "end_turn" {
			errE = errors.WithDetails(ErrUnexpectedStop, "reason", response.StopReason)
			if requestID != "" {
				errors.Details(errE)["apiRequest"] = requestID
			}
			return "", errE
		}

		if len(response.Content) != 1 {
			errE = errors.WithDetails(ErrUnexpectedNumberOfMessages, "number", len(response.Content))
			if requestID != "" {
				errors.Details(errE)["apiRequest"] = requestID
			}
			return "", errE
		}
		if response.Content[0].Type != "text" {
			errE = errors.WithDetails(ErrUnexpectedMessageType, "type", response.Content[0].Type)
			if requestID != "" {
				errors.Details(errE)["apiRequest"] = requestID
			}
			return "", errE
		}

		return response.Content[0].Text, nil
	}
}

func (a *AnthropicTextProvider) estimatedTokens(messages []anthropicMessage) int {
	// We estimate tokens from training messages (including system message) by
	// dividing number of characters by 4.
	tokens := 0
	for _, message := range messages {
		for _, content := range message.Content {
			tokens += len(content.Text) / 4    //nolint:gomnd
			tokens += len(content.Input) / 4   //nolint:gomnd
			tokens += len(content.Content) / 4 //nolint:gomnd
		}
	}
	if a.system != "" {
		tokens += len(a.system) / 4 //nolint:gomnd
	}
	// Each output can be up to anthropicMaxResponseTokens so we assume final output
	// is at most that, with input the same.
	return tokens + 2*anthropicMaxResponseTokens
}

func (a *AnthropicTextProvider) callTool(ctx context.Context, content anthropicContent) (string, errors.E) {
	tool, ok := a.Tools[content.Name]
	if !ok {
		return "", errors.Errorf("%w: %s", ErrToolNotFound, content.Name)
	}

	logger := zerolog.Ctx(ctx).With().Str("tool", content.ID).Logger()
	ctx = logger.WithContext(ctx)

	return tool.Call(ctx, content.Input)
}
