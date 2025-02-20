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
	app1 := inngestgo.NewHandler("connect-app1", inngestgo.HandlerOpts{
		Logger:     logger.StdlibLogger(ctx),
		SigningKey: &key,
		AppVersion: nil,
		Dev:        inngestgo.BoolPtr(true),
	})

	{
		f := inngestgo.CreateFunction(
			inngestgo.FunctionOpts{ID: "connect-test", Name: "connect test"},
			inngestgo.EventTrigger("test/connect.run", nil),
			testRun,
		)

		app1.Register(f)
	}

	app2 := inngestgo.NewHandler("connect-app2", inngestgo.HandlerOpts{
		Logger:     logger.StdlibLogger(ctx),
		SigningKey: &key,
		AppVersion: nil,
		Dev:        inngestgo.BoolPtr(true),
	})

	{
		f := inngestgo.CreateFunction(
			inngestgo.FunctionOpts{ID: "connect-test", Name: "connect test"},
			inngestgo.EventTrigger("test/connect.run", nil),
			testRun,
		)

		app2.Register(f)
	}

	conn, err := inngestgo.Connect(ctx, inngestgo.ConnectOpts{
		InstanceID: inngestgo.Ptr("example-worker"),
		Apps: []inngestgo.Handler{
			app1,
			app2,
		},
	})

	go func() {
		<-ctx.Done()
		_ = conn.Close()
	}()

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
