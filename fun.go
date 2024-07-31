package fun

import (
	"context"

	"gitlab.com/tozd/go/errors"
)

type Callee[Input, Output any] interface {
	Init(ctx context.Context) errors.E
	Call(ctx context.Context, input ...Input) (Output, errors.E)
}

type ChatMessage struct {
	Role    string `json:"role"`
	Content string `json:"content"`
}

type TextProvider interface {
	Init(ctx context.Context, messages []ChatMessage) errors.E
	Chat(ctx context.Context, message ChatMessage) (string, errors.E)
}
