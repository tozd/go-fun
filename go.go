package fun

import (
	"context"

	"gitlab.com/tozd/go/errors"
)

var _ Callee[any, any] = (*Go[any, any])(nil)

// Go implements [Callee] interface with its logic defined by Go function.
type Go[Input, Output any] struct {
	// Fun implements the logic.
	Fun func(ctx context.Context, input ...Input) (Output, errors.E)
}

// Init implements [Callee] interface.
func (*Go[Input, Output]) Init(ctx context.Context) errors.E {
	return nil
}

// Call implements [Callee] interface.
func (f *Go[Input, Output]) Call(ctx context.Context, input ...Input) (Output, errors.E) {
	return f.Fun(ctx, input...)
}
