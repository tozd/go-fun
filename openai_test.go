package fun_test

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"gitlab.com/tozd/go/x"

	"gitlab.com/tozd/go/fun"
)

func TestOpenAIJSON(t *testing.T) {
	t.Parallel()

	provider := fun.OpenAITextProvider{
		Client:                nil,
		APIKey:                "xxx",
		Model:                 "gpt-4o-mini-2024-07-18",
		MaxContextLength:      43,
		MaxResponseLength:     56,
		ForceOutputJSONSchema: false,
		Seed:                  42,
		Temperature:           0.7,
	}

	out, errE := x.MarshalWithoutEscapeHTML(provider)
	require.NoError(t, errE, "% -+#.1v", errE)
	assert.Equal(t, `{"type":"openai","model":"gpt-4o-mini-2024-07-18","maxContextLength":43,"maxResponseLength":56,"forceOutputJsonSchema":false,"seed":42,"temperature":0.7}`, string(out)) //nolint:testifylint
}
