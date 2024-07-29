package fun

import (
	"context"
	"fmt"
	"net/http"
	"net/url"
	"slices"
	"sync"

	"github.com/hashicorp/go-cleanhttp"
	"github.com/ollama/ollama/api"
	"gitlab.com/tozd/go/errors"
)

var ollamaRateLimiter = map[string]*sync.Mutex{}
var ollamaRateLimiterMu = sync.Mutex{}

func ollamaRateLimiterLock(key string) *sync.Mutex {
	ollamaRateLimiterMu.Lock()
	defer ollamaRateLimiterMu.Unlock()

	if _, ok := ollamaRateLimiter[key]; !ok {
		ollamaRateLimiter[key] = &sync.Mutex{}
	}

	return ollamaRateLimiter[key]
}

var _ TextProvider = (*OllamaTextProvider)(nil)

type OllamaModel struct {
	Model    string
	Insecure bool
	Username string
	Password string
}

// OllamaTextProvider implements TextProvider interface.
type OllamaTextProvider struct {
	Client           *http.Client
	Base             string
	Model            OllamaModel
	MaxContextLength int

	Seed        int
	Temperature float64

	client   *api.Client
	messages []api.Message
}

func (o *OllamaTextProvider) Init(ctx context.Context, messages []ChatMessage) errors.E {
	if o.client != nil {
		return errors.New("already initialized")
	}

	base, err := url.Parse(o.Base)
	if err != nil {
		return errors.WithStack(err)
	}
	client := o.Client
	if client == nil {
		client = cleanhttp.DefaultPooledClient()
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
	err = o.client.Pull(ctx, &api.PullRequest{
		Model:    o.Model.Model,
		Insecure: o.Model.Insecure,
		Username: o.Model.Username,
		Password: o.Model.Password,
		Stream:   &stream,
	}, func(pr api.ProgressResponse) error { return nil })
	if err != nil {
		return errors.WithStack(err)
	}

	resp, err := o.client.Show(ctx, &api.ShowRequest{
		Model: o.Model.Model,
	})
	if err != nil {
		return errors.WithStack(err)
	}

	arch := resp.ModelInfo["general.architecture"].(string)
	contextLength := int(resp.ModelInfo[fmt.Sprintf("%s.context_length", arch)].(float64))

	if contextLength == 0 {
		return errors.New("unable to determine max context length")
	}

	if o.MaxContextLength == 0 {
		o.MaxContextLength = contextLength
	}

	if o.MaxContextLength > contextLength {
		return errors.New("max context length is larger than what model supports")
	}

	return nil
}

func (o *OllamaTextProvider) Chat(ctx context.Context, message ChatMessage) (string, errors.E) {
	messages := slices.Clone(o.messages)
	messages = append(messages, api.Message{
		Role:    message.Role,
		Content: message.Content,
	})

	// We allow only one request at a time to an Ollama host.
	// TODO: Should we do something better? Currently Ollama does not work well with multiple parallel requests.
	mu := ollamaRateLimiterLock(o.Base)
	mu.Lock()
	defer mu.Unlock()

	responses := []api.ChatResponse{}

	stream := false
	err := o.client.Chat(ctx, &api.ChatRequest{
		Model:    o.Model.Model,
		Messages: messages,
		Stream:   &stream,
		Options: map[string]interface{}{
			"num_ctx":     o.MaxContextLength,
			"num_predict": o.MaxContextLength,
			"seed":        o.Seed,
			"temperature": o.Temperature,
		},
	}, func(resp api.ChatResponse) error {
		responses = append(responses, resp)
		return nil
	})
	if err != nil {
		return "", errors.WithStack(err)
	}

	if len(responses) != 1 {
		return "", errors.New("unexpected number of responses")
	}
	if !responses[0].Done {
		return "", errors.New("not done")
	}

	if responses[0].Metrics.PromptEvalCount+responses[0].Metrics.EvalCount >= o.MaxContextLength {
		return "", errors.New("hit max context length")
	}

	// TODO: Log/expose responses[0].Metrics.

	return responses[0].Message.Content, nil
}
