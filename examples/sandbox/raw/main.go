package main

import (
	"context"
	"errors"
	"fmt"
	"log"
	"strings"
	"time"

	"github.com/inngest/inngestgo/sandbox"
)

func run(ctx context.Context) (err error) {
	client, err := sandbox.NewClient(sandbox.ClientOpts{})
	if err != nil {
		return err
	}

	name := fmt.Sprintf("sdk-go-raw-%d", time.Now().UnixNano())
	sb, err := client.Create(ctx, sandbox.CreateOpts{Name: name, VCPU: 2, MemoryMB: 2048})
	if err != nil {
		return err
	}
	fmt.Printf("created sandbox %s\n", sb.ID)
	defer func() {
		cleanupCtx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		defer cancel()
		result, destroyErr := sb.Destroy(cleanupCtx)
		if destroyErr != nil {
			err = errors.Join(err, fmt.Errorf("destroy sandbox %s: %w", sb.ID, destroyErr))
			return
		}
		fmt.Printf("sandbox %s cleanup status: %s\n", sb.ID, result.Status)
	}()

	result, err := sb.Run(ctx, sandbox.Shell("printf 'sandbox-ok\\n' && uname -s"), sandbox.CommandOpts{})
	if err != nil {
		return err
	}
	if result.ExitCode != 0 || !strings.HasPrefix(result.Stdout, "sandbox-ok\n") {
		return fmt.Errorf("unexpected command result: exit code %d, stdout %q, stderr %q", result.ExitCode, result.Stdout, result.Stderr)
	}
	fmt.Printf("command output: %s", result.Stdout)
	return nil
}

func main() {
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Minute)
	defer cancel()
	if err := run(ctx); err != nil {
		log.Fatal(err)
	}
}
