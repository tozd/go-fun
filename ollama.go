package fun

import (
	"context"
	"fmt"
	"net/http"
	"net/url"
	"slices"
	"sync"

	"github.com/ollama/ollama/api"
	"gitlab.com/tozd/go/errors"
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

// OllamaModel describes a model for [OllamaTextProvider].
type OllamaModel struct {
	Model    string
	Insecure bool
	Username string
	Password string
}

// OllamaTextProvider is a [TextProvider] which provides integration with
// text-based [Ollama] AI models.
//
// [Ollama]: https://ollama.com/
type OllamaTextProvider struct {
	Client            *http.Client
	Base              string
	Model             OllamaModel
	MaxContextLength  int
	MaxResponseLength int

	Seed        int
	Temperature float64

	client   *api.Client
	messages []api.Message
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

	o.messages = make([]api.Message, len(messages))
	for i, message := range messages {
		o.messages[i] = api.Message{
			Role:      message.Role,
			Content:   message.Content,
			Images:    nil,
			ToolCalls: nil,
		}
	}

	stream := false
	err = o.client.Pull(ctx, &api.PullRequest{ //nolint:exhaustruct
		Model:    o.Model.Model,
		Insecure: o.Model.Insecure,
		Username: o.Model.Username,
		Password: o.Model.Password,
		Stream:   &stream,
	}, func(_ api.ProgressResponse) error { return nil })
	if err != nil {
		return getStatusError(err)
	}

	resp, err := o.client.Show(ctx, &api.ShowRequest{ //nolint:exhaustruct
		Model: o.Model.Model,
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
	recorder := GetTextProviderRecorder(ctx)

	messages := slices.Clone(o.messages)
	messages = append(messages, api.Message{ //nolint:exhaustruct
		Role:    message.Role,
		Content: message.Content,
	})

	if recorder != nil {
		for _, message := range messages {
			o.recordMessage(recorder, message)
		}
	}

	// We allow only one request at a time to an Ollama host.
	// TODO: Should we do something better? Currently Ollama does not work well with multiple parallel requests.
	mu := ollamaRateLimiterLock(o.Base)
	mu.Lock()
	defer mu.Unlock()

	responses := []api.ChatResponse{}

	stream := false
	err := o.client.Chat(ctx, &api.ChatRequest{ //nolint:exhaustruct
		Model:    o.Model.Model,
		Messages: messages,
		Stream:   &stream,
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
		return "", getStatusError(err)
	}

	if len(responses) != 1 {
		return "", errors.WithDetails(
			ErrUnexpectedNumberOfMessages,
			"number", len(responses),
		)
	}

	if recorder != nil {
		recorder.addUsedTokens(
			"",
			o.MaxContextLength,
			o.MaxResponseLength,
			responses[0].Metrics.PromptEvalCount,
			responses[0].Metrics.EvalCount,
		)
		recorder.addUsedTime(
			"",
			responses[0].Metrics.PromptEvalDuration,
			responses[0].Metrics.EvalDuration,
		)

		o.recordMessage(recorder, responses[0].Message)
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
		)
	}

	if responses[0].Message.Role != roleAssistant {
		return "", errors.WithDetails(
			ErrUnexpectedRole,
			"role", responses[0].Message.Role,
		)
	}

	if responses[0].DoneReason != stopReason {
		return "", errors.WithDetails(
			ErrUnexpectedStop,
			"reason", responses[0].DoneReason,
		)
	}

	return responses[0].Message.Content, nil
}

func (o *OllamaTextProvider) recordMessage(recorder *TextProviderRecorder, message api.Message) {
	recorder.addMessage(message.Role, message.Content)
}
