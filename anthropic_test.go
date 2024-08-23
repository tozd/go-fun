package fun_test

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"gitlab.com/tozd/go/x"

	"gitlab.com/tozd/go/fun"
)

func TestAnthropicJSON(t *testing.T) {
	t.Parallel()

	provider := fun.AnthropicTextProvider{
		Client:        nil,
		APIKey:        "xxx",
		Model:         "claude-3-haiku-20240307",
		PromptCaching: true,
		Temperature:   0.7,
	}

	out, errE := x.MarshalWithoutEscapeHTML(provider)
	require.NoError(t, errE, "% -+#.1v", errE)
	assert.Equal(t, `{"type":"anthropic","model":"claude-3-haiku-20240307","promptCaching":true,"temperature":0.7}`, string(out))
}
