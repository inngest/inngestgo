package tests

import (
	"fmt"
	"net/http"
	"os"
	"os/exec"
	"syscall"
	"testing"
	"time"

	"github.com/inngest/inngestgo"
)

func TestMain(m *testing.M) {
	teardown, err := setup()
	if err != nil {
		fmt.Fprintf(os.Stderr, "failed to setup: %v\n", err)
		os.Exit(1)
	}

	code := m.Run()

	err = teardown()
	if err != nil {
		fmt.Fprintf(os.Stderr, "failed to teardown: %v\n", err)
		os.Exit(1)
	}

	os.Exit(code)
}

func setup() (func() error, error) {
	os.Setenv("INNGEST_DEV", "1")

	inngestgo.DefaultClient = inngestgo.NewClient(
		inngestgo.ClientOpts{
			EventKey: inngestgo.StrPtr("dev"),
		},
	)

	if os.Getenv("DEV_SERVER_ENABLED") == "0" {
		// Don't start the Dev Server.
		return func() error { return nil }, nil
	}

	stopDevServer, err := startDevServer()
	if err != nil {
		return nil, err
	}

	return stopDevServer, nil
}

func startDevServer() (func() error, error) {
	fmt.Println("Starting Dev Server")
	cmd := exec.Command(
		"bash",
		"-c",
		"npx --yes inngest-cli@latest dev --no-discovery --no-poll",
	)

	// Run in a new process group so we can kill the process and its children
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}

	err := cmd.Start()
	if err != nil {
		return nil, fmt.Errorf("failed to start command: %w", err)
	}

	// Wait for Dev Server to start
	fmt.Println("Waiting for Dev Server to start")
	httpClient := http.Client{Timeout: time.Second}
	start := time.Now()
	for {
		resp, err := httpClient.Get("http://0.0.0.0:8288")
		if err == nil && resp.StatusCode == 200 {
			break
		}
		if time.Since(start) > 20*time.Second {
			return nil, fmt.Errorf("timeout waiting for Dev Server to start: %w", err)
		}
		<-time.After(500 * time.Millisecond)
	}

	// Callback to stop the Dev Server
	stop := func() error {
		fmt.Println("Stopping Dev Server")
		pgid, err := syscall.Getpgid(cmd.Process.Pid)
		if err != nil {
			return fmt.Errorf("failed to get process group ID: %w", err)
		}
		if err := syscall.Kill(-pgid, syscall.SIGKILL); err != nil {
			return fmt.Errorf("failed to kill process group: %w", err)
		}
		return nil
	}

	return stop, nil
}
