package fun_test

import (
	"context"
	"fmt"
	"os"
	"slices"
	"testing"

	"github.com/rs/zerolog"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"gitlab.com/tozd/go/errors"

	"gitlab.com/tozd/go/fun"
)

var jsonSchemaString = []byte(`{"type": "string"}`)

// It looks like they are quite some limitations for OpenAI JSON Schema.
// OpenAI currently supports only top-level object type and it does not support
// top-level $ref in the schema. Also it requires that JSON Schema has all properties
// required. A lot of restrictions which probably means we have to provide manual
// JSON Schema and it cannot be self-generated.
// See: https://github.com/invopop/jsonschema/issues/148
var outputStructJSONSchema = []byte(`
{
  "$schema": "https://json-schema.org/draft/2020-12/schema",
	"$defs": {
		"OutputStruct": {
			"properties": {
				"key": {
					"type": "string"
				},
				"value": {
					"type": "integer"
				},
				"children": {
					"items": {
						"$ref": "#/$defs/OutputStruct"
					},
					"type": "array"
				}
			},
			"additionalProperties": false,
			"type": "object",
			"required": [
				"key",
				"value",
				"children"
			]
		}
	},
  "properties": {
    "key": {
      "type": "string"
    },
    "value": {
      "type": "integer"
    },
    "children": {
      "items": {
        "$ref": "#/$defs/OutputStruct"
      },
      "type": "array"
    }
  },
  "additionalProperties": false,
  "type": "object",
  "required": [
    "key",
    "value",
		"children"
  ],
  "title": "OutputStruct"
}
`)

type OutputStructWithoutOmitEmpty struct {
	Key      string                         `json:"key"`
	Value    int                            `json:"value"`
	Children []OutputStructWithoutOmitEmpty `json:"children"`
}

func toOutputStructWithoutOmitEmpty(d OutputStruct) OutputStructWithoutOmitEmpty {
	children := []OutputStructWithoutOmitEmpty{}
	for _, c := range d.Children {
		children = append(children, toOutputStructWithoutOmitEmpty(c))
	}

	return OutputStructWithoutOmitEmpty{
		Key:      d.Key,
		Value:    d.Value,
		Children: children,
	}
}

type OutputStruct struct {
	Key      string         `json:"key"`
	Value    int            `json:"value"`
	Children []OutputStruct `json:"children,omitempty"`
}

type testProvider struct {
	Name     string
	Provider func(t *testing.T) fun.TextProvider
}

var providers = []testProvider{
	{
		"ollama",
		func(t *testing.T) fun.TextProvider {
			t.Helper()

			if os.Getenv("OLLAMA_HOST") == "" {
				t.Skip("OLLAMA_HOST is not available")
			}
			return &fun.OllamaTextProvider{
				Client: nil,
				Base:   os.Getenv("OLLAMA_HOST"),
				Model: fun.OllamaModel{
					Model:    "llama3:8b",
					Insecure: false,
					Username: "",
					Password: "",
				},
				MaxContextLength:  0,
				MaxResponseLength: 0,
				Seed:              42,
				Temperature:       0,
			}
		},
	},
	{
		"groq",
		func(t *testing.T) fun.TextProvider {
			t.Helper()

			if os.Getenv("GROQ_API_KEY") == "" {
				t.Skip("GROQ_API_KEY is not available")
			}
			return &fun.GroqTextProvider{
				Client:            nil,
				APIKey:            os.Getenv("GROQ_API_KEY"),
				Model:             "llama3-8b-8192",
				MaxContextLength:  0,
				MaxResponseLength: 0,
				Seed:              42,
				Temperature:       0,
			}
		},
	},
	{
		"anthropic",
		func(t *testing.T) fun.TextProvider {
			t.Helper()

			if os.Getenv("ANTHROPIC_API_KEY") == "" {
				t.Skip("ANTHROPIC_API_KEY is not available")
			}
			return &fun.AnthropicTextProvider{
				Client:      nil,
				APIKey:      os.Getenv("ANTHROPIC_API_KEY"),
				Model:       "claude-3-haiku-20240307",
				Temperature: 0,
			}
		},
	},
	{
		"openai",
		func(t *testing.T) fun.TextProvider {
			t.Helper()

			if os.Getenv("OPENAI_API_KEY") == "" {
				t.Skip("OPENAI_API_KEY is not available")
			}
			return &fun.OpenAITextProvider{
				Client:                nil,
				APIKey:                os.Getenv("OPENAI_API_KEY"),
				Model:                 "gpt-4o-mini-2024-07-18",
				MaxContextLength:      128_000,
				MaxResponseLength:     16_384,
				ForceOutputJSONSchema: false,
				Seed:                  42,
				Temperature:           0,
			}
		},
	},
}

