package fun

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"slices"
	"strconv"
	"sync"

	"github.com/ollama/ollama/api"
	"github.com/rs/zerolog"
	"github.com/santhosh-tekuri/jsonschema/v6"
	"gitlab.com/tozd/go/errors"
	"gitlab.com/tozd/go/x"
	"gitlab.com/tozd/identifier"
)

var (
	ollamaRateLimiter   = map[string]*sync.Mutex{} //nolint:gochecknoglobals
	ollamaRateLimiterMu = sync.Mutex{}             //nolint:gochecknoglobals
)

func getStatusError(err error) errors.E {
	var statusError api.StatusError
	if errors.As(err, &statusError) {
		return errors.WithDetails(
			ErrAPIRequestFailed,
			"code", statusError.StatusCode,
			"status", statusError.Status,
			"errorMessage", statusError.ErrorMessage,
		)
	}
	return errors.Prefix(err, ErrAPIRequestFailed)
}

func ollamaRateLimiterLock(key string) *sync.Mutex {
	ollamaRateLimiterMu.Lock()
	defer ollamaRateLimiterMu.Unlock()

	if _, ok := ollamaRateLimiter[key]; !ok {
		ollamaRateLimiter[key] = &sync.Mutex{}
	}

	return ollamaRateLimiter[key]
}

// This is a copy of what is supported in ToolFunction.
// See: https://github.com/ollama/ollama/issues/6377
type ollamaToolFunctionParameters struct {
	Type       string   `json:"type"`
	Required   []string `json:"required"`
	Properties map[string]struct {
		Type        string   `json:"type"`
		Description string   `json:"description"`
		Enum        []string `json:"enum,omitempty"`
	} `json:"properties"`
}

var _ TextProvider = (*OllamaTextProvider)(nil)

// OllamaModelAccess describes access to a model for [OllamaTextProvider].
type OllamaModelAccess struct {
	Insecure bool
	Username string
	Password string
}

// OllamaTextProvider is a [TextProvider] which provides integration with
// text-based [Ollama] AI models.
//
// [Ollama]: https://ollama.com/
type OllamaTextProvider struct {
	Client            *http.Client      `json:"-"`
	Base              string            `json:"-"`
	Model             string            `json:"model"`
	ModelAccess       OllamaModelAccess `json:"-"`
	MaxContextLength  int               `json:"maxContextLength"`
	MaxResponseLength int               `json:"maxResponseLength"`

	Seed        int     `json:"seed"`
	Temperature float64 `json:"temperature"`

	client   *api.Client
	messages []api.Message
	tools    api.Tools
	toolers  map[string]Tooler
}

func (o OllamaTextProvider) MarshalJSON() ([]byte, error) {
	// We define a new type to not recurse into this same MarshalJSON.
	type P OllamaTextProvider
	t := struct {
		Type string `json:"type"`
		P
	}{
		Type: "ollama",
		P:    P(o),
	}
	return x.MarshalWithoutEscapeHTML(t)
}

// Init implements TextProvider interface.
func (o *OllamaTextProvider) Init(ctx context.Context, messages []ChatMessage) errors.E {
	if o.client != nil {
		return errors.WithStack(ErrAlreadyInitialized)
	}

	base, err := url.Parse(o.Base)
	if err != nil {
		return errors.WithStack(err)
	}
	client := o.Client
	if client == nil {
		client = newClient(
			// We lock in OllamaTextProvider.Chat instead.
			nil,
			// No headers to parse.
			nil,
			// Nothing to update after every request.
			nil,
		)
	}
	o.client = api.NewClient(base, client)

	o.messages = []api.Message{}
	for _, message := range messages {
		o.messages = append(o.messages, api.Message{
			Role:      message.Role,
			Content:   message.Content,
			Images:    nil,
			ToolCalls: nil,
		})
	}

	stream := false
	err = o.client.Pull(ctx, &api.PullRequest{ //nolint:exhaustruct
		Model:    o.Model,
		Insecure: o.ModelAccess.Insecure,
		Username: o.ModelAccess.Username,
		Password: o.ModelAccess.Password,
		Stream:   &stream,
	}, func(_ api.ProgressResponse) error { return nil })
	if err != nil {
		return getStatusError(err)
	}

	resp, err := o.client.Show(ctx, &api.ShowRequest{ //nolint:exhaustruct
		Model: o.Model,
	})
	if err != nil {
		return getStatusError(err)
	}

	arch, ok := resp.ModelInfo["general.architecture"].(string)
	if !ok {
		return errors.WithStack(ErrModelMaxContextLength)
	}
	contextLength, ok := resp.ModelInfo[fmt.Sprintf("%s.context_length", arch)].(float64)
	if !ok {
		return errors.WithStack(ErrModelMaxContextLength)
	}
	contextLengthInt := int(contextLength)

	if contextLengthInt == 0 {
		return errors.WithStack(ErrModelMaxContextLength)
	}

	if o.MaxContextLength == 0 {
		o.MaxContextLength = contextLengthInt
	}
	if o.MaxContextLength > contextLengthInt {
		return errors.WithDetails(
			ErrMaxContextLengthOverModel,
			"maxTotal", o.MaxContextLength,
			"model", contextLengthInt,
		)
	}

	if o.MaxResponseLength == 0 {
		// -2 = fill context.
		o.MaxResponseLength = -2 //nolint:gomnd
	}
	if o.MaxResponseLength > 0 && o.MaxResponseLength > o.MaxContextLength {
		return errors.WithDetails(
			ErrMaxResponseLengthOverContext,
			"maxTotal", o.MaxContextLength,
			"maxResponse", o.MaxResponseLength,
		)
	}

	return nil
}

