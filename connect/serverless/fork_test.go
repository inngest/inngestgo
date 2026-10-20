package serverless

import (
	"context"
	"net"
	"net/http"
	"os"
	"os/exec"
	"strconv"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

// testAppPortEnv makes the test binary serve newGoApp on that port in place
// of the tests.  the fork tests start the binary this way, so the app runs as
// a separate process, as it does in a VM.
const testAppPortEnv = "INNGEST_SERVERLESS_TEST_APP_PORT"

// forkedAppBindDelay is how long the forked app waits before it listens, so
// the readiness check polls an app that is still booting.
const forkedAppBindDelay = 500 * time.Millisecond

func TestMain(m *testing.M) {
	if port := os.Getenv(testAppPortEnv); port != "" {
		h, err := newGoApp()
		if err != nil {
			panic(err)
		}
		time.Sleep(forkedAppBindDelay)
		panic(http.ListenAndServe(net.JoinHostPort("127.0.0.1", port), h))
	}
	os.Exit(m.Run())
}

// forkedApp is the test binary running as a Go serve() app.
type forkedApp struct {
	url    string
	cmd    *exec.Cmd
	exited <-chan struct{}
}

// forkApp starts the test binary as a Go serve() app on a free port.  the
// process is killed when the test ends.
func forkApp(t *testing.T) forkedApp {
	t.Helper()

	l, err := net.Listen("tcp", "127.0.0.1:0")
	require.NoError(t, err)
	port := l.Addr().(*net.TCPAddr).Port
	require.NoError(t, l.Close())

	cmd := exec.Command(os.Args[0])
	cmd.Env = append(os.Environ(), testAppPortEnv+"="+strconv.Itoa(port))
	require.NoError(t, cmd.Start())

	exited := make(chan struct{})
	go func() {
		_ = cmd.Wait()
		close(exited)
	}()
	t.Cleanup(func() {
		_ = cmd.Process.Kill()
		<-exited
	})

	return forkedApp{
		url:    "http://127.0.0.1:" + strconv.Itoa(port) + "/api/inngest",
		cmd:    cmd,
		exited: exited,
	}
}

// TestIntrospectForkedApp reads the config of an app that runs in its own
// process and binds its port after Introspect starts.
func TestIntrospectForkedApp(t *testing.T) {
	r := require.New(t)

	app := forkApp(t)
	cfg := validConfig()
	cfg.AppURL = app.url
	cfg.BootTimeout = 30 * time.Second
	cfg.Logger = discardLogger()

	out, err := Introspect(context.Background(), cfg)
	r.NoError(err)

	r.Equal(testAppID, out.AppID)
	requireServeStepURLs(t, decodeFunctions(t, out.Functions))
}

// TestIntrospectStopsWhenAppExits checks the caller contract.  the caller cancels
// ctx when the app process exits, and Introspect returns at once instead of
// waiting out the boot timeout.
func TestIntrospectStopsWhenAppExits(t *testing.T) {
	r := require.New(t)

	app := forkApp(t)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go func() {
		<-app.exited
		cancel()
	}()

	cfg := validConfig()
	cfg.AppURL = app.url
	cfg.BootTimeout = time.Minute
	cfg.Logger = discardLogger()

	done := make(chan error, 1)
	go func() {
		_, err := Introspect(ctx, cfg)
		done <- err
	}()

	// the app is still waiting to bind, so Introspect is polling when it dies.
	time.Sleep(forkedAppBindDelay / 5)
	r.NoError(app.cmd.Process.Kill())

	select {
	case err := <-done:
		r.ErrorContains(err, "app not ready")
	case <-time.After(10 * time.Second):
		t.Fatal("Introspect did not return after the app exited")
	}
}
