package fun_test

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"os"

	"gitlab.com/tozd/go/errors"

	"gitlab.com/tozd/go/fun"
)

var (
	// It has to be an object and not just an array of numbers.
	// This is current limitation of AI providers.
	jsonSchemaNumbers = []byte(`
		{
			"type": "object",
			"properties": {
				"numbers": {"type": "array", "items": {"type": "number"}}
			},
			"additionalProperties": false,
			"required": [
				"numbers"
			]
		}
	`)
	jsonSchemaNumber = []byte(`{"type": "integer"}`)
)

type toolInput struct {
	Numbers []float64 `json:"numbers"`
}

func ExampleTextTool() {
	if os.Getenv("OPENAI_API_KEY") == "" {
		fmt.Println("skipped")
		return
	}

	ctx := context.Background()

	f := fun.Text[int, int]{
		Provider: &fun.OpenAITextProvider{
			APIKey:            os.Getenv("OPENAI_API_KEY"),
			Model:             "gpt-4o-mini-2024-07-18",
			MaxContextLength:  128_000,
			MaxResponseLength: 16_384,
			Seed:              42,
		},
		Prompt: `Sum numbers together. Output only the number.`,
		Tools: map[string]fun.TextTooler{
			"sum_numbers": &fun.TextTool[toolInput, float64]{
				Description:      "Sums numbers together.",
				InputJSONSchema:  jsonSchemaNumbers,
				OutputJSONSchema: jsonSchemaNumber,
				Fun: func(_ context.Context, input toolInput) (float64, errors.E) {
					res := 0.0
					for _, n := range input.Numbers {
						res += n
					}
					return res, nil
				},
			},
		},
	}
	errE := f.Init(ctx)
	if errE != nil {
		log.Fatalf("% -+#.1v\n", errE)
	}

	// We use the recorder to make sure the tool has really been called.
	ctx = fun.WithTextRecorder(ctx)

	output, errE := f.Call(ctx, 38, 4)
	if errE != nil {
		log.Fatalf("% -+#.1v\n", errE)
	}
	fmt.Println(output)

	calls := fun.GetTextRecorder(ctx).Calls()
	// We change calls a bit for the example to be deterministic.
	cleanCalls(calls)

	messages, err := json.MarshalIndent(calls[0].Messages, "", "  ")
	if err != nil {
		log.Fatalf("%v\n", err)
	}
	fmt.Println(string(messages))

	// Output:
	// 42
	// [
	//   {
	//     "role": "system",
	//     "content": "Sum numbers together. Output only the number."
	//   },
	//   {
	//     "role": "user",
	//     "content": "[38,4]"
	//   },
	//   {
	//     "role": "tool_use",
	//     "content": "{\"numbers\":[38,4]}",
	//     "toolUseId": "call_1_2",
	//     "toolUseName": "sum_numbers"
	//   },
	//   {
	//     "role": "tool_result",
	//     "content": "42",
	//     "toolUseId": "call_1_2",
	//     "toolDuration": 100004.000
	//   },
	//   {
	//     "role": "assistant",
	//     "content": "42"
	//   }
	// ]
}
