package fun_test

import (
	"context"
	"fmt"
	"os"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/rs/zerolog"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"gitlab.com/tozd/go/errors"

	"gitlab.com/tozd/go/fun"
)

var jsonSchemaString = []byte(`{"type": "string"}`)

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
				Client:            nil,
				Base:              os.Getenv("OLLAMA_HOST"),
				Model:             "llama3:8b",
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

type toolStringInput struct {
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

func tools() map[string]fun.TextTooler {
	return map[string]fun.TextTooler{
		"repeat_string": &fun.TextTool[toolStringInput, string]{
			Description:      "Repeats the input twice, by concatenating the input string without any space.",
			InputJSONSchema:  toolInputJSONSchema,
			OutputJSONSchema: jsonSchemaString,
			Fun: func(_ context.Context, input toolStringInput) (string, errors.E) {
				return input.String + input.String, nil
			},
		},
	}
}

var providersForTools = []testProvider{
	{
		"ollama",
		func(t *testing.T) fun.TextProvider {
			t.Helper()

			if os.Getenv("OLLAMA_HOST") == "" {
				t.Skip("OLLAMA_HOST is not available")
			}
			return &fun.OllamaTextProvider{
				Client:            nil,
				Base:              os.Getenv("OLLAMA_HOST"),
				Model:             "llama3-groq-tool-use:70b",
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
				Model:             "llama3-groq-70b-8192-tool-use-preview",
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
				Model:       "claude-3-5-sonnet-20240620",
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

var tests = []struct {
	Name          string
	Prompt        string
	Data          []fun.InputOutput[string, OutputStruct]
	Cases         []fun.InputOutput[string, OutputStruct]
	CheckRecorder func(t *testing.T, recorder *fun.TextRecorder, providerName string)
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
		func(t *testing.T, recorder *fun.TextRecorder, _ string) {
			t.Helper()

			if assert.Len(t, recorder.Calls(), 1) {
				assert.Len(t, recorder.Calls()[0].Messages, 10)
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
		func(t *testing.T, recorder *fun.TextRecorder, _ string) {
			t.Helper()

			if assert.Len(t, recorder.Calls(), 1) {
				assert.Len(t, recorder.Calls()[0].Messages, 11)
			}
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
		func(t *testing.T, recorder *fun.TextRecorder, providerName string) {
			t.Helper()

			if providerName == "groq" || providerName == "ollama" {
				if assert.Len(t, recorder.Calls(), 1) {
					assert.Len(t, recorder.Calls()[0].Messages, 15)
				}
			} else {
				if assert.Len(t, recorder.Calls(), 1) {
					assert.Len(t, recorder.Calls()[0].Messages, 11)
				}
			}
		},
	},
}

func runTextTests(
	t *testing.T, providers []testProvider, tests []textTestCase,
	tools func() map[string]fun.TextTooler,
	checkOutput func(t *testing.T, providerName string, tt fun.InputOutput[string, string], output string),
) {
	t.Helper()

	for _, provider := range providers {
		t.Run(provider.Name, func(t *testing.T) {
			t.Parallel()

			for _, tt := range tests {
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
						Tools:            tools(),
					}

					ctx := zerolog.New(zerolog.NewTestWriter(t)).WithContext(context.Background())

					errE := f.Init(ctx)
					require.NoError(t, errE, "% -+#.1v", errE)

					for _, d := range tt.Data {
						t.Run(fmt.Sprintf("input=%s", d.Input), func(t *testing.T) {
							if provider.Name != "ollama" {
								t.Parallel()
							}

							ct := fun.WithTextRecorder(ctx)
							output, errE := f.Call(ct, d.Input...)
							require.NoError(t, errE, "% -+#.1v", errE)
							checkOutput(t, provider.Name, d, output)
							tt.CheckRecorder(t, fun.GetTextRecorder(ct), provider.Name)
						})
					}

					for _, c := range tt.Cases {
						t.Run(fmt.Sprintf("input=%s", c.Input), func(t *testing.T) {
							if provider.Name != "ollama" {
								t.Parallel()
							}

							ct := fun.WithTextRecorder(ctx)
							output, errE := f.Call(ct, c.Input...)
							require.NoError(t, errE, "% -+#.1v", errE)
							checkOutput(t, provider.Name, c, output)
							tt.CheckRecorder(t, fun.GetTextRecorder(ct), provider.Name)
						})
					}
				})
			}
		})
	}
}

func cleanCall(call *fun.TextRecorderCall, d *int64) {
	*d++
	callID := *d

	toolUses := map[string]string{}
	for i := range call.Messages {
		if call.Messages[i].ToolUseID != "" {
			if _, ok := toolUses[call.Messages[i].ToolUseID]; !ok {
				toolUses[call.Messages[i].ToolUseID] = fmt.Sprintf("call_%d_%d", callID, i)
			}
			call.Messages[i].ToolUseID = toolUses[call.Messages[i].ToolUseID]
		}
		for j := range call.Messages[i].ToolCalls {
			cleanCall(&call.Messages[i].ToolCalls[j], d)
		}
		if call.Messages[i].ToolDuration != 0 {
			call.Messages[i].ToolDuration = fun.Duration((callID*100000 + int64(i) + 1) * int64(time.Second))
		}
	}

	call.ID = fmt.Sprintf("id_%d", callID)

	usedTokensSlice := []fun.TextRecorderUsedTokens{}
	for _, tokens := range call.UsedTokens {
		usedTokensSlice = append(usedTokensSlice, tokens)
	}
	slices.SortStableFunc(usedTokensSlice, func(a, b fun.TextRecorderUsedTokens) int {
		return a.Total - b.Total
	})
	usedTokens := map[string]fun.TextRecorderUsedTokens{}
	for i, tokens := range usedTokensSlice {
		usedTokens[fmt.Sprintf("req_%d_%d", callID, i)] = tokens
	}
	call.UsedTokens = usedTokens

	usedTime := map[string]fun.TextRecorderUsedTime{}
	i := 0
	for _, t := range call.UsedTime {
		t.APICall = fun.Duration((1 + int64(i)) * int64(time.Second))
		usedTime[fmt.Sprintf("req_%d_%d", callID, i)] = t
		i++
	}
	call.UsedTime = usedTime

	call.Duration = fun.Duration(callID * int64(time.Second))
}

func cleanCalls(calls []fun.TextRecorderCall) {
	var d int64
	for i := range calls {
		cleanCall(&calls[i], &d)
	}
}

type textTestCase struct {
	Name          string
	Prompt        string
	Data          []fun.InputOutput[string, string]
	Cases         []fun.InputOutput[string, string]
	CheckRecorder func(t *testing.T, recorder *fun.TextRecorder, providerName string)
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
			func(t *testing.T, recorder *fun.TextRecorder, _ string) {
				t.Helper()

				if assert.Len(t, recorder.Calls(), 1) {
					assert.Len(t, recorder.Calls()[0].Messages, 3)
				}
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
				// {[]string{"zoo"}, "zoozoo"}, // Does not work with groq.
				{[]string{"AbC"}, "AbCAbC"},
				{[]string{"roar"}, "roarroar"},
				{[]string{"roar"}, "roarroar"},
				{[]string{"lsdfk"}, "lsdfklsdfk"},
				// {[]string{"ZZZZ"}, "ZZZZZZZZ"}, // Does not work with groq.
				{[]string{"long"}, "longlong"},
			},
			[]fun.InputOutput[string, string]{
				{[]string{"foo"}, "foofoo"},
				{[]string{"bar"}, "barbar"},
				{[]string{"test"}, "testtest"},
				// {[]string{"zzz"}, "zzzzzz"}, // Returns "zzz..." with llama3:8b.
			},
			func(t *testing.T, recorder *fun.TextRecorder, _ string) {
				t.Helper()

				if assert.Len(t, recorder.Calls(), 1) {
					assert.Len(t, recorder.Calls()[0].Messages, 18)
				}
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
				// {[]string{"zzz"}, "zzzzzz"}, // Returns "zzzZZZ" with llama3:8b.
			},
			func(t *testing.T, recorder *fun.TextRecorder, _ string) {
				t.Helper()

				if assert.Len(t, recorder.Calls(), 1) {
					assert.Len(t, recorder.Calls()[0].Messages, 35)
				}
			},
		},
	}

	runTextTests(
		t, providers, tests,
		func() map[string]fun.TextTooler { return nil },
		func(t *testing.T, _ string, tt fun.InputOutput[string, string], output string) {
			t.Helper()

			assert.Equal(t, tt.Output, output)
		},
	)
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
			func(t *testing.T, recorder *fun.TextRecorder, providerName string) {
				t.Helper()

				calls := recorder.Calls()
				if assert.Len(t, calls, 1) {
					usedTool := 0
					messages := calls[0].Messages
					for i := range messages {
						if messages[i].Role == "tool_use" || messages[i].Role == "tool_result" {
							usedTool++
						}
					}
					if providerName == "groq" {
						// For some reason groq calls the tool twice.
						assert.Equal(t, 4, usedTool, messages)
					} else {
						assert.Equal(t, 2, usedTool, messages)
					}

					if providerName == "anthropic" {
						assert.Len(t, messages, 4+usedTool)
					} else {
						assert.Len(t, messages, 3+usedTool)
					}
				}
			},
		},
	}

	runTextTests(t, providersForTools, tests, tools, func(t *testing.T, providerName string, tt fun.InputOutput[string, string], output string) {
		t.Helper()

		if providerName == "ollama" {
			// TODO: Remove this special case.
			// Ollama adds this prefix to the output and no prompt manipulation could remove it.
			output = strings.TrimPrefix(output, "The repeated string is: ")
		}

		assert.Equal(t, tt.Output, output)
	})
}

