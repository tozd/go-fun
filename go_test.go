package fun_test

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
	"gitlab.com/tozd/go/errors"
	"gitlab.com/tozd/go/fun"
)

func TestGo(t *testing.T) {
	f := fun.Go[string, string]{
		Fun: func(ctx context.Context, input string) (string, errors.E) {
			return input + input, nil
		},
	}

	ctx := context.Background()

	errE := f.Init(ctx)
	assert.NoError(t, errE, "% -+#.1v", errE)

	output, errE := f.Call(ctx, "foo")
	assert.NoError(t, errE, "% -+#.1v", errE)
	assert.Equal(t, "foofoo", output)
}