type toolInput struct {
	String string `json:"string"`
}

var toolInputJSONSchema = []byte(`
{
  "properties": {
    "string": {
      "type": "string"
    }
  },
  "additionalProperties": false,
  "type": "object",
  "required": [
    "string"
  ]
}
`)

func tools() map[string]fun.Tooler {
	return map[string]fun.Tooler{
		"repeat_string": &fun.Tool[toolInput, string]{
			Description:      "Repeats the input twice, by concatenating the input string without any space.",
			InputJSONSchema:  toolInputJSONSchema,
			OutputJSONSchema: jsonSchemaString,
			Fun: func(_ context.Context, input toolInput) (string, errors.E) {
				return input.String + input.String, nil
			},
		},
	}
}

var providersWithTools = []testProvider{
	{
		"ollama",
		func(t *testing.T) fun.TextProvider {
			t.Helper()

			if os.Getenv("OLLAMA_HOST") == "" {
				t.Skip("OLLAMA_HOST is not available")
			}
			return &fun.OllamaTextProvider{
				Client: nil,
				Base:   os.Getenv("OLLAMA_HOST"),
				Model: fun.OllamaModel{
					Model:    "llama3-groq-tool-use:70b",
					Insecure: false,
					Username: "",
					Password: "",
				},
				MaxContextLength:  0,
				MaxResponseLength: 0,
				Tools:             tools(),
				Seed:              42,
				Temperature:       0,
			}
		},
	},
	{
		"groq",
		func(t *testing.T) fun.TextProvider {
			t.Helper()

			if os.Getenv("GROQ_API_KEY") == "" {
				t.Skip("GROQ_API_KEY is not available")
			}
			return &fun.GroqTextProvider{
				Client:            nil,
				APIKey:            os.Getenv("GROQ_API_KEY"),
				Model:             "llama3-groq-70b-8192-tool-use-preview",
				MaxContextLength:  0,
				MaxResponseLength: 0,
				Tools:             tools(),
				Seed:              42,
				Temperature:       0,
			}
		},
	},
	{
		"anthropic",
		func(t *testing.T) fun.TextProvider {
			t.Helper()

			if os.Getenv("ANTHROPIC_API_KEY") == "" {
				t.Skip("ANTHROPIC_API_KEY is not available")
			}
			return &fun.AnthropicTextProvider{
				Client:      nil,
				APIKey:      os.Getenv("ANTHROPIC_API_KEY"),
				Model:       "claude-3-5-sonnet-20240620",
				Tools:       tools(),
				Temperature: 0,
			}
		},
	},
	{
		"openai",
		func(t *testing.T) fun.TextProvider {
			t.Helper()

			if os.Getenv("OPENAI_API_KEY") == "" {
				t.Skip("OPENAI_API_KEY is not available")
			}
			return &fun.OpenAITextProvider{
				Client:                nil,
				APIKey:                os.Getenv("OPENAI_API_KEY"),
				Model:                 "gpt-4o-mini-2024-07-18",
				MaxContextLength:      128_000,
				MaxResponseLength:     16_384,
				Tools:                 tools(),
				ForceOutputJSONSchema: false,
				Seed:                  42,
				Temperature:           0,
			}
		},
	},
}

