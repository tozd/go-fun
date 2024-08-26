package fun

import (
	"context"
	"maps"
	"slices"
	"strconv"
	"sync"
	"time"
)

var textRecorderContextKey = &contextKey{"text-provider-recorder"} //nolint:gochecknoglobals

// Duration is [time.Duration] but which formats duration as
// seconds with millisecond precision in JSON.
type Duration time.Duration

func (d Duration) MarshalJSON() ([]byte, error) {
	return []byte(strconv.FormatFloat(time.Duration(d).Seconds(), byte('f'), 3, 64)), nil
}

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

	// CacheCreationInputTokens is the number of tokens written
	// to the cache when creating a new entry.
	CacheCreationInputTokens *int `json:"cacheCreationInputTokens,omitempty"`

	// CacheReadInputTokens is the number of tokens retrieved
	// from the cache for this request.
	CacheReadInputTokens *int `json:"cacheReadInputTokens,omitempty"`
}

// TextRecorderUsedTime describes time taken by a request to an AI model.
type TextRecorderUsedTime struct {
	// Prompt is time taken by processing the prompt.
	Prompt Duration `json:"prompt,omitempty"`

	// Response is time taken by formulating the response.
	Response Duration `json:"response,omitempty"`

	// Total is the sum of Prompt and Response.
	Total Duration `json:"total,omitempty"`

	// APICall is end-to-end duration of the API call request.
	APICall Duration `json:"apiCall"`
}

type TextRecorderNotification struct {
	Stack []string `json:"stack"`

	Message TextRecorderMessage `json:"message"`
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

	// Messages sent to and received from the AI model. Note that
	// these messages might have been sent and received multiple times
	// in multiple requests made (e.g., when using tools).
	Messages []TextRecorderMessage `json:"messages,omitempty"`

	// UsedTokens for each request made to the AI model.
	UsedTokens map[string]TextRecorderUsedTokens `json:"usedTokens,omitempty"`

	// UsedTime for each request made to the AI model.
	UsedTime map[string]TextRecorderUsedTime `json:"usedTime,omitempty"`

	// Duration is end-to-end duration of this call.
	Duration Duration `json:"duration,omitempty"`

	recorder *TextRecorder
	start    time.Time
}

// TextRecorderMessage describes one message sent to or received
// from the AI model.
type TextRecorderMessage struct {
	// Role of the message. Possible values are "system",
	// "assistant", "user", "tool_use", and "tool_result".
	Role string `json:"role"`

	// Content is textual content of the message.
	Content *string `json:"content,omitempty"`

	// ToolUseID is the ID of the tool use to correlate
	// "tool_use" and "tool_result" messages.
	ToolUseID string `json:"toolUseId,omitempty"`

	// ToolUseName is the name of the tool to use.
	ToolUseName string `json:"toolUseName,omitempty"`

	// ToolDuration is duration of the tool call.
	ToolDuration Duration `json:"toolDuration,omitempty"`

	// ToolCalls contains any recursive calls recorded while running the tool.
	ToolCalls []TextRecorderCall `json:"toolCalls,omitempty"`

	// IsError is true if there was an error during tool execution.
	// In this case, Content is the error message returned to the AI model.
	IsError bool `json:"isError,omitempty"`

	// IsRefusal is true if the AI model refused to respond.
	// In this case, Content is the explanation of the refusal.
	IsRefusal bool `json:"isRefusal,omitempty"`

	start time.Time
}

func (m *TextRecorderMessage) snapshot(final bool) TextRecorderMessage {
	var duration Duration
	if !m.start.IsZero() {
		duration = Duration(time.Since(m.start))
	} else {
		duration = m.ToolDuration
	}

	var toolCalls []TextRecorderCall
	if m.ToolCalls != nil {
		toolCalls = make([]TextRecorderCall, 0, len(m.ToolCalls))
		for _, toolCall := range m.ToolCalls {
			toolCalls = append(toolCalls, toolCall.snapshot(final))
		}
	}

	var start time.Time
	if !final {
		start = m.start
	}

	return TextRecorderMessage{
		Role:         m.Role,
		Content:      m.Content,
		ToolUseID:    m.ToolUseID,
		ToolUseName:  m.ToolUseName,
		ToolDuration: duration,
		ToolCalls:    toolCalls,
		IsError:      m.IsError,
		IsRefusal:    m.IsRefusal,
		start:        start,
	}
}

