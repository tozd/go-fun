package fun_test

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"os"
	"sync"

	"gitlab.com/tozd/go/fun"
)

func ExampleTextRecorder_Notify() {
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

	// We want to be notified as soon as a message is received or send.
	c := make(chan []fun.TextRecorderCall)
	fun.GetTextRecorder(ctx).Notify(c)

	var wg sync.WaitGroup
	wg.Add(1)
	go func() {
		defer wg.Done()
		for n := range c {
			// We change calls a bit for the example to be deterministic.
			cleanCalls(n)

			nJSON, err := json.MarshalIndent(n, "", "  ")
			if err != nil {
				log.Fatalf("%v\n", err)
			}
			fmt.Println(string(nJSON))
		}
	}()

	output, errE := f.Call(ctx, 38, 4)
	if errE != nil {
		log.Fatalf("% -+#.1v\n", errE)
	}

	close(c)
	wg.Wait()

	fmt.Println(output)

	// Output:
	// [
	//   {
	//     "id": "id_1",
	//     "provider": {
	//       "model": "gpt-4o-mini-2024-07-18",
	//       "maxContextLength": 128000,
	//       "maxResponseLength": 16384,
	//       "maxExchanges": 10,
	//       "forceOutputJsonSchema": false,
	//       "seed": 42,
	//       "temperature": 0,
	//       "type": "openai"
	//     },
	//     "messages": [
	//       {
	//         "role": "system",
	//         "content": "Sum numbers together. Output only the number."
	//       },
	//       {
	//         "role": "user",
	//         "content": "[38,4]"
	//       }
	//     ],
	//     "duration": 1.000
	//   }
	// ]
	// [
	//   {
	//     "id": "id_1",
	//     "provider": {
	//       "model": "gpt-4o-mini-2024-07-18",
	//       "maxContextLength": 128000,
	//       "maxResponseLength": 16384,
	//       "maxExchanges": 10,
	//       "forceOutputJsonSchema": false,
	//       "seed": 42,
	//       "temperature": 0,
	//       "type": "openai"
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
	//       }
	//     ],
	//     "usedTokens": {
	//       "req_1_0": {
	//         "maxTotal": 128000,
	//         "maxResponse": 16384,
	//         "prompt": 57,
	//         "response": 16,
	//         "total": 73
	//       }
	//     },
	//     "usedTime": {
	//       "req_1_0": {
	//         "apiCall": 1.000
	//       }
	//     },
	//     "duration": 1.000
	//   }
	// ]
	// [
	//   {
	//     "id": "id_1",
	//     "provider": {
	//       "model": "gpt-4o-mini-2024-07-18",
	//       "maxContextLength": 128000,
	//       "maxResponseLength": 16384,
	//       "maxExchanges": 10,
	//       "forceOutputJsonSchema": false,
	//       "seed": 42,
	//       "temperature": 0,
	//       "type": "openai"
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
	//         "toolUseId": "call_1_2",
	//         "toolDuration": 100004.000,
	//         "toolCalls": [
	//           {
	//             "id": "id_2",
	//             "provider": {
	//               "model": "claude-3-haiku-20240307",
	//               "maxContextLength": 200000,
	//               "maxResponseLength": 4096,
	//               "maxExchanges": 10,
	//               "promptCaching": false,
	//               "reasoningBudget": 0,
	//               "temperature": 0,
	//               "type": "anthropic"
	//             },
	//             "messages": [
	//               {
	//                 "role": "system",
	//                 "content": "Sum numbers together. Output only the number."
	//               },
	//               {
	//                 "role": "user",
	//                 "content": "{\"numbers\":[38,4]}"
	//               }
	//             ],
	//             "duration": 2.000
	//           }
	//         ]
	//       }
	//     ],
	//     "usedTokens": {
	//       "req_1_0": {
	//         "maxTotal": 128000,
	//         "maxResponse": 16384,
	//         "prompt": 57,
	//         "response": 16,
	//         "total": 73
	//       }
	//     },
	//     "usedTime": {
	//       "req_1_0": {
	//         "apiCall": 1.000
	//       }
	//     },
	//     "duration": 1.000
	//   }
	// ]
	// [
	//   {
	//     "id": "id_1",
	//     "provider": {
	//       "model": "gpt-4o-mini-2024-07-18",
	//       "maxContextLength": 128000,
	//       "maxResponseLength": 16384,
	//       "maxExchanges": 10,
	//       "forceOutputJsonSchema": false,
	//       "seed": 42,
	//       "temperature": 0,
	//       "type": "openai"
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
	//         "toolUseId": "call_1_2",
	//         "toolDuration": 100004.000,
	//         "toolCalls": [
	//           {
	//             "id": "id_2",
	//             "provider": {
	//               "model": "claude-3-haiku-20240307",
	//               "maxContextLength": 200000,
	//               "maxResponseLength": 4096,
	//               "maxExchanges": 10,
	//               "promptCaching": false,
	//               "reasoningBudget": 0,
	//               "temperature": 0,
	//               "type": "anthropic"
	//             },
	//             "messages": [
	//               {
	//                 "role": "system",
	//                 "content": "Sum numbers together. Output only the number."
	//               },
	//               {
	//                 "role": "user",
	//                 "content": "{\"numbers\":[38,4]}"
	//               },
	//               {
	//                 "role": "assistant",
	//                 "content": "42"
	//               }
	//             ],
	//             "usedTokens": {
	//               "req_2_0": {
	//                 "maxTotal": 200000,
	//                 "maxResponse": 4096,
	//                 "prompt": 24,
	//                 "response": 5,
	//                 "total": 29,
	//                 "cacheCreationInputTokens": 0,
	//                 "cacheReadInputTokens": 0
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
	//       }
	//     ],
	//     "usedTokens": {
	//       "req_1_0": {
	//         "maxTotal": 128000,
	//         "maxResponse": 16384,
	//         "prompt": 57,
	//         "response": 16,
	//         "total": 73
	//       }
	//     },
	//     "usedTime": {
	//       "req_1_0": {
	//         "apiCall": 1.000
	//       }
	//     },
	//     "duration": 1.000
	//   }
	// ]
	// [
	//   {
	//     "id": "id_1",
	//     "provider": {
	//       "model": "gpt-4o-mini-2024-07-18",
	//       "maxContextLength": 128000,
	//       "maxResponseLength": 16384,
	//       "maxExchanges": 10,
	//       "forceOutputJsonSchema": false,
	//       "seed": 42,
	//       "temperature": 0,
	//       "type": "openai"
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
	//               "model": "claude-3-haiku-20240307",
	//               "maxContextLength": 200000,
	//               "maxResponseLength": 4096,
	//               "maxExchanges": 10,
	//               "promptCaching": false,
	//               "reasoningBudget": 0,
	//               "temperature": 0,
	//               "type": "anthropic"
	//             },
	//             "messages": [
	//               {
	//                 "role": "system",
	//                 "content": "Sum numbers together. Output only the number."
	//               },
	//               {
	//                 "role": "user",
	//                 "content": "{\"numbers\":[38,4]}"
	//               },
	//               {
	//                 "role": "assistant",
	//                 "content": "42"
	//               }
	//             ],
	//             "usedTokens": {
	//               "req_2_0": {
	//                 "maxTotal": 200000,
	//                 "maxResponse": 4096,
	//                 "prompt": 24,
	//                 "response": 5,
	//                 "total": 29,
	//                 "cacheCreationInputTokens": 0,
	//                 "cacheReadInputTokens": 0
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
	//       }
	//     ],
	//     "usedTokens": {
	//       "req_1_0": {
	//         "maxTotal": 128000,
	//         "maxResponse": 16384,
	//         "prompt": 57,
	//         "response": 16,
	//         "total": 73
	//       }
	//     },
	//     "usedTime": {
	//       "req_1_0": {
	//         "apiCall": 1.000
	//       }
	//     },
	//     "duration": 1.000
	//   }
	// ]
	// [
	//   {
	//     "id": "id_1",
	//     "provider": {
	//       "model": "gpt-4o-mini-2024-07-18",
	//       "maxContextLength": 128000,
	//       "maxResponseLength": 16384,
	//       "maxExchanges": 10,
	//       "forceOutputJsonSchema": false,
	//       "seed": 42,
	//       "temperature": 0,
	//       "type": "openai"
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
	//               "model": "claude-3-haiku-20240307",
	//               "maxContextLength": 200000,
	//               "maxResponseLength": 4096,
	//               "maxExchanges": 10,
	//               "promptCaching": false,
	//               "reasoningBudget": 0,
	//               "temperature": 0,
	//               "type": "anthropic"
	//             },
	//             "messages": [
	//               {
	//                 "role": "system",
	//                 "content": "Sum numbers together. Output only the number."
	//               },
	//               {
	//                 "role": "user",
	//                 "content": "{\"numbers\":[38,4]}"
	//               },
	//               {
	//                 "role": "assistant",
	//                 "content": "42"
	//               }
	//             ],
	//             "usedTokens": {
	//               "req_2_0": {
	//                 "maxTotal": 200000,
	//                 "maxResponse": 4096,
	//                 "prompt": 24,
	//                 "response": 5,
	//                 "total": 29,
	//                 "cacheCreationInputTokens": 0,
	//                 "cacheReadInputTokens": 0
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
	// 42
}