var tests = []struct {
	Name          string
	Prompt        string
	Data          []fun.InputOutput[string, OutputStruct]
	Cases         []fun.InputOutput[string, OutputStruct]
	CheckRecorder func(t *testing.T, recorder *fun.TextProviderRecorder, providerName string)
}{
	{
		"just_data",
		"",
		[]fun.InputOutput[string, OutputStruct]{
			{[]string{"foo=1"}, OutputStruct{Key: "foo", Value: 1}},
			{[]string{"bar=3"}, OutputStruct{Key: "bar", Value: 3}},
			{[]string{"foo=1 [bar=3]"}, OutputStruct{Key: "foo", Value: 1, Children: []OutputStruct{{Key: "bar", Value: 3}}}},
			{[]string{"foo=1 [bar=3 zoo=2]"}, OutputStruct{Key: "foo", Value: 1, Children: []OutputStruct{{Key: "bar", Value: 3}, {Key: "zoo", Value: 2}}}},
		},
		[]fun.InputOutput[string, OutputStruct]{
			{[]string{"name=42 [first=2 second=1]"}, OutputStruct{Key: "name", Value: 42, Children: []OutputStruct{{Key: "first", Value: 2}, {Key: "second", Value: 1}}}},
		},
		func(t *testing.T, recorder *fun.TextProviderRecorder, providerName string) {
			t.Helper()

			if providerName == "groq" {
				assert.Len(t, recorder.Messages(), 12)
			} else {
				assert.Len(t, recorder.Messages(), 10)
			}
		},
	},
	{
		"prompt_and_data",
		fun.TextParserToJSONPrompt,
		[]fun.InputOutput[string, OutputStruct]{
			{[]string{"foo=1"}, OutputStruct{Key: "foo", Value: 1}},
			{[]string{"bar=3"}, OutputStruct{Key: "bar", Value: 3}},
			{[]string{"foo=1 [bar=3]"}, OutputStruct{Key: "foo", Value: 1, Children: []OutputStruct{{Key: "bar", Value: 3}}}},
			{[]string{"foo=1 [bar=3 zoo=2]"}, OutputStruct{Key: "foo", Value: 1, Children: []OutputStruct{{Key: "bar", Value: 3}, {Key: "zoo", Value: 2}}}},
		},
		[]fun.InputOutput[string, OutputStruct]{
			{[]string{"name=42 [first=2 second=1]"}, OutputStruct{Key: "name", Value: 42, Children: []OutputStruct{{Key: "first", Value: 2}, {Key: "second", Value: 1}}}},
		},
		func(t *testing.T, recorder *fun.TextProviderRecorder, _ string) {
			t.Helper()

			assert.Len(t, recorder.Messages(), 11)
		},
	},
	{
		"json_only_prompt_and_data",
		fun.TextToJSONPrompt,
		[]fun.InputOutput[string, OutputStruct]{
			{[]string{"foo=1"}, OutputStruct{Key: "foo", Value: 1}},
			{[]string{"bar=3"}, OutputStruct{Key: "bar", Value: 3}},
			{[]string{"foo=1 [bar=3]"}, OutputStruct{Key: "foo", Value: 1, Children: []OutputStruct{{Key: "bar", Value: 3}}}},
			{[]string{"foo=1 [bar=3 zoo=2]"}, OutputStruct{Key: "foo", Value: 1, Children: []OutputStruct{{Key: "bar", Value: 3}, {Key: "zoo", Value: 2}}}},
		},
		[]fun.InputOutput[string, OutputStruct]{
			{[]string{"name=42 [first=2 second=1]"}, OutputStruct{Key: "name", Value: 42, Children: []OutputStruct{{Key: "first", Value: 2}, {Key: "second", Value: 1}}}},
		},
		func(t *testing.T, recorder *fun.TextProviderRecorder, providerName string) {
			t.Helper()

			if providerName == "groq" || providerName == "ollama" {
				assert.Len(t, recorder.Messages(), 15)
			} else {
				assert.Len(t, recorder.Messages(), 11)
			}
		},
	},
}

