package fun

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"slices"
	"sync"
	"time"

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
	// Client is a HTTP client to be used for API calls. If not provided
	// a rate-limited retryable HTTP client is initialized instead.
	Client *http.Client `json:"-"`

	// Base is a HTTP URL where Ollama instance is listening.
	Base string `json:"-"`

	// Model is the name of the model to be used.
	Model string `json:"model"`

	// ModelAccess allows Ollama to access private AI models.
	ModelAccess OllamaModelAccess `json:"-"`

	// MaxContextLength is the maximum total number of tokens allowed to be used
	// with the underlying AI model (i.e., the maximum context window).
	// If not provided, it is obtained from Ollama for the model.
	MaxContextLength int `json:"maxContextLength"`

	// MaxResponseLength is the maximum number of tokens allowed to be used in
	// a response with the underlying AI model. If not provided, -2 is used
	// which instructs Ollama to fill the context.
	MaxResponseLength int `json:"maxResponseLength"`

	// MaxExchanges is the maximum number of exchanges with the AI model per chat
	// to obtain the final response. Default is 10.
	MaxExchanges int `json:"maxExchanges"`

	// ForceOutputJSONSchema when set to true requests the AI model to force
	// the output JSON Schema for its output. When true, you should instruct
	// the AI model to respond in JSON.
	ForceOutputJSONSchema bool `json:"forceOutputJsonSchema"`

	// Seed is used to control the randomness of the AI model. Default is 0.
	Seed int `json:"seed"`

	// Temperature is how creative should the AI model be.
	// Default is 0 which means not at all.
	Temperature float64 `json:"temperature"`

	client           *api.Client
	messages         []api.Message
	tools            api.Tools
	toolers          map[string]TextTooler
	outputJSONSchema json.RawMessage
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

// Init implements [TextProvider] interface.
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
			Thinking:  "",
			Images:    nil,
			ToolCalls: nil,
			ToolName:  "",
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
	contextLength, ok := resp.ModelInfo[arch+".context_length"].(float64)
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
		// -2 = fill the context.
		o.MaxResponseLength = -2 //nolint:mnd
	}
	if o.MaxResponseLength > 0 && o.MaxResponseLength > o.MaxContextLength {
		return errors.WithDetails(
			ErrMaxResponseLengthOverContext,
			"maxTotal", o.MaxContextLength,
			"maxResponse", o.MaxResponseLength,
		)
	}

	if o.MaxExchanges == 0 {
		o.MaxExchanges = 10
	}

	return nil
}

// Chat implements [TextProvider] interface.
func (o *OllamaTextProvider) Chat(ctx context.Context, message ChatMessage) (string, errors.E) {
	callID := identifier.New().String()

	var callRecorder *TextRecorderCall
	if recorder := GetTextRecorder(ctx); recorder != nil {
		callRecorder = recorder.newCall(callID, o)
		defer recorder.recordCall(callRecorder)
	}

	logger := zerolog.Ctx(ctx).With().Str("fun", callID).Logger()
	ctx = logger.WithContext(ctx)

	messages := slices.Clone(o.messages)
	messages = append(messages, api.Message{
		Role:      message.Role,
		Content:   message.Content,
		Thinking:  "",
		Images:    nil,
		ToolCalls: nil,
		ToolName:  "",
	})

	if callRecorder != nil {
		for _, message := range messages {
			o.recordMessage(callRecorder, message, "")
		}

		callRecorder.notify("", nil)
	}

	// We allow only one request at a time to an Ollama host.
	// TODO: Should we do something better? Currently Ollama does not work well with multiple parallel requests.
	mu := ollamaRateLimiterLock(o.Base)
	mu.Lock()
	defer mu.Unlock()

	// Ollama does not provide request ID, so we make one ourselves.
	apiRequestNumber := 0
	for range o.MaxExchanges {
		apiRequestNumber++
		apiRequest := fmt.Sprintf("req_%d", apiRequestNumber)

		responses := []api.ChatResponse{}

		start := time.Now()
		stream := false
		err := o.client.Chat(ctx, &api.ChatRequest{
			Model:    o.Model,
			Messages: messages,
			Stream:   &stream,
			Format:   o.outputJSONSchema,
			Tools:    o.tools,
			Options: map[string]interface{}{
				"num_ctx":     o.MaxContextLength,
				"num_predict": o.MaxResponseLength,
				"seed":        o.Seed,
				"temperature": o.Temperature,
			},
			KeepAlive: nil,
			Think:     &api.ThinkValue{Value: true},
		}, func(resp api.ChatResponse) error {
			responses = append(responses, resp)
			return nil
		})
		if err != nil {
			errE := getStatusError(err)
			errors.Details(errE)["apiRequest"] = apiRequest
			return "", errE
		}

		apiCallDuration := time.Since(start)

		if len(responses) != 1 {
			errE := errors.Errorf("%w: not just one response", ErrUnexpectedMessage)
			errors.Details(errE)["number"] = len(responses)
			errors.Details(errE)["apiRequest"] = apiRequest
			return "", errE
		}

		toolCallIDPrefix := fmt.Sprintf("call_%d", len(messages))

		if callRecorder != nil {
			callRecorder.addUsedTokens(
				apiRequest,
				o.MaxContextLength,
				o.MaxResponseLength,
				responses[0].Metrics.PromptEvalCount,
				responses[0].Metrics.EvalCount,
				nil,
				nil,
				nil,
			)
			callRecorder.addUsedTime(
				apiRequest,
				responses[0].Metrics.PromptEvalDuration,
				responses[0].Metrics.EvalDuration,
				apiCallDuration,
			)

			o.recordMessage(callRecorder, responses[0].Message, toolCallIDPrefix)

			callRecorder.notify("", nil)
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

			// We make space for tool results (one per tool call) so that the messages slice
			// does not grow when appending below and invalidate pointers goroutines keep.
			messages = slices.Grow(messages, len(responses[0].Message.ToolCalls))

			if callRecorder != nil {
				// We grow the slice inside call recorder as well.
				callRecorder.prepareForToolMessages(len(responses[0].Message.ToolCalls))
			}

			var wg sync.WaitGroup
			for i, toolCall := range responses[0].Message.ToolCalls {
				toolCallID := fmt.Sprintf("%s_%d", toolCallIDPrefix, i)
				messages = append(messages, api.Message{
					Role:      roleTool,
					Content:   "",
					Thinking:  "",
					Images:    nil,
					ToolCalls: nil,
					ToolName:  toolCall.Function.Name,
				})
				result := &messages[len(messages)-1]

				toolCtx := ctx
				var toolMessage *TextRecorderMessage
				if callRecorder != nil {
					toolCtx, toolMessage = callRecorder.startToolMessage(ctx, toolCallID)
				}
				wg.Add(1)
				go func() {
					defer wg.Done()
					o.callToolWrapper(toolCtx, apiRequest, toolCall, toolCallID, result, callRecorder, toolMessage)
				}()
			}

			wg.Wait()

			continue
		}

		return responses[0].Message.Content, nil
	}

	return "", errors.WithDetails(
		ErrMaxExchangesReached,
		"maxExchanges", o.MaxExchanges,
	)
}

