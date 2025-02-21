package fun_test

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"gitlab.com/tozd/go/x"

	"gitlab.com/tozd/go/fun"
)

func TestOllamaJSON(t *testing.T) {
	t.Parallel()

	provider := fun.OllamaTextProvider{
		Client: nil,
		Base:   "xxx",
		Model:  "llama3:8b",
		ModelAccess: fun.OllamaModelAccess{
			Insecure: true,
			Username: "yyy",
			Password: "zzz",
		},
		MaxContextLength:  43,
		MaxResponseLength: 56,
		Seed:              42,
		Temperature:       0.7,
	}

	out, errE := x.MarshalWithoutEscapeHTML(provider)
	require.NoError(t, errE, "% -+#.1v", errE)
	assert.Equal(t, `{"type":"ollama","model":"llama3:8b","maxContextLength":43,"maxResponseLength":56,"seed":42,"temperature":0.7}`, string(out)) //nolint:testifylint
}
