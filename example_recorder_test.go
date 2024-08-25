package fun_test

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"os"

	"gitlab.com/tozd/go/fun"
)

func ExampleTextRecorder() {
	if os.Getenv("ANTHROPIC_API_KEY") == "" || os.Getenv("OPENAI_API_KEY") == "" {
		fmt.Println("skipped")
		return
	}

	ctx := context.Background()

	// We can define a tool implementation with another model.
	tool := fun.Text[toolInput, float64]{
		Provider: &fun.AnthropicTextProvider{
			APIKey: os.Getenv("ANTHROPIC_API_KEY"),
			Model:  "claude-3-haiku-20240307",
		},
		InputJSONSchema:  jsonSchemaNumbers,
		OutputJSONSchema: jsonSchemaNumber,
		Prompt:           `Sum numbers together. Output only the number.`,
	}
	errE := tool.Init(ctx)
	if errE != nil {
		log.Fatalf("% -+#.1v\n", errE)
	}

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
				// Here we provide the tool implemented with another model.
				Fun: tool.Unary(),
			},
		},
	}
	errE = f.Init(ctx)
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

	callsJSON, err := json.MarshalIndent(calls, "", "  ")
	if err != nil {
		log.Fatalf("%v\n", err)
	}
	fmt.Println(string(callsJSON))

	// Output:
	// 42
	// [
	//   {
	//     "id": "id_1",
	//     "provider": {
	//       "type": "openai",
	//       "model": "gpt-4o-mini-2024-07-18",
	//       "maxContextLength": 128000,
	//       "maxResponseLength": 16384,
	//       "forceOutputJsonSchema": false,
	//       "seed": 42,
	//       "temperature": 0
	//     },
	//     "messages": [
	//       {
	//         "role": "system",
	//         "content": "Sum numbers together. Output only the number."
	//       },
	//       {
	//         "role": "user",
	//         "content": "[38,4]"
	//       },
	//       {
	//         "role": "tool_use",
	//         "content": "{\"numbers\":[38,4]}",
	//         "toolUseId": "call_1_2",
	//         "toolUseName": "sum_numbers"
	//       },
	//       {
	//         "role": "tool_result",
	//         "content": "42",
	//         "toolUseId": "call_1_2",
	//         "toolDuration": 100004.000,
	//         "toolCalls": [
	//           {
	//             "id": "id_2",
	//             "provider": {
	//               "type": "anthropic",
	//               "model": "claude-3-haiku-20240307",
	//               "promptCaching": false,
	//               "temperature": 0
	//             },
	//             "messages": [
	//               {
	//                 "role": "system",
	//                 "content": "Sum numbers together. Output only the number."
	//               },
	//               {
	//                 "role": "user",
	//                 "content": "[{\"numbers\":[38,4]}]"
	//               },
	//               {
	//                 "role": "assistant",
	//                 "content": "42"
	//               }
	//             ],
	//             "usedTokens": {
	//               "req_2_0": {
	//                 "maxTotal": 8208,
	//                 "maxResponse": 4096,
	//                 "prompt": 26,
	//                 "response": 5,
	//                 "total": 31
	//               }
	//             },
	//             "usedTime": {
	//               "req_2_0": {
	//                 "apiCall": 1.000
	//               }
	//             },
	//             "duration": 2.000
	//           }
	//         ]
	//       },
	//       {
	//         "role": "assistant",
	//         "content": "42"
	//       }
	//     ],
	//     "usedTokens": {
	//       "req_1_0": {
	//         "maxTotal": 128000,
	//         "maxResponse": 16384,
	//         "prompt": 57,
	//         "response": 16,
	//         "total": 73
	//       },
	//       "req_1_1": {
	//         "maxTotal": 128000,
	//         "maxResponse": 16384,
	//         "prompt": 82,
	//         "response": 2,
	//         "total": 84
	//       }
	//     },
	//     "usedTime": {
	//       "req_1_0": {
	//         "apiCall": 1.000
	//       },
	//       "req_1_1": {
	//         "apiCall": 2.000
	//       }
	//     },
	//     "duration": 1.000
	//   }
	// ]
}
