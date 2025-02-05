package main

import (
	"context"
	"fmt"
	"os"

	"github.com/inngest/inngest/pkg/logger"
	"github.com/inngest/inngestgo"
)

func main() {
	ctx := context.Background()

	key := "signkey-test-12345678"
	c, err := inngestgo.NewClient(inngestgo.ClientOpts{AppID: "connect-test"})
	if err != nil {
		panic(err)
	}
	h := inngestgo.NewHandler(c, inngestgo.HandlerOpts{
		Logger:     logger.StdlibLogger(ctx),
		SigningKey: &key,
		InstanceId: inngestgo.Ptr("example-worker"),
		BuildId:    nil,
		Dev:        inngestgo.BoolPtr(true),
	})

	f, err := inngestgo.CreateFunction(
		c,
		inngestgo.FunctionOpts{ID: "conntest", Name: "connect test"},
		inngestgo.EventTrigger("test/connect.run", nil),
		testRun,
	)
	if err != nil {
		panic(err)
	}

	h.Register(f)

	err = h.Connect(ctx)
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
