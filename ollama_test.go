package fun_test

import (
	"context"
	"fmt"
	"os"
	"testing"

	"github.com/stretchr/testify/assert"
	"gitlab.com/tozd/go/fun"
)

var jsonSchemaString = []byte(`{"type": "string"}`)

type TestCase struct {
	Input  string
	Output string
}

func TestOllama(t *testing.T) {
	base := os.Getenv("OLLAMA_HOST")
	if base == "" {
		t.Skip("OLLAMA_HOST is not available")
	}

	tests := []struct {
		Prompt string
		Data   []fun.InputOutput[string, string]
		Cases  []TestCase
	}{
		{
			"Repeat the input twice, by concatenating the input string without any space. Return just the result.",
			nil,
			[]TestCase{
				{"foo", "foofoo"},
				{"bar", "barbar"},
				{"test", "testtest"},
			},
		},
		{
			"",
			[]fun.InputOutput[string, string]{
				{"abc", "abcabc"},
				{"ddd", "dddddd"},
				{"cba", "cbacba"},
				{"zoo", "zoozoo"},
				{"AbC", "AbCAbC"},
				{"roar", "roarroar"},
				{"lsdfk", "lsdfklsdfk"},
				{"ZZZZ", "ZZZZZZZZ"},
				{"long", "longlong"},
			},
			[]TestCase{
				{"foo", "foofoo"},
				{"bar", "barbar"},
				{"test", "testtest"},
			},
		},
		{
			"Repeat the input twice, by concatenating the input string without any space. Return just the result.",
			[]fun.InputOutput[string, string]{
				{"abc", "abcabc"},
				{"ddd", "dddddd"},
				{"cba", "cbacba"},
				{"zoo", "zoozoo"},
				{"AbC", "AbCAbC"},
				{"roar", "roarroar"},
				{"lsdfk", "lsdfklsdfk"},
				{"ZZZZ", "ZZZZZZZZ"},
				{"long", "longlong"},
			},
			[]TestCase{
				{"foo", "foofoo"},
				{"bar", "barbar"},
				{"test", "testtest"},
			},
		},
	}

	for i, tt := range tests {
		t.Run(fmt.Sprintf("i=%d", i), func(t *testing.T) {
			f := fun.Ollama[string, string]{
				Client: nil,
				Base:   base,
				Model: fun.OllamaModel{
					Model:    "llama3:8b",
					Insecure: false,
					Username: "",
					Password: "",
				},
				InputJSONSchema:  jsonSchemaString,
				OutputJSONSchema: jsonSchemaString,
				Prompt:           tt.Prompt,
				Data:             tt.Data,
				Seed:             42,
				Temperature:      0.1,
			}

			ctx := context.Background()

			errE := f.Init(ctx)
			assert.NoError(t, errE, "% -+#.1v", errE)

			for _, c := range tt.Cases {
				t.Run(fmt.Sprintf("input=%s", c.Input), func(t *testing.T) {
					output, errE := f.Call(ctx, c.Input)
					assert.NoError(t, errE, "% -+#.1v", errE)
					assert.Equal(t, c.Output, output)
				})
			}
		})
	}
}