func runTextTests(t *testing.T, providers []testProvider, tests []textTestCase) {
	t.Helper()

	for _, provider := range providers {
		provider := provider

		t.Run(provider.Name, func(t *testing.T) {
			t.Parallel()

			for _, tt := range tests {
				tt := tt

				t.Run(tt.Name, func(t *testing.T) {
					if provider.Name != "ollama" {
						t.Parallel()
					}

					f := fun.Text[string, string]{
						Provider:         provider.Provider(t),
						InputJSONSchema:  jsonSchemaString,
						OutputJSONSchema: jsonSchemaString,
						Prompt:           tt.Prompt,
						Data:             tt.Data,
					}

					ctx := zerolog.New(zerolog.NewTestWriter(t)).WithContext(context.Background())

					errE := f.Init(ctx)
					require.NoError(t, errE, "% -+#.1v", errE)

					for _, d := range tt.Data {
						d := d

						t.Run(fmt.Sprintf("input=%s", d.Input), func(t *testing.T) {
							if provider.Name != "ollama" {
								t.Parallel()
							}

							ct := fun.WithTextProviderRecorder(ctx)
							output, errE := f.Call(ct, d.Input...)
							assert.NoError(t, errE, "% -+#.1v", errE)
							assert.Equal(t, d.Output, output)
							tt.CheckRecorder(t, fun.GetTextProviderRecorder(ct), provider.Name)
						})
					}

					for _, c := range tt.Cases {
						c := c

						t.Run(fmt.Sprintf("input=%s", c.Input), func(t *testing.T) {
							if provider.Name != "ollama" {
								t.Parallel()
							}

							ct := fun.WithTextProviderRecorder(ctx)
							output, errE := f.Call(ct, c.Input...)
							assert.NoError(t, errE, "% -+#.1v", errE)
							assert.Equal(t, c.Output, output)
							tt.CheckRecorder(t, fun.GetTextProviderRecorder(ct), provider.Name)
						})
					}
				})
			}
		})
	}
}

type textTestCase struct {
	Name          string
	Prompt        string
	Data          []fun.InputOutput[string, string]
	Cases         []fun.InputOutput[string, string]
	CheckRecorder func(t *testing.T, recorder *fun.TextProviderRecorder, providerName string)
}

func TestText(t *testing.T) { //nolint:paralleltest,tparallel
	// We do not run test cases in parallel, so that we can run Ollama tests in sequence.

	tests := []textTestCase{
		{
			"just_prompt",
			"Repeat the input twice, by concatenating the input string without any space. Return just the result.",
			nil,
			[]fun.InputOutput[string, string]{
				{[]string{"foo"}, "foofoo"},
				{[]string{"bar"}, "barbar"},
				{[]string{"test"}, "testtest"},
				{[]string{"zzz"}, "zzzzzz"},
			},
			func(t *testing.T, recorder *fun.TextProviderRecorder, _ string) {
				t.Helper()

				assert.Len(t, recorder.Messages(), 3)
			},
		},
		{
			"just_data",
			"",
			[]fun.InputOutput[string, string]{
				// We repeat some training data to reinforce those cases.
				// (Otherwise they fail when we test training cases.)
				{[]string{"abc"}, "abcabc"},
				{[]string{"ddd"}, "dddddd"},
				{[]string{"cba"}, "cbacba"},
				{[]string{"zoo"}, "zoozoo"},
				{[]string{"zoo"}, "zoozoo"},
				{[]string{"AbC"}, "AbCAbC"},
				{[]string{"roar"}, "roarroar"},
				{[]string{"roar"}, "roarroar"},
				{[]string{"lsdfk"}, "lsdfklsdfk"},
				{[]string{"ZZZZ"}, "ZZZZZZZZ"},
				{[]string{"ZZZZ"}, "ZZZZZZZZ"},
				{[]string{"ZZZZ"}, "ZZZZZZZZ"},
				{[]string{"long"}, "longlong"},
			},
			[]fun.InputOutput[string, string]{
				{[]string{"foo"}, "foofoo"},
				{[]string{"bar"}, "barbar"},
				{[]string{"test"}, "testtest"},
				// {[]string{"zzz"}, "zzzzzz"}, // Returns "zzz..." with llama3.8b.
			},
			func(t *testing.T, recorder *fun.TextProviderRecorder, _ string) {
				t.Helper()

				assert.Len(t, recorder.Messages(), 28)
			},
		},
		{
			"prompt_and_data",
			"Repeat the input twice, by concatenating the input string without any space. Return just the result.",
			[]fun.InputOutput[string, string]{
				// We repeat some training data to reinforce those cases.
				// (Otherwise they fail when we test training cases.)
				{[]string{"abc"}, "abcabc"},
				{[]string{"ddd"}, "dddddd"},
				{[]string{"cba"}, "cbacba"},
				{[]string{"zoo"}, "zoozoo"},
				{[]string{"zoo"}, "zoozoo"},
				{[]string{"zoo"}, "zoozoo"},
				{[]string{"zoo"}, "zoozoo"},
				{[]string{"zoo"}, "zoozoo"},
				{[]string{"zoo"}, "zoozoo"},
				{[]string{"AbC"}, "AbCAbC"},
				{[]string{"roar"}, "roarroar"},
				{[]string{"roar"}, "roarroar"},
				{[]string{"lsdfk"}, "lsdfklsdfk"},
				{[]string{"ZZZZ"}, "ZZZZZZZZ"},
				{[]string{"ZZZZ"}, "ZZZZZZZZ"},
				{[]string{"long"}, "longlong"},
			},
			[]fun.InputOutput[string, string]{
				{[]string{"foo"}, "foofoo"},
				{[]string{"bar"}, "barbar"},
				{[]string{"test"}, "testtest"},
				// {[]string{"zzz"}, "zzzzzz"}, // Returns "zzzZZZ" with llama3.8b.
			},
			func(t *testing.T, recorder *fun.TextProviderRecorder, _ string) {
				t.Helper()

				assert.Len(t, recorder.Messages(), 35)
			},
		},
	}

	runTextTests(t, providers, tests)
}