// InitOutputJSONSchema implements [WithOutputJSONSchema] interface.
func (o *OllamaTextProvider) InitOutputJSONSchema(_ context.Context, schema []byte) errors.E {
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

	return nil
}

// InitTools implements [WithTools] interface.
func (o *OllamaTextProvider) InitTools(ctx context.Context, tools map[string]TextTooler) errors.E {
	if o.tools != nil {
		return errors.WithStack(ErrAlreadyInitialized)
	}
	o.tools = []api.Tool{}
	o.toolers = map[string]TextTooler{}

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
		var parameters api.ToolFunctionParameters
		errE = x.UnmarshalWithoutUnknownFields(schema, &parameters)
		if errE != nil {
			errE = errors.Prefix(errE, ErrInvalidJSONSchema)
			errors.Details(errE)["name"] = name
			return errE
		}

		o.tools = append(o.tools, api.Tool{
			Type:  "function",
			Items: nil,
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

func (o *OllamaTextProvider) callToolWrapper(
	ctx context.Context, apiRequest string, toolCall api.ToolCall, toolCallID string, result *api.Message, callRecorder *TextRecorderCall, toolMessage *TextRecorderMessage,
) {
	if callRecorder != nil {
		defer func() {
			callRecorder.notify("", nil)
		}()
	}

	defer func() {
		if err := recover(); err != nil {
			content := fmt.Sprintf("Error: %s", err)
			result.Content = content

			toolMessage.setContent(content, true)
		}
	}()

	defer func() {
		toolMessage.setToolCalls(GetTextRecorder(ctx).Calls())
	}()

	logger := zerolog.Ctx(ctx).With().Str("tool", toolCallID).Logger()
	ctx = logger.WithContext(ctx)

	output, duration, errE := o.callTool(ctx, toolCall)
	if errE != nil {
		zerolog.Ctx(ctx).Warn().Err(errE).Str("name", toolCall.Function.Name).Str("apiRequest", apiRequest).
			Str("tool", toolCallID).RawJSON("input", json.RawMessage(toolCall.Function.Arguments.String())).Msg("tool error")
		content := "Error: " + errE.Error()
		result.Content = content

		toolMessage.setContent(content, true)
	} else {
		result.Content = output

		toolMessage.setContent(output, false)
	}

	toolMessage.setToolDuration(duration)
}

func (o *OllamaTextProvider) callTool(ctx context.Context, toolCall api.ToolCall) (string, Duration, errors.E) {
	tool, ok := o.toolers[toolCall.Function.Name]
	if !ok {
		return "", 0, errors.Errorf("%w: %s", ErrToolNotFound, toolCall.Function.Name)
	}

	start := time.Now()
	output, errE := tool.Call(ctx, json.RawMessage(toolCall.Function.Arguments.String()))
	duration := time.Since(start)
	return output, Duration(duration), errE
}

func (o *OllamaTextProvider) recordMessage(recorder *TextRecorderCall, message api.Message, toolCallIDPrefix string) {
	if message.Role == roleTool {
		panic(errors.New("recording tool result message should not happen"))
	} else if message.Content != "" || len(message.ToolCalls) == 0 {
		// Often with ToolCalls present, the content is empty and we do not record the content in that case.
		// But we do want to record empty content when there are no ToolCalls.
		recorder.addMessage(message.Role, message.Content, "", "", false)
	}
	for i, tool := range message.ToolCalls {
		recorder.addMessage(roleToolUse, tool.Function.Arguments.String(), fmt.Sprintf("%s_%d", toolCallIDPrefix, i), tool.Function.Name, false)
	}
}