// Chat implements TextProvider interface.
func (o *OllamaTextProvider) Chat(ctx context.Context, message ChatMessage) (string, errors.E) {
	callID := identifier.New().String()
	logger := zerolog.Ctx(ctx).With().Str("fun", callID).Logger()
	ctx = logger.WithContext(ctx)

	var callRecorder *TextRecorderCall
	if recorder := GetTextRecorder(ctx); recorder != nil {
		callRecorder = &TextRecorderCall{
			ID:         callID,
			Provider:   o,
			Messages:   nil,
			UsedTokens: nil,
			UsedTime:   nil,
		}
		defer recorder.recordCall(callRecorder)
	}

	messages := slices.Clone(o.messages)
	messages = append(messages, api.Message{
		Role:      message.Role,
		Content:   message.Content,
		Images:    nil,
		ToolCalls: nil,
	})

	if callRecorder != nil {
		for _, message := range messages {
			o.recordMessage(callRecorder, message, nil)
		}
	}

	// We allow only one request at a time to an Ollama host.
	// TODO: Should we do something better? Currently Ollama does not work well with multiple parallel requests.
	mu := ollamaRateLimiterLock(o.Base)
	mu.Lock()
	defer mu.Unlock()

	// Ollama does not provide request ID, so we make one ourselves.
	apiRequestNumber := 0
	for {
		apiRequestNumber++
		apiRequest := strconv.Itoa(apiRequestNumber)

		responses := []api.ChatResponse{}

		stream := false
		err := o.client.Chat(ctx, &api.ChatRequest{ //nolint:exhaustruct
			Model:    o.Model,
			Messages: messages,
			Stream:   &stream,
			Tools:    o.tools,
			Options: map[string]interface{}{
				"num_ctx":     o.MaxContextLength,
				"num_predict": o.MaxResponseLength,
				"seed":        o.Seed,
				"temperature": o.Temperature,
			},
		}, func(resp api.ChatResponse) error {
			responses = append(responses, resp)
			return nil
		})
		if err != nil {
			errE := getStatusError(err)
			errors.Details(errE)["apiRequest"] = apiRequest
			return "", errE
		}

		if len(responses) != 1 {
			return "", errors.WithDetails(
				ErrUnexpectedNumberOfMessages,
				"number", len(responses),
				"apiRequest", apiRequest,
			)
		}

		if callRecorder != nil {
			callRecorder.addUsedTokens(
				apiRequest,
				o.MaxContextLength,
				o.MaxResponseLength,
				responses[0].Metrics.PromptEvalCount,
				responses[0].Metrics.EvalCount,
			)
			callRecorder.addUsedTime(
				apiRequest,
				responses[0].Metrics.PromptEvalDuration,
				responses[0].Metrics.EvalDuration,
			)

			o.recordMessage(callRecorder, responses[0].Message, nil)
		}

		if responses[0].Metrics.PromptEvalCount+responses[0].Metrics.EvalCount >= o.MaxContextLength {
			return "", errors.WithDetails(
				ErrUnexpectedNumberOfTokens,
				"content", responses[0].Message.Content,
				"prompt", responses[0].Metrics.PromptEvalCount,
				"response", responses[0].Metrics.EvalCount,
				"total", responses[0].Metrics.PromptEvalCount+responses[0].Metrics.EvalCount,
				"maxTotal", o.MaxContextLength,
				"maxResponse", o.MaxResponseLength,
				"apiRequest", apiRequest,
			)
		}

		if responses[0].Message.Role != roleAssistant {
			return "", errors.WithDetails(
				ErrUnexpectedRole,
				"role", responses[0].Message.Role,
				"apiRequest", apiRequest,
			)
		}

		if responses[0].DoneReason != stopReason {
			return "", errors.WithDetails(
				ErrUnexpectedStop,
				"reason", responses[0].DoneReason,
				"apiRequest", apiRequest,
			)
		}

		if len(responses[0].Message.ToolCalls) > 0 {
			// We have already recorded this message above.
			messages = append(messages, responses[0].Message)

			for i, toolCall := range responses[0].Message.ToolCalls {
				output, calls, errE := o.callTool(ctx, toolCall, i)
				if errE != nil {
					zerolog.Ctx(ctx).Warn().Err(errE).Str("name", toolCall.Function.Name).Str("apiRequest", apiRequest).
						Str("tool", strconv.Itoa(i)).RawJSON("input", json.RawMessage(toolCall.Function.Arguments.String())).Msg("tool error")
					content := fmt.Sprintf("Error: %s", errE.Error())
					messages = append(messages, api.Message{
						Role:      roleTool,
						Content:   content,
						Images:    nil,
						ToolCalls: nil,
					})
				} else {
					messages = append(messages, api.Message{
						Role:      roleTool,
						Content:   output,
						Images:    nil,
						ToolCalls: nil,
					})
				}

				if callRecorder != nil {
					o.recordMessage(callRecorder, messages[len(messages)-1], calls)
				}
			}

			continue
		}

		return responses[0].Message.Content, nil
	}
}

