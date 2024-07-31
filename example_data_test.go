package fun_test

import (
	"context"
	"fmt"
	"log"
	"os"

	"gitlab.com/tozd/go/fun"
)

func Example_data() {
	if os.Getenv("ANTHROPIC_API_KEY") == "" {
		fmt.Println("skipped")
		return
	}

	ctx := context.Background()

	f := fun.Text[[]int, int]{
		Provider: &fun.AnthropicTextProvider{
			APIKey: os.Getenv("ANTHROPIC_API_KEY"),
			Model:  "claude-3-haiku-20240307",
		},
		Data: []fun.InputOutput[[]int, int]{
			{[]int{1, 2}, 3},
			{[]int{10, 12}, 22},
			{[]int{3, 5}, 8},
		},
	}
	errE := f.Init(ctx)
	if errE != nil {
		log.Fatalln(errE)
	}

	output, errE := f.Call(ctx, []int{38, 4})
	if errE != nil {
		log.Fatalln(errE)
	}
	fmt.Println(output)

	// Output:
	// 42
}