func TestTextStruct(t *testing.T) { //nolint:paralleltest,tparallel
	// We do not run test cases in parallel, so that we can run Ollama tests in sequence.

	for _, provider := range providers {
		t.Run(provider.Name, func(t *testing.T) {
			t.Parallel()

			for _, tt := range tests {
				t.Run(tt.Name, func(t *testing.T) {
					if provider.Name != "ollama" {
						t.Parallel()
					}

					data := slices.Clone(tt.Data)
					// TODO: See if there is a way to not have to repeat samples.
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
						t.Run(fmt.Sprintf("input=%s", d.Input), func(t *testing.T) {
							if provider.Name != "ollama" {
								t.Parallel()
							}

							ct := fun.WithTextRecorder(ctx)
							output, errE := f.Call(ct, d.Input...)
							require.NoError(t, errE, "% -+#.1v", errE)
							assert.Equal(t, d.Output, output)
							tt.CheckRecorder(t, fun.GetTextRecorder(ct), provider.Name)
						})
					}

					for _, c := range tt.Cases {
						t.Run(fmt.Sprintf("input=%s", c.Input), func(t *testing.T) {
							if provider.Name != "ollama" {
								t.Parallel()
							}

							ct := fun.WithTextRecorder(ctx)
							output, errE := f.Call(ct, c.Input...)
							require.NoError(t, errE, "% -+#.1v", errE)
							assert.Equal(t, c.Output, output)
							tt.CheckRecorder(t, fun.GetTextRecorder(ct), provider.Name)
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
				t.Run(fmt.Sprintf("input=%s", d.Input), func(t *testing.T) {
					t.Parallel()

					ct := fun.WithTextRecorder(ctx)
					output, errE := f.Call(ct, d.Input...)
					require.NoError(t, errE, "% -+#.1v", errE)
					assert.Equal(t, d.Output, output)
					tt.CheckRecorder(t, fun.GetTextRecorder(ct), "openai")
				})
			}

			for _, c := range tt.Cases {
				t.Run(fmt.Sprintf("input=%s", c.Input), func(t *testing.T) {
					t.Parallel()

					ct := fun.WithTextRecorder(ctx)
					output, errE := f.Call(ct, c.Input...)
					require.NoError(t, errE, "% -+#.1v", errE)
					assert.Equal(t, toOutputStructWithoutOmitEmpty(c.Output), output)
					tt.CheckRecorder(t, fun.GetTextRecorder(ct), "openai")
				})
			}
		})
	}
}
