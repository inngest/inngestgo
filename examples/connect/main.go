package main

import (
	"context"
	"fmt"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/inngest/inngest/pkg/logger"
	"github.com/inngest/inngestgo"
	"github.com/inngest/inngestgo/connect"
)

func main() {
	ctx, cancel := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer cancel()

	key := "signkey-test-12345678"
	c1, err := inngestgo.NewClient(inngestgo.ClientOpts{
		AppID:      "connect-app1",
		Logger:     logger.StdlibLogger(ctx),
		SigningKey: &key,
		AppVersion: nil,
		Dev:        inngestgo.BoolPtr(true),
	})
	if err != nil {
		panic(err)
	}

	{
		_, err := inngestgo.CreateFunction(
			c1,
			inngestgo.FunctionOpts{ID: "connect-test", Name: "connect test"},
			inngestgo.EventTrigger("test/connect.run", nil),
			testRun,
		)
		if err != nil {
			panic(err)
		}
	}

	c2, err := inngestgo.NewClient(inngestgo.ClientOpts{
		AppID:      "connect-app2",
		Logger:     logger.StdlibLogger(ctx),
		SigningKey: &key,
		AppVersion: nil,
		Dev:        inngestgo.BoolPtr(true),
	})
	if err != nil {
		panic(err)
	}

	{
		_, err := inngestgo.CreateFunction(
			c2,
			inngestgo.FunctionOpts{ID: "connect-test", Name: "connect test"},
			inngestgo.EventTrigger("test/connect.run", nil),
			testRun,
		)
		if err != nil {
			panic(err)
		}
	}

	conn, err := inngestgo.Connect(ctx, inngestgo.ConnectOpts{
		InstanceID: inngestgo.Ptr("example-worker"),
		Apps: []inngestgo.Client{
			c1,
			c2,
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

type TestRunEvent inngestgo.GenericEvent[any]

func testRun(ctx context.Context, input inngestgo.Input[any]) (any, error) {
	fmt.Println("HELLO")

	return "Connected!!", nil
}
