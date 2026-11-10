package main

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"time"

	"github.com/inngest/inngestgo"
	"github.com/inngest/inngestgo/loop"
	"github.com/inngest/inngestgo/pkg/checkpoint"
	"github.com/inngest/inngestgo/step"
)

func main() {
	c, err := inngestgo.NewClient(inngestgo.ClientOpts{
		AppID: "loop-example",
	})
	if err != nil {
		panic(err)
	}

	type S struct {
		Name  string
		Age   int
		State string // "rolling" | "canary" | "healthy"
	}

	type E struct {
		Name  string
		Email string
	}

	f, err := inngestgo.CreateLoop(
		c,
		inngestgo.LoopOpts[S]{
			FunctionOpts: inngestgo.FunctionOpts{
				Checkpoint: checkpoint.ConfigSafe,
				ID:         "loop-test",
			},
			Tick:    10 * time.Second,
			Key:     "event.data.name",
			Initial: S{},
		},
		inngestgo.EventTrigger("foo", nil),
		func(ctx context.Context, input inngestgo.LoopInput[E], state *S) error {
			debug("state", state)

			step.Run(ctx, "load-data", func(context.Context) (any, error) {
				// you can run steps as usual.  just, don't modify
				// state in a step.
				return nil, nil
			})

			loop.Subscribe(ctx, "foo", "event/foo.bar")

			state.Age += 1
			state.Name = input.Event.Name

			fmt.Println("done")
			fmt.Println("")
			return nil
		},
	)
	if err != nil {
		panic(err)
	}
	_ = f

	fmt.Print("listening on :8080")
	_ = http.ListenAndServe(":8080", c.Serve())
}

func debug(s string, i any) {
	b, _ := json.MarshalIndent(i, "", "  ")
	fmt.Printf("%s: %s\n", s, b)
}
