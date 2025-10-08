package fun_test

import (
	"context"
	"fmt"
	"log"
	"os"

	"gitlab.com/tozd/go/fun"
)

func ExampleText_description() {
	if os.Getenv("GROQ_API_KEY") == "" {
		fmt.Println("skipped")
		return
	}

	ctx := context.Background()

	f := fun.Text[int, int]{
		Provider: &fun.GroqTextProvider{
			APIKey: os.Getenv("GROQ_API_KEY"),
			Model:  "openai/gpt-oss-20b",
			Seed:   42,
		},
		Prompt: `Sum numbers together. Output only the number.`,
	}
	errE := f.Init(ctx)
	if errE != nil {
		log.Fatalf("% -+#.1v\n", errE)
	}

	output, errE := f.Call(ctx, 38, 4)
	if errE != nil {
		log.Fatalf("% -+#.1v\n", errE)
	}
	fmt.Println(output)

	// Output:
	// 42
}
