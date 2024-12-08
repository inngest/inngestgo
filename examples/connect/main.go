package main

import (
	"context"
	"fmt"
	"os"

	"github.com/inngest/inngestgo"
)

func main() {
	ctx := context.Background()

	key := "signkey-test-12345678"
	h := inngestgo.NewHandler("connect-test", inngestgo.HandlerOpts{
		SigningKey: &key,
		Dev:        inngestgo.BoolPtr(true),
	})

	f := inngestgo.CreateFunction(
		inngestgo.FunctionOpts{ID: "conntest", Name: "connect test"},
		inngestgo.EventTrigger("test/connect.run", nil),
		testRun,
	)

	h.Register(f)

	err := h.Connect(ctx)
	if err != nil {
		fmt.Printf("ERROR: %#v\n", err)
		os.Exit(1)
	}
}

type TestRunEvent inngestgo.GenericEvent[any, any]

func testRun(ctx context.Context, input inngestgo.Input[TestRunEvent]) (any, error) {
	fmt.Println("HELLO")

	return "Connected!!", nil
}
