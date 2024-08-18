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

type TextProviderRecorderCall struct {
	ID         string                                    `json:"id"`
	Type       string                                    `json:"type"`
	Messages   []any                                     `json:"messages,omitempty"`
	UsedTokens map[string]TextProviderRecorderUsedTokens `json:"usedTokens,omitempty"` //nolint:tagliatelle
	UsedTime   map[string]TextProviderRecorderUsedTime   `json:"usedTime,omitempty"`   //nolint:tagliatelle
}

type TextProviderRecorderMessage map[string]string

func (c *TextProviderRecorderCall) addMessage(role, message string, params ...string) {
	m := TextProviderRecorderMessage{
		"type":    "message",
		"role":    role,
		"message": message,
	}

	if len(params)%2 != 0 {
		panic(errors.Errorf("odd number of elements in params: %s", strings.Join(params, ", ")))
	}

	for i := 0; i < len(params); i += 2 {
		m[params[i]] = params[i+1]
	}

	c.Messages = append(c.Messages, m)
}

func (c *TextProviderRecorderCall) addCall(call TextProviderRecorderCall) {
	c.Messages = append(c.Messages, call)
}

func (c *TextProviderRecorderCall) addUsedTokens(requestID string, maxTotal, maxResponse, prompt, response int) {
	if c.UsedTokens == nil {
		c.UsedTokens = map[string]TextProviderRecorderUsedTokens{}
	}

	c.UsedTokens[requestID] = TextProviderRecorderUsedTokens{
		MaxTotal:    maxTotal,
		MaxResponse: maxResponse,
		Prompt:      prompt,
		Response:    response,
		Total:       prompt + response,
	}
}

func (c *TextProviderRecorderCall) addUsedTime(requestID string, prompt, response time.Duration) {
	if c.UsedTime == nil {
		c.UsedTime = map[string]TextProviderRecorderUsedTime{}
	}

	c.UsedTime[requestID] = TextProviderRecorderUsedTime{
		Prompt:   prompt,
		Response: response,
		Total:    prompt + response,
	}
}

type TextProviderRecorder struct {
	mu sync.Mutex

	stack         []*TextProviderRecorderCall
	topLevelCalls []TextProviderRecorderCall
}

func (t *TextProviderRecorder) pushCall(id string) {
	t.mu.Lock()
	defer t.mu.Unlock()

	call := &TextProviderRecorderCall{
		ID:         id,
		Type:       "call",
		Messages:   nil,
		UsedTokens: nil,
		UsedTime:   nil,
	}

	t.stack = append(t.stack, call)
}

func (t *TextProviderRecorder) popCall() {
	t.mu.Lock()
	defer t.mu.Unlock()

	call := t.stack[len(t.stack)-1]
	t.stack = t.stack[:len(t.stack)-1]
	if len(t.stack) == 0 {
		t.topLevelCalls = append(t.topLevelCalls, *call)
	} else {
		t.stack[len(t.stack)-1].addCall(*call)
	}
}

func (t *TextProviderRecorder) Calls() []TextProviderRecorderCall {
	t.mu.Lock()
	defer t.mu.Unlock()

	return t.topLevelCalls
}

func (t *TextProviderRecorder) addMessage(role, message string, params ...string) {
	t.mu.Lock()
	defer t.mu.Unlock()

	t.stack[len(t.stack)-1].addMessage(role, message, params...)
}

func (t *TextProviderRecorder) addUsedTokens(requestID string, maxTotal, maxResponse, prompt, response int) {
	t.mu.Lock()
	defer t.mu.Unlock()

	t.stack[len(t.stack)-1].addUsedTokens(requestID, maxTotal, maxResponse, prompt, response)
}

func (t *TextProviderRecorder) addUsedTime(requestID string, prompt, response time.Duration) {
	t.mu.Lock()
	defer t.mu.Unlock()

	t.stack[len(t.stack)-1].addUsedTime(requestID, prompt, response)
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
