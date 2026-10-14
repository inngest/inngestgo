package main

import (
	"context"
	"fmt"
	"log"
	"net/http"
	"os"
	"strings"

	"github.com/inngest/inngestgo"
	"github.com/inngest/inngestgo/sandbox"
)

const eventName = "sandbox/e2e"

type result struct {
	SandboxID string `json:"sandboxId"`
	Stdout    string `json:"stdout"`
	ExitCode  int    `json:"exitCode"`
}

func run(ctx context.Context, _ inngestgo.Input[map[string]any]) (any, error) {
	return sandbox.With(ctx, "sandbox", sandbox.CreateOpts{}, func(ctx context.Context, sb *sandbox.DurableSandbox) (result, error) {
		command, err := sb.Run(ctx, "command", sandbox.Shell("printf 'sandbox-ok\\n' && uname -s"), sandbox.CommandOpts{})
		if err != nil {
			return result{}, err
		}
		if command.ExitCode != 0 || !strings.HasPrefix(command.Stdout, "sandbox-ok\n") {
			return result{}, fmt.Errorf("unexpected command result: exit code %d, stdout %q, stderr %q", command.ExitCode, command.Stdout, command.Stderr)
		}
		return result{SandboxID: sb.ID, Stdout: command.Stdout, ExitCode: command.ExitCode}, nil
	})
}

func main() {
	client, err := inngestgo.NewClient(inngestgo.ClientOpts{AppID: "sandbox-e2e-go"})
	if err != nil {
		log.Fatal(err)
	}
	_, err = inngestgo.CreateFunction(client, inngestgo.FunctionOpts{ID: "sandbox-roundtrip"}, inngestgo.EventTrigger(eventName, nil), run)
	if err != nil {
		log.Fatal(err)
	}
	if len(os.Args) == 2 && os.Args[1] == "send" {
		id, err := client.Send(context.Background(), inngestgo.GenericEvent[map[string]any]{Name: eventName, Data: map[string]any{}})
		if err != nil {
			log.Fatal(err)
		}
		fmt.Println(id)
		return
	}
	log.Fatal(http.ListenAndServe(":8090", client.Serve()))
}
