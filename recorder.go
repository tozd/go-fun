package fun

import (
	"context"
	"strings"
	"sync"
	"time"

	"gitlab.com/tozd/go/errors"
)

var textRecorderContextKey = &contextKey{"text-provider-recorder"} //nolint:gochecknoglobals

// TextRecorderUsedTokens describes number of tokens used by a request
// to an AI model.
type TextRecorderUsedTokens struct {
	// MaxTotal is the maximum total number of tokens allowed to be used
	// with the underlying AI model (i.e., the maximum context window).
	MaxTotal int `json:"maxTotal"`

	// MaxResponse is the maximum number of tokens allowed to be used in
	// a response with the underlying AI model.
	MaxResponse int `json:"maxResponse"`

	// Prompt is the number of tokens used by the prompt (including the system
	// prompt and all example inputs with corresponding outputs).
	Prompt int `json:"prompt"`

	// Response is the number of tokens used by the response.
	Response int `json:"response"`

	// Total is the sum of Prompt and Response.
	Total int `json:"total"`
}

// TextRecorderUsedTokens describes time taken by a request to an AI model.
type TextRecorderUsedTime struct {
	// Prompt is time taken by processing the prompt.
	Prompt time.Duration `json:"prompt"`

	// Response is time taken by formulating the response.
	Response time.Duration `json:"response"`

	// Total is the sum of Prompt and Response.
	Total time.Duration `json:"total"`
}

type TextRecorderCall struct {
	ID       string       `json:"id"`
	Type     string       `json:"type"`
	Provider TextProvider `json:"provider"`
	Messages []any        `json:"messages,omitempty"`

	// UsedTokens
	UsedTokens map[string]TextRecorderUsedTokens `json:"usedTokens,omitempty"`
	UsedTime   map[string]TextRecorderUsedTime   `json:"usedTime,omitempty"`
}

type TextRecorderMessage map[string]string

func (c *TextRecorderCall) addMessage(role, message string, params ...string) {
	m := TextRecorderMessage{
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

func (c *TextRecorderCall) addCall(call TextRecorderCall) {
	c.Messages = append(c.Messages, call)
}

func (c *TextRecorderCall) addUsedTokens(requestID string, maxTotal, maxResponse, prompt, response int) {
	if c.UsedTokens == nil {
		c.UsedTokens = map[string]TextRecorderUsedTokens{}
	}

	c.UsedTokens[requestID] = TextRecorderUsedTokens{
		MaxTotal:    maxTotal,
		MaxResponse: maxResponse,
		Prompt:      prompt,
		Response:    response,
		Total:       prompt + response,
	}
}

func (c *TextRecorderCall) addUsedTime(requestID string, prompt, response time.Duration) {
	if c.UsedTime == nil {
		c.UsedTime = map[string]TextRecorderUsedTime{}
	}

	c.UsedTime[requestID] = TextRecorderUsedTime{
		Prompt:   prompt,
		Response: response,
		Total:    prompt + response,
	}
}

// TextRecorderCall is a recorder which records all communication
// with the AI model and track usage.
//
// It can be used to record multiple calls, but it is suggested that
// create a new instance with [WithTextRecorder] for every top-level call.
//
// It supports recording recursive calls (e.g., a tool call which calls into
// an AI model again).
type TextRecorder struct {
	mu sync.Mutex

	stack         []*TextRecorderCall
	topLevelCalls []TextRecorderCall
}

func (t *TextRecorder) pushCall(id string, provider TextProvider) {
	t.mu.Lock()
	defer t.mu.Unlock()

	call := &TextRecorderCall{
		ID:         id,
		Type:       "call",
		Provider:   provider,
		Messages:   nil,
		UsedTokens: nil,
		UsedTime:   nil,
	}

	t.stack = append(t.stack, call)
}

func (t *TextRecorder) popCall() {
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

// TextRecorderCall returns top-level call records recorded by this recorder.
//
// In most cases this will be just one call record unless you are reusing
// same context across multiple calls.
func (t *TextRecorder) Calls() []TextRecorderCall {
	t.mu.Lock()
	defer t.mu.Unlock()

	return t.topLevelCalls
}

func (t *TextRecorder) addMessage(role, message string, params ...string) {
	t.mu.Lock()
	defer t.mu.Unlock()

	t.stack[len(t.stack)-1].addMessage(role, message, params...)
}

func (t *TextRecorder) addUsedTokens(requestID string, maxTotal, maxResponse, prompt, response int) {
	t.mu.Lock()
	defer t.mu.Unlock()

	t.stack[len(t.stack)-1].addUsedTokens(requestID, maxTotal, maxResponse, prompt, response)
}

func (t *TextRecorder) addUsedTime(requestID string, prompt, response time.Duration) {
	t.mu.Lock()
	defer t.mu.Unlock()

	t.stack[len(t.stack)-1].addUsedTime(requestID, prompt, response)
}

// WithTextRecorder returns a copy of the context in which an instance
// of [TextRecorder] is stored.
//
// Passing such context to [Text.Call] allows you to record all communication
// with the AI model and track usage.
func WithTextRecorder(ctx context.Context) context.Context {
	return context.WithValue(ctx, textRecorderContextKey, new(TextRecorder))
}

// GetTextRecorder returns the instance of [TextRecorder] stored in the context,
// if any.
func GetTextRecorder(ctx context.Context) *TextRecorder {
	provider, ok := ctx.Value(textRecorderContextKey).(*TextRecorder)
	if !ok {
		return nil
	}
	return provider
}
