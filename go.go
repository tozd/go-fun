package fun

import (
	"context"

	"github.com/rs/zerolog"
	"gitlab.com/tozd/go/errors"
	"gitlab.com/tozd/identifier"
)

var _ Callee[any, any] = (*Go[any, any])(nil)

// Go implements [Callee] interface with its logic defined by Go function.
type Go[Input, Output any] struct {
	// Fun implements the logic.
	Fun func(ctx context.Context, input ...Input) (Output, errors.E)
}

// Init implements [Callee] interface.
func (*Go[Input, Output]) Init(_ context.Context) errors.E {
	return nil
}

// Call implements [Callee] interface.
func (f *Go[Input, Output]) Call(ctx context.Context, input ...Input) (Output, errors.E) { //nolint:ireturn
	logger := zerolog.Ctx(ctx).With().Str("fun", identifier.New().String()).Logger()
	ctx = logger.WithContext(ctx)

	return f.Fun(ctx, input...)
}

// Variadic implements [Callee] interface.
func (f *Go[Input, Output]) Variadic() func(ctx context.Context, input ...Input) (Output, errors.E) {
	return func(ctx context.Context, input ...Input) (Output, errors.E) {
		return f.Call(ctx, input...)
	}
}

// Unary implements [Callee] interface.
func (f *Go[Input, Output]) Unary() func(ctx context.Context, input Input) (Output, errors.E) {
	return func(ctx context.Context, input Input) (Output, errors.E) {
		return f.Call(ctx, input)
	}
}
