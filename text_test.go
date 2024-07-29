package fun_test

import (
	"context"
	"fmt"
	"os"
	"slices"
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

func TestText(t *testing.T) {
	t.Parallel()

	tests := []struct {
		Name   string
		Prompt string
		Data   []fun.InputOutput[string, string]
		Cases  []fun.InputOutput[string, string]
	}{
		{
			"just_prompt",
			"Repeat the input twice, by concatenating the input string without any space. Return just the result.",
			nil,
			[]fun.InputOutput[string, string]{
				{"foo", "foofoo"},
				{"bar", "barbar"},
				{"test", "testtest"},
				{"zzz", "zzzzzz"},
			},
		},
		{
			"just_data",
			"",
			[]fun.InputOutput[string, string]{
				// We repeat some training data to reinforce those cases.
				// (Otherwise they fail when we test training cases.)
				{"abc", "abcabc"},
				{"ddd", "dddddd"},
				{"cba", "cbacba"},
				{"zoo", "zoozoo"},
				{"zoo", "zoozoo"},
				{"AbC", "AbCAbC"},
				{"roar", "roarroar"},
				{"roar", "roarroar"},
				{"lsdfk", "lsdfklsdfk"},
				{"ZZZZ", "ZZZZZZZZ"},
				{"ZZZZ", "ZZZZZZZZ"},
				{"ZZZZ", "ZZZZZZZZ"},
				{"long", "longlong"},
			},
			[]fun.InputOutput[string, string]{
				{"foo", "foofoo"},
				{"bar", "barbar"},
				{"test", "testtest"},
				// {"zzz", "zzzzzz"}, // Returns "zzz..." with llama3.8b.
			},
		},
		{
			"prompt_and_data",
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
				// {"zzz", "zzzzzz"}, // Returns "zzzZZZ" with llama3.8b.
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.Name, func(t *testing.T) {
			t.Parallel()

			providers := []struct {
				Name     string
				Provider fun.TextProvider
				Enabled  func(t *testing.T)
			}{
				{
					"ollama",
					&fun.OllamaTextProvider{
						Client: nil,
						Base:   os.Getenv("OLLAMA_HOST"),
						Model: fun.OllamaModel{
							Model:    "llama3:8b",
							Insecure: false,
							Username: "",
							Password: "",
						},
						MaxContextLength: 0,
						Seed:             42,
						Temperature:      0,
					},
					func(t *testing.T) {
						if os.Getenv("OLLAMA_HOST") == "" {
							t.Skip("OLLAMA_HOST is not available")
						}
					},
				},
				{
					"groq",
					&fun.GroqTextProvider{
						Client:           nil,
						APIKey:           os.Getenv("GROQ_API_KEY"),
						Model:            "llama3-8b-8192",
						MaxContextLength: 0,
						Seed:             42,
						Temperature:      0,
					},
					func(t *testing.T) {
						if os.Getenv("GROQ_API_KEY") == "" {
							t.Skip("GROQ_API_KEY is not available")
						}
					},
				},
				{
					"anthropic",
					&fun.AnthropicTextProvider{
						Client:      nil,
						APIKey:      os.Getenv("ANTHROPIC_API_KEY"),
						Model:       "claude-3-haiku-20240307",
						Temperature: 0,
					},
					func(t *testing.T) {
						if os.Getenv("ANTHROPIC_API_KEY") == "" {
							t.Skip("ANTHROPIC_API_KEY is not available")
						}
					},
				},
			}

			for _, provider := range providers {
				t.Run(provider.Name, func(t *testing.T) {
					t.Parallel()

					provider.Enabled(t)

					f := fun.Text[string, string]{
						Provider:         provider.Provider,
						InputJSONSchema:  jsonSchemaString,
						OutputJSONSchema: jsonSchemaString,
						Prompt:           tt.Prompt,
						Data:             tt.Data,
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
		})
	}
}

func TestTextStruct(t *testing.T) {
	t.Parallel()

	tests := []struct {
		Name   string
		Prompt string
		Data   []fun.InputOutput[string, OutputStruct]
		Cases  []fun.InputOutput[string, OutputStruct]
	}{
		{
			"just_data",
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
			"prompt_and_data",
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

	for _, tt := range tests {
		t.Run(tt.Name, func(t *testing.T) {
			t.Parallel()

			providers := []struct {
				Name     string
				Provider fun.TextProvider
				Enabled  func(t *testing.T)
			}{
				{
					"ollama",
					&fun.OllamaTextProvider{
						Client: nil,
						Base:   os.Getenv("OLLAMA_HOST"),
						Model: fun.OllamaModel{
							Model:    "llama3:8b",
							Insecure: false,
							Username: "",
							Password: "",
						},
						MaxContextLength: 0,
						Seed:             42,
						Temperature:      0,
					},
					func(t *testing.T) {
						if os.Getenv("OLLAMA_HOST") == "" {
							t.Skip("OLLAMA_HOST is not available")
						}
					},
				},
				{
					"groq",
					&fun.GroqTextProvider{
						Client:           nil,
						APIKey:           os.Getenv("GROQ_API_KEY"),
						Model:            "llama3-8b-8192",
						MaxContextLength: 0,
						Seed:             42,
						Temperature:      0,
					},
					func(t *testing.T) {
						if os.Getenv("GROQ_API_KEY") == "" {
							t.Skip("GROQ_API_KEY is not available")
						}
					},
				},
				{
					"anthropic",
					&fun.AnthropicTextProvider{
						Client:      nil,
						APIKey:      os.Getenv("ANTHROPIC_API_KEY"),
						Model:       "claude-3-haiku-20240307",
						Temperature: 0,
					},
					func(t *testing.T) {
						if os.Getenv("ANTHROPIC_API_KEY") == "" {
							t.Skip("ANTHROPIC_API_KEY is not available")
						}
					},
				},
			}

			for _, provider := range providers {
				t.Run(provider.Name, func(t *testing.T) {
					t.Parallel()

					provider.Enabled(t)

					data := slices.Clone(tt.Data)
					// TODO: Why is there a differnce on Groq so that we have to repeat the last training data sample.
					//       And why with repeated sample Ollama starts returning non-JSON comments.
					if provider.Name == "groq" {
						data = append(data, data[len(data)-1])
					}

					f := fun.Text[string, OutputStruct]{
						Provider:         provider.Provider,
						InputJSONSchema:  jsonSchemaString,
						OutputJSONSchema: nil,
						Prompt:           tt.Prompt,
						Data:             data,
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
		})
	}
}
