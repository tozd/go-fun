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
	c := make(chan fun.TextRecorderNotification, 10000)
	fun.GetTextRecorder(ctx).Notify(c)

	var wg sync.WaitGroup
	wg.Add(1)
	go func() {
		defer wg.Done()
		for n := range c {
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
	// 42
}
