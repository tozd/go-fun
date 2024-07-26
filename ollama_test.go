package fun_test

import (
	"context"
	"fmt"
	"os"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"gitlab.com/tozd/go/fun"
)

var jsonSchemaString = []byte(`{"type": "string"}`)

type OutputStruct struct {
	Key      string         `json:"key"`
	Value    int            `json:"value"`
	Children []OutputStruct `json:"children,omitempty"`
}

func TestOllama(t *testing.T) {
	base := os.Getenv("OLLAMA_HOST")
	if base == "" {
		t.Skip("OLLAMA_HOST is not available")
	}

	tests := []struct {
		Prompt string
		Data   []fun.InputOutput[string, string]
		Cases  []fun.InputOutput[string, string]
	}{
		{
			"Repeat the input twice, by concatenating the input string without any space. Return just the result.",
			nil,
			[]fun.InputOutput[string, string]{
				{"foo", "foofoo"},
				{"bar", "barbar"},
				{"test", "testtest"},
			},
		},
		{
			"",
			[]fun.InputOutput[string, string]{
				// We repeat some training data to reinforce those cases.
				// (Otherwise they fail when we test training cases.)
				{"abc", "abcabc"},
				{"ddd", "dddddd"},
				{"cba", "cbacba"},
				{"zoo", "zoozoo"},
				{"zoo", "zoozoo"},
				{"zoo", "zoozoo"},
				{"zoo", "zoozoo"},
				{"zoo", "zoozoo"},
				{"zoo", "zoozoo"},
				{"AbC", "AbCAbC"},
				{"roar", "roarroar"},
				{"roar", "roarroar"},
				{"lsdfk", "lsdfklsdfk"},
				{"ZZZZ", "ZZZZZZZZ"},
				{"ZZZZ", "ZZZZZZZZ"},
				{"long", "longlong"},
			},
			[]fun.InputOutput[string, string]{
				{"foo", "foofoo"},
				{"bar", "barbar"},
				{"test", "testtest"},
			},
		},
		{
			"Repeat the input twice, by concatenating the input string without any space. Return just the result.",
			[]fun.InputOutput[string, string]{
				// We repeat some training data to reinforce those cases.
				// (Otherwise they fail when we test training cases.)
				{"abc", "abcabc"},
				{"ddd", "dddddd"},
				{"cba", "cbacba"},
				{"zoo", "zoozoo"},
				{"zoo", "zoozoo"},
				{"zoo", "zoozoo"},
				{"zoo", "zoozoo"},
				{"zoo", "zoozoo"},
				{"zoo", "zoozoo"},
				{"AbC", "AbCAbC"},
				{"roar", "roarroar"},
				{"roar", "roarroar"},
				{"lsdfk", "lsdfklsdfk"},
				{"ZZZZ", "ZZZZZZZZ"},
				{"ZZZZ", "ZZZZZZZZ"},
				{"long", "longlong"},
			},
			[]fun.InputOutput[string, string]{
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
				Temperature:      0,
			}

			ctx := context.Background()

			errE := f.Init(ctx)
			require.NoError(t, errE, "% -+#.1v", errE)

			for _, d := range tt.Data {
				t.Run(fmt.Sprintf("input=%s", d.Input), func(t *testing.T) {
					output, errE := f.Call(ctx, d.Input)
					assert.NoError(t, errE, "% -+#.1v", errE)
					assert.Equal(t, d.Output, output)
				})
			}

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

func TestOllamaStruct(t *testing.T) {
	base := os.Getenv("OLLAMA_HOST")
	if base == "" {
		t.Skip("OLLAMA_HOST is not available")
	}

	tests := []struct {
		Prompt string
		Data   []fun.InputOutput[string, OutputStruct]
		Cases  []fun.InputOutput[string, OutputStruct]
	}{
		{
			"",
			[]fun.InputOutput[string, OutputStruct]{
				{"foo=1", OutputStruct{Key: "foo", Value: 1}},
				{"bar=3", OutputStruct{Key: "bar", Value: 3}},
				{"foo=1 [bar=3]", OutputStruct{Key: "foo", Value: 1, Children: []OutputStruct{{Key: "bar", Value: 3}}}},
				{"foo=1 [bar=3 zoo=2]", OutputStruct{Key: "foo", Value: 1, Children: []OutputStruct{{Key: "bar", Value: 3}, {Key: "zoo", Value: 2}}}},
			},
			[]fun.InputOutput[string, OutputStruct]{
				{"name=42 [first=2 second=1]", OutputStruct{Key: "name", Value: 42, Children: []OutputStruct{{Key: "first", Value: 2}, {Key: "second", Value: 1}}}},
			},
		},
		{
			fun.StringToJSONPrompt,
			[]fun.InputOutput[string, OutputStruct]{
				{"foo=1", OutputStruct{Key: "foo", Value: 1}},
				{"bar=3", OutputStruct{Key: "bar", Value: 3}},
				{"foo=1 [bar=3]", OutputStruct{Key: "foo", Value: 1, Children: []OutputStruct{{Key: "bar", Value: 3}}}},
				{"foo=1 [bar=3 zoo=2]", OutputStruct{Key: "foo", Value: 1, Children: []OutputStruct{{Key: "bar", Value: 3}, {Key: "zoo", Value: 2}}}},
			},
			[]fun.InputOutput[string, OutputStruct]{
				{"name=42 [first=2 second=1]", OutputStruct{Key: "name", Value: 42, Children: []OutputStruct{{Key: "first", Value: 2}, {Key: "second", Value: 1}}}},
			},
		},
	}

	for i, tt := range tests {
		t.Run(fmt.Sprintf("i=%d", i), func(t *testing.T) {
			f := fun.Ollama[string, OutputStruct]{
				Client: nil,
				Base:   base,
				Model: fun.OllamaModel{
					Model:    "llama3:8b",
					Insecure: false,
					Username: "",
					Password: "",
				},
				InputJSONSchema:  jsonSchemaString,
				OutputJSONSchema: nil,
				Prompt:           tt.Prompt,
				Data:             tt.Data,
				Seed:             42,
				Temperature:      0,
			}

			ctx := context.Background()

			errE := f.Init(ctx)
			require.NoError(t, errE, "% -+#.1v", errE)

			for _, d := range tt.Data {
				t.Run(fmt.Sprintf("input=%s", d.Input), func(t *testing.T) {
					output, errE := f.Call(ctx, d.Input)
					assert.NoError(t, errE, "% -+#.1v", errE)
					assert.Equal(t, d.Output, output)
				})
			}

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
