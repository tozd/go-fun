package fun

import (
	"context"
	"sync"
	"time"
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

// TextRecorderCall describes a call to an AI model.
//
// There might be multiple requests made to an AI model
// during a call (e.g., when using tools).
type TextRecorderCall struct {
	// ID is a random ID assigned to this call so that it is
	// possible to correlate the call with logging.
	ID string `json:"id"`

	// Provider for this call.
	Provider TextProvider `json:"provider"`

	Messages []TextRecorderMessage `json:"messages,omitempty"`

	// UsedTokens for each request made to an AI model.
	UsedTokens map[string]TextRecorderUsedTokens `json:"usedTokens,omitempty"`

	// UsedTime for each request made to an AI model.
	UsedTime map[string]TextRecorderUsedTime `json:"usedTime,omitempty"`
}

type TextRecorderMessage struct {
	Role        string             `json:"role"`
	Message     string             `json:"message"`
	ToolUseID   string             `json:"toolUseId,omitempty"`
	ToolUseName string             `json:"toolUseName,omitempty"`
	IsError     bool               `json:"isError,omitempty"`
	IsRefusal   bool               `json:"isRefusal,omitempty"`
	Calls       []TextRecorderCall `json:"calls,omitempty"`
}

func (c *TextRecorderCall) addMessage(role, message, toolID, toolName string, isError, isRefusal bool, calls []TextRecorderCall) {
	c.Messages = append(c.Messages, TextRecorderMessage{
		Role:        role,
		Message:     message,
		ToolUseID:   toolID,
		ToolUseName: toolName,
		IsError:     isError,
		IsRefusal:   isRefusal,
		Calls:       calls,
	})
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
// you create a new instance with [WithTextRecorder] for every call.
type TextRecorder struct {
	mu sync.Mutex

	calls []TextRecorderCall
}

func (t *TextRecorder) recordCall(call *TextRecorderCall) {
	t.mu.Lock()
	defer t.mu.Unlock()

	t.calls = append(t.calls, *call)
}

// TextRecorderCall returns calls records recorded by this recorder.
//
// In most cases this will be just one call record unless you are reusing
// same context across multiple calls.
func (t *TextRecorder) Calls() []TextRecorderCall {
	if t == nil {
		return nil
	}

	t.mu.Lock()
	defer t.mu.Unlock()

	return t.calls
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
