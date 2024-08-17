package fun_test

import (
	"context"
	"fmt"
	"log"
	"os"

	"gitlab.com/tozd/go/fun"
)

func Example_description() {
	if os.Getenv("GROQ_API_KEY") == "" {
		fmt.Println("skipped")
		return
	}

	ctx := context.Background()

	f := fun.Text[int, int]{
		Provider: &fun.GroqTextProvider{
			APIKey: os.Getenv("GROQ_API_KEY"),
			Model:  "llama3-8b-8192",
			Seed:   42,
		},
		Prompt: `Sum numbers together. Output only the number.`,
	}
	errE := f.Init(ctx)
	if errE != nil {
		log.Fatalln(errE)
	}

	output, errE := f.Call(ctx, 38, 4)
	if errE != nil {
		log.Fatalln(errE)
	}
	fmt.Println(output)

	// Output:
	// 42
}
