package fun_test

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"gitlab.com/tozd/go/x"

	"gitlab.com/tozd/go/fun"
)

func TestGroqJSON(t *testing.T) {
	t.Parallel()

	provider := fun.GroqTextProvider{
		Client:                 nil,
		APIKey:                 "xxx",
		Model:                  "openai/gpt-oss-20b",
		RequestsPerMinuteLimit: 41,
		MaxContextLength:       43,
		MaxResponseLength:      56,
		MaxExchanges:           57,
		Seed:                   42,
		Temperature:            0.7,
	}

	out, errE := x.MarshalWithoutEscapeHTML(provider)
	require.NoError(t, errE, "% -+#.1v", errE)
	assert.Equal(t, `{"model":"openai/gpt-oss-20b","requestsPerMinuteLimit":41,"maxContextLength":43,"maxResponseLength":56,"maxExchanges":57,"seed":42,"temperature":0.7,"type":"groq"}`, string(out)) //nolint:testifylint
}