// InitTools implements WithTools interface.
func (o *OllamaTextProvider) InitTools(ctx context.Context, tools map[string]Tooler) errors.E {
	if o.tools != nil {
		return errors.WithStack(ErrAlreadyInitialized)
	}
	o.tools = []api.Tool{}
	o.toolers = map[string]Tooler{}

	for name, tool := range tools {
		errE := tool.Init(ctx)
		if errE != nil {
			errors.Details(errE)["name"] = name
			return errE
		}

		// Ollama is very restricted in what JSON Schema it supports so we have to
		// manually convert JSON Schema for its API.
		// See: https://github.com/ollama/ollama/issues/6377
		schema := tool.GetInputJSONSchema()

		// We want to remove additionalProperties which is required for OpenAI but
		// not supported in ollamaToolFunctionParameters.
		tempSchema, err := jsonschema.UnmarshalJSON(bytes.NewReader(schema))
		if err != nil {
			errE = errors.Prefix(err, ErrInvalidJSONSchema)
			errors.Details(errE)["name"] = name
			return errE
		}
		if ts, ok := tempSchema.(map[string]any); ok {
			delete(ts, "additionalProperties")
			schema, errE = x.MarshalWithoutEscapeHTML(ts)
			if errE != nil {
				errors.Details(errE)["name"] = name
				return errE
			}
		}

		// We do not allow unknown fields to make sure we can fully support provided JSON Schema.
		var parameters ollamaToolFunctionParameters
		errE = x.UnmarshalWithoutUnknownFields(schema, &parameters)
		if errE != nil {
			errE = errors.Prefix(errE, ErrInvalidJSONSchema)
			errors.Details(errE)["name"] = name
			return errE
		}

		o.tools = append(o.tools, api.Tool{
			Type: "function",
			Function: api.ToolFunction{
				Name:        name,
				Description: tool.GetDescription(),
				Parameters:  parameters,
			},
		})
		o.toolers[name] = tool
	}

	return nil
}

func (o *OllamaTextProvider) callTool(ctx context.Context, toolCall api.ToolCall, i int) (string, []TextRecorderCall, errors.E) {
	tool, ok := o.toolers[toolCall.Function.Name]
	if !ok {
		return "", nil, errors.Errorf("%w: %s", ErrToolNotFound, toolCall.Function.Name)
	}

	logger := zerolog.Ctx(ctx).With().Str("tool", strconv.Itoa(i)).Logger()
	ctx = logger.WithContext(ctx)

	if recorder := GetTextRecorder(ctx); recorder != nil {
		// If recorder is present in the current content, we create a new context with
		// a new recorder so that we can record a tool implemented with Text.
		ctx = WithTextRecorder(ctx)
	}

	output, errE := tool.Call(ctx, json.RawMessage(toolCall.Function.Arguments.String()))
	// If there is no recorder, Calls returns nil.
	// Calls returns nil as well if the tool was not implemented with Text.
	return output, GetTextRecorder(ctx).Calls(), errE
}

func (o *OllamaTextProvider) recordMessage(recorder *TextRecorderCall, message api.Message, calls []TextRecorderCall) {
	if message.Role == roleTool {
		// TODO: How to provide our tool call ID (the "i" parameter to callTool method).
		recorder.addMessage(roleToolResult, message.Content, "", "", false, false, calls)
	} else if message.Content != "" || len(message.ToolCalls) == 0 {
		// Often with ToolCalls present, the content is empty and we do not record the content in that case.
		// But we do want to record empty content when there are no ToolCalls.
		recorder.addMessage(message.Role, message.Content, "", "", false, false, calls)
	}
	for i, tool := range message.ToolCalls {
		recorder.addMessage(roleToolUse, tool.Function.Arguments.String(), strconv.Itoa(i), tool.Function.Name, false, false, calls)
	}
}
