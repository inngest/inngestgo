package main

import (
	"context"
	"fmt"
	"github.com/inngest/inngest/pkg/logger"
	"github.com/inngest/inngestgo"
	"os"
	"os/signal"
	"syscall"
)

func main() {
	ctx, cancel := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer cancel()

	key := "signkey-test-12345678"
	h := inngestgo.NewHandler("connect-test", inngestgo.HandlerOpts{
		Logger:     logger.StdlibLogger(ctx),
		SigningKey: &key,
		InstanceId: inngestgo.Ptr("example-worker"),
		BuildId:    nil,
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
