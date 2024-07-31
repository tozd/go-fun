package fun_test

import (
	"context"
	"fmt"
	"log"

	"gitlab.com/tozd/go/errors"

	"gitlab.com/tozd/go/fun"
)

func Example_go() {
	ctx := context.Background()

	f := fun.Go[int, int]{
		Fun: func(_ context.Context, input ...int) (int, errors.E) {
			return input[0] + input[1], nil
		},
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
