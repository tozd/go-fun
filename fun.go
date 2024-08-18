// Package fun provides high-level abstraction to define functions with code (the usual way),
// data (providing examples of inputs and expected outputs which are then used with an AI model),
// or natural language description.
// It is the simplest but powerful way to use large language models (LLMs) in Go.
package fun

import (
	"context"

	"gitlab.com/tozd/go/errors"
)

// Callee is a high-level function abstraction to unify functions defined in different ways.
type Callee[Input, Output any] interface {
	// Init initializes the callee.
	Init(ctx context.Context) errors.E

	// Call calls the callee with provided inputs and returns the output.
	Call(ctx context.Context, input ...Input) (Output, errors.E)

	// Variadic returns a Go function which takes variadic inputs
	// and returns the output as defined by the callee.
	Variadic() func(ctx context.Context, input ...Input) (Output, errors.E)

	// Variadic returns a Go function which takes one input
	// and returns the output as defined by the callee.
	Unary() func(ctx context.Context, input Input) (Output, errors.E)
}

// ChatMessage is a message struct for TextProvider.
type ChatMessage struct {
	// Role of the message.
	Role string `json:"role"`

	// Content is textual content of the message.
	Content string `json:"content"`
}

// TextProvider is a provider for text-based LLMs.
type TextProvider interface {
	// Init initializes text provider with optional messages which
	// are at every Chat call prepended to its message. These messages
	// can include system prompt and prior conversation with the AI model.
	Init(ctx context.Context, messages []ChatMessage) errors.E

	// Chat sends a message to the AI model and returns its response.
	Chat(ctx context.Context, message ChatMessage) (string, errors.E)
}

// WithOutputJSONSchema is a [TextProvider] which supports setting JSON Schema for its output.
type WithOutputJSONSchema interface {
	// InitOutputJSONSchema provides the JSON Schema the provider
	// should request the AI model to use for its output.
	InitOutputJSONSchema(ctx context.Context, schema []byte) errors.E
}

// WithTools is a [TextProvider] which supports tools.
type WithTools interface {
	// InitTools initializes the tool with available tools.
	InitTools(ctx context.Context, tools map[string]TextTooler) errors.E
}
