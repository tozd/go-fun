package fun

import (
	"context"
	"strings"
	"sync"

	"gitlab.com/tozd/go/errors"
)

var textProviderRecorderContextKey = &contextKey{"text-provider-recorder"} //nolint:gochecknoglobals

type TextProviderRecorderUsage struct {
	MaxTotal    int `json:"maxTotal"`    //nolint:tagliatelle
	MaxResponse int `json:"maxResponse"` //nolint:tagliatelle
	Prompt      int `json:"prompt"`
	Response    int `json:"response"`
	Total       int `json:"total"`
}

type TextProviderRecorder struct {
	mu sync.Mutex

	messages []map[string]string
	usage    map[string]TextProviderRecorderUsage
}

func (t *TextProviderRecorder) addMessage(role, message string, params ...string) {
	m := map[string]string{
		"role":    role,
		"message": message,
	}

	if len(params)%2 != 0 {
		panic(errors.Errorf("odd number of elements in params: %s", strings.Join(params, ", ")))
	}

	for i := 0; i < len(params); i += 2 {
		m[params[i]] = params[i+1]
	}

	t.mu.Lock()
	defer t.mu.Unlock()

	t.messages = append(t.messages, m)
}

func (t *TextProviderRecorder) Messages() []map[string]string {
	t.mu.Lock()
	defer t.mu.Unlock()

	return t.messages
}

func (t *TextProviderRecorder) addUsage(requestID string, maxTotal, maxResponse, prompt, response int) {
	t.mu.Lock()
	defer t.mu.Unlock()

	if t.usage == nil {
		t.usage = map[string]TextProviderRecorderUsage{}
	}

	t.usage[requestID] = TextProviderRecorderUsage{
		MaxTotal:    maxTotal,
		MaxResponse: maxResponse,
		Prompt:      prompt,
		Response:    response,
		Total:       prompt + response,
	}
}

func (t *TextProviderRecorder) Usage() map[string]TextProviderRecorderUsage {
	t.mu.Lock()
	defer t.mu.Unlock()

	return t.usage
}

func WithTextProviderRecorder(ctx context.Context) context.Context {
	return context.WithValue(ctx, textProviderRecorderContextKey, new(TextProviderRecorder))
}

func GetTextProviderRecorder(ctx context.Context) *TextProviderRecorder {
	provider, ok := ctx.Value(textProviderRecorderContextKey).(*TextProviderRecorder)
	if !ok {
		return nil
	}
	return provider
}
