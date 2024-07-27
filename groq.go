package fun

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"net/http"
	"slices"

	"gitlab.com/tozd/go/errors"
	"gitlab.com/tozd/go/x"
)

type groqModel struct {
	ID            string `json:"id"`
	Object        string `json:"object"`
	Created       int64  `json:"created"`
	OwnedBy       string `json:"owned_by"`
	Active        bool   `json:"active"`
	ContextWindow int    `json:"context_window"`
}

type groqResponse struct {
	ID                string  `json:"id"`
	Object            string  `json:"object"`
	Created           int64   `json:"created"`
	Model             string  `json:"model"`
	SystemFingerprint *string `json:"system_fingerprint,omitempty"`
	Choices           []struct {
		Index   int `json:"index"`
		Message struct {
			Role    string `json:"role"`
			Content string `json:"content"`
		} `json:"message"`
		FinishReason     string    `json:"finish_reason"`
		LogProbabilities []float64 `json:"logprobs,omitempty"`
	} `json:"choices"`
	Usage struct {
		PromptTokens     int     `json:"prompt_tokens"`
		CompletionTokens int     `json:"completion_tokens"`
		TotalTokens      int     `json:"total_tokens"`
		PromptTime       float64 `json:"prompt_time"`
		CompletionTime   float64 `json:"completion_time"`
		TotalTime        float64 `json:"total_time"`
	} `json:"usage"`
}

var _ TextProvider = (*GroqTextProvider)(nil)

// GroqTextProvider implements TextProvider interface.
type GroqTextProvider struct {
	Client *http.Client
	Model  string

	Seed        int
	Temperature float64

	messages         []ChatMessage
	maxContextLength int
}

func (o *GroqTextProvider) Init(ctx context.Context, messages []ChatMessage) errors.E {
	if o.messages != nil {
		return errors.New("already initialized")
	}
	o.messages = messages

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, fmt.Sprintf("https://api.groq.com/openai/v1/models/%s", o.Model), nil)
	if err != nil {
		return errors.WithStack(err)
	}
	resp, err := o.Client.Do(req)
	if err != nil {
		return errors.WithStack(err)
	}
	defer resp.Body.Close()
	defer io.Copy(io.Discard, resp.Body)

	var model groqModel
	errE := x.DecodeJSONWithoutUnknownFields(resp.Body, &model)
	if errE != nil {
		return errE
	}

	if !model.Active {
		return errors.New("model not active")
	}

	o.maxContextLength = model.ContextWindow

	return nil
}

func (o *GroqTextProvider) Chat(ctx context.Context, message ChatMessage) (string, errors.E) {
	messages := slices.Clone(o.messages)
	messages = append(messages, message)

	request, errE := x.MarshalWithoutEscapeHTML(map[string]interface{}{
		"messages":    messages,
		"model":       o.Model,
		"seed":        o.Seed,
		"temperature": o.Temperature,
	})
	if errE != nil {
		return "", errE
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, "https://api.groq.com/openai/v1/chat/completions", bytes.NewReader(request))
	if err != nil {
		return "", errors.WithStack(err)
	}
	resp, err := o.Client.Do(req)
	if err != nil {
		return "", errors.WithStack(err)
	}
	defer resp.Body.Close()
	defer io.Copy(io.Discard, resp.Body)

	var response groqResponse
	errE = x.DecodeJSONWithoutUnknownFields(resp.Body, &response)
	if errE != nil {
		return "", errE
	}

	if len(response.Choices) != 1 {
		return "", errors.New("unexpected number of choices")
	}
	if response.Choices[0].FinishReason != "stop" {
		return "", errors.New("not done")
	}

	if response.Usage.CompletionTokens >= o.maxContextLength {
		return "", errors.New("response hit max context length")
	}
	if response.Usage.PromptTokens >= o.maxContextLength {
		return "", errors.New("prompt hit max context length")
	}

	// TODO: Log/expose response.Usage.

	return response.Choices[0].Message.Content, nil
}
