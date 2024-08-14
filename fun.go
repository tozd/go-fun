// Package fun provides high-level abstraction to define functions with code (the usual way), data
// (providing examples of inputs and expected outputs which are then used with an AI model),
// or natural language description.
package fun

import (
	"context"

	"gitlab.com/tozd/go/errors"
)

// Callee is a high-level function abstraction to unify functions defined in different ways.
type Callee[Input, Output any] interface {
	Init(ctx context.Context) errors.E
	Call(ctx context.Context, input ...Input) (Output, errors.E)
}

// ChatMessage is a message struct for TextProvider.
type ChatMessage struct {
	Role    string `json:"role"`
	Content string `json:"content"`
}

// TextProvider is a provider for text-based LLMs.
type TextProvider interface {
	Init(ctx context.Context, messages []ChatMessage) errors.E
	Chat(ctx context.Context, message ChatMessage) (string, errors.E)
}

// WithOutputJSONSchema is a provider which supports setting JSON Schema for its output.
type WithOutputJSONSchema interface {
	InitOutputJSONSchema(ctx context.Context, schema []byte) errors.E
}
