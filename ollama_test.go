package fun_test

import (
	"context"
	"fmt"
	"os"
	"testing"

	"github.com/stretchr/testify/assert"
	"gitlab.com/tozd/go/fun"
)

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
				{"zzz", "zzzzzz"},
			},
		},
		{
			"",
			[]fun.InputOutput[string, string]{
				{"abc", "abcabc"},
				{"ddd", "dddddd"},
				{"cba", "cbacba"},
				{"zoo", "zoozoo"},
				{"roar", "roarroar"},
				{"lsdfk", "lsdfklsdfk"},
			},
			[]TestCase{
				{"foo", "foofoo"},
				{"bar", "barbar"},
				{"zzz", "zzzzzz"},
			},
		},
	}

	for i, tt := range tests {
		t.Run(fmt.Sprintf("t=%d", i), func(t *testing.T) {
			f := fun.Ollama[string, string]{
				Client: nil,
				Base:   base,
				Model: fun.OllamaModel{
					Model:    "llama3:instruct",
					Insecure: false,
					Username: "",
					Password: "",
				},
				InputJSONSchema:  nil,
				OutputJSONSchema: nil,
				Prompt:           tt.Prompt,
				Data:             tt.Data,
				Seed:             42,
				Temperature:      0,
			}

			ctx := context.Background()

			errE := f.Init(ctx)
			assert.NoError(t, errE, "% -+#.1v", errE)

			for j, c := range tt.Cases {
				t.Run(fmt.Sprintf("c=%d", j), func(t *testing.T) {
					output, errE := f.Call(ctx, c.Input)
					assert.NoError(t, errE, "% -+#.1v", errE)
					assert.Equal(t, c.Output, output)
				})
			}
		})
	}
}
