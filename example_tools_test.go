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

func Example_tools() {
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
		Tools: map[string]fun.Tooler{
			"sum_numbers": &fun.Tool[toolInput, float64]{
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
	ctx = fun.WithTextProviderRecorder(ctx)

	output, errE := f.Call(ctx, 38, 4)
	if errE != nil {
		log.Fatalf("% -+#.1v\n", errE)
	}
	fmt.Println(output)

	messages := fun.GetTextProviderRecorder(ctx).Messages()
	// We change call IDs to some deterministic for the example to be deterministic.
	for _, message := range messages {
		if _, ok := message["id"]; ok {
			message["id"] = "call_123"
		}
	}

	messagesJSON, err := json.MarshalIndent(messages, "", "  ")
	if errE != nil {
		log.Fatalf("%v\n", err)
	}
	fmt.Println(string(messagesJSON))

	// Output:
	// 42
	// [
	//   {
	//     "message": "Sum numbers together. Output only the number.",
	//     "role": "system"
	//   },
	//   {
	//     "message": "[38,4]",
	//     "role": "user"
	//   },
	//   {
	//     "id": "call_123",
	//     "message": "{\"numbers\":[38,4]}",
	//     "name": "sum_numbers",
	//     "role": "tool_use"
	//   },
	//   {
	//     "id": "call_123",
	//     "message": "42",
	//     "role": "tool_result"
	//   },
	//   {
	//     "message": "42",
	//     "role": "assistant"
	//   }
	// ]
}