func TestTextTools(t *testing.T) { //nolint:paralleltest,tparallel
	// We do not run test cases in parallel, so that we can run Ollama tests in sequence.

	tests := []textTestCase{
		{
			"just_prompt",
			"Repeat the input twice, by concatenating the input string without any space. Return only the resulting string. Do not explain anything.",
			nil,
			[]fun.InputOutput[string, string]{
				// We cannot use "foo" here because groq makes trash output.
				{[]string{"bla"}, "blabla"},
				{[]string{"bar"}, "barbar"},
				{[]string{"test"}, "testtest"},
				{[]string{"zzz"}, "zzzzzz"},
			},
			func(t *testing.T, recorder *fun.TextProviderRecorder, providerName string) {
				t.Helper()

				usedTool := 0
				for _, message := range recorder.Messages() {
					if message["role"] == "tool_use" || message["role"] == "tool_result" {
						usedTool++
					}
				}
				if providerName == "groq" {
					// For some reason groq calls the tool twice.
					assert.Equal(t, 4, usedTool, recorder.Messages())
				} else {
					assert.Equal(t, 2, usedTool, recorder.Messages())
				}

				if providerName == "anthropic" {
					assert.Len(t, recorder.Messages(), 4+usedTool)
				} else {
					assert.Len(t, recorder.Messages(), 3+usedTool)
				}
			},
		},
	}

	runTextTests(t, providersWithTools, tests)
}

