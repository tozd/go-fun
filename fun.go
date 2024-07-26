package fun

import (
	"context"

	"gitlab.com/tozd/go/errors"
)

type Callee[Input, Output any] interface {
	Init(ctx context.Context) errors.E
	Call(ctx context.Context, input Input) (Output, errors.E)
}
