package fun

import (
	"context"
	"strings"
	"sync"
	"time"

	"gitlab.com/tozd/go/errors"
)

var textProviderRecorderContextKey = &contextKey{"text-provider-recorder"} //nolint:gochecknoglobals

type TextProviderRecorderUsedTokens struct {
	MaxTotal    int `json:"maxTotal"`    //nolint:tagliatelle
	MaxResponse int `json:"maxResponse"` //nolint:tagliatelle
	Prompt      int `json:"prompt"`
	Response    int `json:"response"`
	Total       int `json:"total"`
}

type TextProviderRecorderUsedTime struct {
	Prompt   time.Duration `json:"prompt"`
	Response time.Duration `json:"response"`
	Total    time.Duration `json:"total"`
}

type TextProviderRecorder struct {
	mu sync.Mutex

	messages   []map[string]string
	usedTokens map[string]TextProviderRecorderUsedTokens
	usedTime   map[string]TextProviderRecorderUsedTime
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

func (t *TextProviderRecorder) addUsedTokens(requestID string, maxTotal, maxResponse, prompt, response int) {
	t.mu.Lock()
	defer t.mu.Unlock()

	if t.usedTokens == nil {
		t.usedTokens = map[string]TextProviderRecorderUsedTokens{}
	}

	t.usedTokens[requestID] = TextProviderRecorderUsedTokens{
		MaxTotal:    maxTotal,
		MaxResponse: maxResponse,
		Prompt:      prompt,
		Response:    response,
		Total:       prompt + response,
	}
}

func (t *TextProviderRecorder) UsedTokens() map[string]TextProviderRecorderUsedTokens {
	t.mu.Lock()
	defer t.mu.Unlock()

	return t.usedTokens
}

func (t *TextProviderRecorder) addUsedTime(requestID string, prompt, response time.Duration) {
	t.mu.Lock()
	defer t.mu.Unlock()

	if t.usedTime == nil {
		t.usedTime = map[string]TextProviderRecorderUsedTime{}
	}

	t.usedTime[requestID] = TextProviderRecorderUsedTime{
		Prompt:   prompt,
		Response: response,
		Total:    prompt + response,
	}
}

func (t *TextProviderRecorder) UsedTime() map[string]TextProviderRecorderUsedTime {
	t.mu.Lock()
	defer t.mu.Unlock()

	return t.usedTime
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