func TestTextStruct(t *testing.T) { //nolint:paralleltest,tparallel
	// We do not run test cases in parallel, so that we can run Ollama tests in sequence.

	for _, provider := range providers {
		provider := provider

		t.Run(provider.Name, func(t *testing.T) {
			t.Parallel()

			for _, tt := range tests {
				tt := tt

				t.Run(tt.Name, func(t *testing.T) {
					if provider.Name != "ollama" {
						t.Parallel()
					}

					data := slices.Clone(tt.Data)
					// TODO: Why is there a difference between providers so that we have to repeat the last training data sample.
					//       And why with repeated sample Ollama starts returning non-JSON comments for "just_data".
					if tt.Name == "just_data" && provider.Name == "groq" {
						data = append(data, data[len(data)-1])
					}
					if tt.Name == "json_only_prompt_and_data" && provider.Name == "groq" {
						data = append(data, data[len(data)-1])
						data = append(data, data[len(data)-1])
					}
					if tt.Name == "json_only_prompt_and_data" && provider.Name == "ollama" {
						data = append(data, data[len(data)-1])
						data = append(data, data[len(data)-1])
					}

					f := fun.Text[string, OutputStruct]{
						Provider:         provider.Provider(t),
						InputJSONSchema:  jsonSchemaString,
						OutputJSONSchema: nil,
						Prompt:           tt.Prompt,
						Data:             data,
					}

					ctx := zerolog.New(zerolog.NewTestWriter(t)).WithContext(context.Background())

					errE := f.Init(ctx)
					require.NoError(t, errE, "% -+#.1v", errE)

					for _, d := range tt.Data {
						d := d

						t.Run(fmt.Sprintf("input=%s", d.Input), func(t *testing.T) {
							if provider.Name != "ollama" {
								t.Parallel()
							}

							ct := fun.WithTextProviderRecorder(ctx)
							output, errE := f.Call(ct, d.Input...)
							assert.NoError(t, errE, "% -+#.1v", errE)
							assert.Equal(t, d.Output, output)
							tt.CheckRecorder(t, fun.GetTextProviderRecorder(ct), provider.Name)
						})
					}

					for _, c := range tt.Cases {
						c := c

						t.Run(fmt.Sprintf("input=%s", c.Input), func(t *testing.T) {
							if provider.Name != "ollama" {
								t.Parallel()
							}

							ct := fun.WithTextProviderRecorder(ctx)
							output, errE := f.Call(ct, c.Input...)
							assert.NoError(t, errE, "% -+#.1v", errE)
							assert.Equal(t, c.Output, output)
							tt.CheckRecorder(t, fun.GetTextProviderRecorder(ct), provider.Name)
						})
					}
				})
			}
		})
	}
}

func TestOpenAIJSONSchema(t *testing.T) {
	t.Parallel()

	if os.Getenv("OPENAI_API_KEY") == "" {
		t.Skip("OPENAI_API_KEY is not available")
	}

	for _, tt := range tests {
		tt := tt

		t.Run(tt.Name, func(t *testing.T) {
			t.Parallel()

			data := []fun.InputOutput[string, OutputStructWithoutOmitEmpty]{}
			for _, d := range tt.Data {
				data = append(data, fun.InputOutput[string, OutputStructWithoutOmitEmpty]{
					Input:  d.Input,
					Output: toOutputStructWithoutOmitEmpty(d.Output),
				})
			}

			f := fun.Text[string, OutputStructWithoutOmitEmpty]{
				Provider: &fun.OpenAITextProvider{
					Client:                nil,
					APIKey:                os.Getenv("OPENAI_API_KEY"),
					Model:                 "gpt-4o-mini-2024-07-18",
					MaxContextLength:      128_000,
					MaxResponseLength:     16_384,
					ForceOutputJSONSchema: true,
					Seed:                  42,
					Temperature:           0,
				},
				InputJSONSchema:  jsonSchemaString,
				OutputJSONSchema: outputStructJSONSchema,
				Prompt:           tt.Prompt,
				Data:             data,
			}

			ctx := zerolog.New(zerolog.NewTestWriter(t)).WithContext(context.Background())

			errE := f.Init(ctx)
			require.NoError(t, errE, "% -+#.1v", errE)

			for _, d := range data {
				d := d

				t.Run(fmt.Sprintf("input=%s", d.Input), func(t *testing.T) {
					t.Parallel()

					ct := fun.WithTextProviderRecorder(ctx)
					output, errE := f.Call(ct, d.Input...)
					assert.NoError(t, errE, "% -+#.1v", errE)
					assert.Equal(t, d.Output, output)
					tt.CheckRecorder(t, fun.GetTextProviderRecorder(ct), "openai")
				})
			}

			for _, c := range tt.Cases {
				c := c

				t.Run(fmt.Sprintf("input=%s", c.Input), func(t *testing.T) {
					t.Parallel()

					ct := fun.WithTextProviderRecorder(ctx)
					output, errE := f.Call(ct, c.Input...)
					assert.NoError(t, errE, "% -+#.1v", errE)
					assert.Equal(t, toOutputStructWithoutOmitEmpty(c.Output), output)
					tt.CheckRecorder(t, fun.GetTextProviderRecorder(ct), "openai")
				})
			}
		})
	}
}
