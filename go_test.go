package fun_test

import (
	"context"
	"testing"

	"github.com/rs/zerolog"
	"github.com/stretchr/testify/assert"
	"gitlab.com/tozd/go/errors"

	"gitlab.com/tozd/go/fun"
)

func TestGo(t *testing.T) {
	t.Parallel()

	f := fun.Go[string, string]{
		Fun: func(_ context.Context, input ...string) (string, errors.E) {
			return input[0] + input[0], nil
		},
	}

	ctx := zerolog.New(zerolog.NewTestWriter(t)).WithContext(context.Background())

	errE := f.Init(ctx)
	assert.NoError(t, errE, "% -+#.1v", errE)

	output, errE := f.Call(ctx, "foo")
	assert.NoError(t, errE, "% -+#.1v", errE)
	assert.Equal(t, "foofoo", output)

	output, errE = f.Variadic()(ctx, "foo")
	assert.NoError(t, errE, "% -+#.1v", errE)
	assert.Equal(t, "foofoo", output)

	output, errE = f.Unary()(ctx, "foo")
	assert.NoError(t, errE, "% -+#.1v", errE)
	assert.Equal(t, "foofoo", output)
}