func (c *TextRecorderCall) snapshot(final bool) TextRecorderCall {
	var duration Duration
	if !c.start.IsZero() {
		duration = Duration(time.Since(c.start))
	} else {
		duration = c.Duration
	}

	var messages []TextRecorderMessage
	if c.Messages != nil {
		messages = make([]TextRecorderMessage, 0, len(c.Messages))
		for _, message := range c.Messages {
			messages = append(messages, message.snapshot(final))
		}
	}

	var start time.Time
	if !final {
		start = c.start
	}

	return TextRecorderCall{
		ID:         c.ID,
		Provider:   c.Provider,
		Messages:   messages,
		UsedTokens: maps.Clone(c.UsedTokens),
		UsedTime:   maps.Clone(c.UsedTime),
		Duration:   duration,
		recorder:   nil,
		start:      start,
	}
}

func (c *TextRecorderCall) addMessage(role, content, toolID, toolName string, toolDuration Duration, toolCalls []TextRecorderCall, isError, isRefusal bool) {
	c.Messages = append(c.Messages, TextRecorderMessage{
		Role:         role,
		Content:      &content,
		ToolUseID:    toolID,
		ToolUseName:  toolName,
		ToolDuration: toolDuration,
		ToolCalls:    toolCalls,
		IsError:      isError,
		IsRefusal:    isRefusal,
		start:        time.Time{},
	})
}

func (c *TextRecorderCall) addUsedTokens(requestID string, maxTotal, maxResponse, prompt, response int, cacheCreationInputTokens, cacheReadInputTokens *int) {
	if c.UsedTokens == nil {
		c.UsedTokens = map[string]TextRecorderUsedTokens{}
	}

	c.UsedTokens[requestID] = TextRecorderUsedTokens{
		MaxTotal:                 maxTotal,
		MaxResponse:              maxResponse,
		Prompt:                   prompt,
		Response:                 response,
		Total:                    prompt + response,
		CacheCreationInputTokens: cacheCreationInputTokens,
		CacheReadInputTokens:     cacheReadInputTokens,
	}
}

func (c *TextRecorderCall) addUsedTime(requestID string, prompt, response, apiCall time.Duration) {
	if c.UsedTime == nil {
		c.UsedTime = map[string]TextRecorderUsedTime{}
	}

	c.UsedTime[requestID] = TextRecorderUsedTime{
		Prompt:   Duration(prompt),
		Response: Duration(response),
		Total:    Duration(prompt + response),
		APICall:  Duration(apiCall),
	}
}

func (c *TextRecorderCall) notify() {
	c.recorder.mu.Lock()
	defer c.recorder.mu.Unlock()

	if c.recorder.c == nil {
		return
	}

	calls := slices.Clone(c.recorder.calls)
	calls = append(calls, c.snapshot(false))

	c.recorder.c <- calls
}

func (c *TextRecorderCall) startToolMessage(ctx context.Context, toolCallID string) (context.Context, *TextRecorderMessage) {
	c.recorder.mu.Lock()
	defer c.recorder.mu.Unlock()

	c.Messages = append(c.Messages, TextRecorderMessage{
		Role:         roleToolResult,
		Content:      nil,
		ToolUseID:    toolCallID,
		ToolUseName:  "",
		ToolDuration: 0,
		ToolCalls:    nil,
		IsError:      false,
		IsRefusal:    false,
		start:        time.Now(),
	})

	return context.WithValue(ctx, textRecorderContextKey, &TextRecorder{
		mu:     sync.Mutex{},
		calls:  nil,
		parent: c.recorder,
		c:      nil,
	}), &c.Messages[len(c.Messages)-1]
}

// TextRecorderCall is a recorder which records all communication
// with the AI model and track usage.
//
// It can be used to record multiple calls and it can be used concurrently,
// but it is suggested that you create a new instance with [WithTextRecorder]
// for every call.
type TextRecorder struct {
	mu sync.Mutex

	calls  []TextRecorderCall
	parent *TextRecorder
	c      chan<- []TextRecorderCall
}

func (t *TextRecorder) newCall(callID string, provider TextProvider) *TextRecorderCall {
	t.mu.Lock()
	defer t.mu.Unlock()

	return &TextRecorderCall{
		ID:         callID,
		Provider:   provider,
		Messages:   nil,
		UsedTokens: nil,
		UsedTime:   nil,
		Duration:   0,
		recorder:   t,
		start:      time.Now(),
	}
}

func (t *TextRecorder) recordCall(call *TextRecorderCall) {
	t.mu.Lock()
	defer t.mu.Unlock()

	t.calls = append(t.calls, call.snapshot(true))
}

// Notify sets a channel which is used to send all recorded calls
// (at current, possibly incomplete, state) anytime any of them changes.
func (t *TextRecorder) Notify(c chan<- []TextRecorderCall) {
	t.mu.Lock()
	defer t.mu.Unlock()

	t.c = c
}

// TextRecorderCall returns calls records recorded by this recorder.
//
// It returns only completed calls. If you need access to calls while
// they are being recorded, use [Notify].
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
