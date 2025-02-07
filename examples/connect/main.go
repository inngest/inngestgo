package main

import (
	"context"
	"fmt"
	"github.com/inngest/inngest/pkg/logger"
	"github.com/inngest/inngestgo"
	"github.com/inngest/inngestgo/connect"
	"os"
	"os/signal"
	"syscall"
	"time"
)

func main() {
	ctx, cancel := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer cancel()

	key := "signkey-test-12345678"
	h := inngestgo.NewHandler("connect-test", inngestgo.HandlerOpts{
		Logger:     logger.StdlibLogger(ctx),
		SigningKey: &key,
		AppVersion: nil,
		Dev:        inngestgo.BoolPtr(true),
	})

	f := inngestgo.CreateFunction(
		inngestgo.FunctionOpts{ID: "conntest", Name: "connect test"},
		inngestgo.EventTrigger("test/connect.run", nil),
		testRun,
	)

	h.Register(f)

	conn, err := h.Connect(ctx, inngestgo.ConnectOpts{
		InstanceID: inngestgo.Ptr("example-worker"),
	})
	defer func(conn connect.WorkerConnection) {
		err := conn.Close()
		if err != nil {
			fmt.Printf("Could not close: %s\n", err.Error())
		}
	}(conn)
	if err != nil {
		fmt.Printf("ERROR: %#v\n", err)
		os.Exit(1)
	}

	fmt.Println("Connected!")

	tick := time.Tick(time.Second)
	for {
		select {
		case <-ctx.Done():
			return
		case <-tick:
			fmt.Printf("Connection state: %s\n", conn.State())
		}
	}
}

type TestRunEvent inngestgo.GenericEvent[any, any]

func testRun(ctx context.Context, input inngestgo.Input[TestRunEvent]) (any, error) {
	fmt.Println("HELLO")

	return "Connected!!", nil
}
