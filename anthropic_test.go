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
		Client:            nil,
		APIKey:            "xxx",
		Model:             "claude-3-haiku-20240307",
		MaxContextLength:  43,
		MaxResponseLength: 56,
		MaxExchanges:      57,
		PromptCaching:     true,
		ReasoningBudget:   12345,
		Temperature:       0.7,
	}

	out, errE := x.MarshalWithoutEscapeHTML(provider)
	require.NoError(t, errE, "% -+#.1v", errE)
	assert.Equal(t, `{"model":"claude-3-haiku-20240307","maxContextLength":43,"maxResponseLength":56,"maxExchanges":57,"promptCaching":true,"reasoningBudget":12345,"temperature":0.7,"type":"anthropic"}`, string(out)) //nolint:testifylint
}
