package connect

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"log/slog"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

func TestManagerStateInitialConnectionEntersActive(t *testing.T) {
	r := require.New(t)

	h := &connectHandler{
		opts:                   Opts{IsDev: true},
		logger:                 slog.New(slog.DiscardHandler),
		notifyConnectDoneChan:  make(chan connectReport),
		notifyConnectedChan:    make(chan struct{}),
		initiateConnectionChan: make(chan struct{}, 1),
		notifyFlushChan:        make(chan struct{}, 1),
		state:                  ConnectionStateConnecting,
		messageBuffer:          newMessageBuffer(newWorkerApiClient("", nil), slog.New(slog.DiscardHandler)),
	}
	h.startConnection = func(context.Context, connectionEstablishData, ...connectOpt) {
		h.notifyConnectedChan <- struct{}{}
	}
	h.workerCtx, h.cancelWorkerCtx = context.WithCancel(context.Background())
	defer h.cancelWorkerCtx()

	connectCtx, cancelConnect := context.WithCancel(context.Background())
	defer cancelConnect()

	conn, err := h.Connect(connectCtx)
	r.NoError(err)
	r.Equal(h, conn)
	r.Equal(ConnectionStateActive, h.State())

	cancelConnect()
	r.NoError(h.Close())
}

func TestManagerStateReconnectableFailureReturnsActiveAfterReconnect(t *testing.T) {
	r := require.New(t)

	events := make(chan string, 4)
	h := &connectHandler{
		opts:                   Opts{IsDev: true},
		logger:                 slog.New(slog.DiscardHandler),
		notifyConnectDoneChan:  make(chan connectReport),
		notifyConnectedChan:    make(chan struct{}),
		initiateConnectionChan: make(chan struct{}, 1),
		notifyFlushChan:        make(chan struct{}, 1),
		state:                  ConnectionStateConnecting,
		messageBuffer:          newMessageBuffer(newWorkerApiClient("", nil), slog.New(slog.DiscardHandler)),
		reconnectBackoff: func(int) time.Duration {
			return 0
		},
	}
	var startCalls atomic.Int32
	h.startConnection = func(context.Context, connectionEstablishData, ...connectOpt) {
		call := startCalls.Add(1)
		events <- fmt.Sprintf("start-%d", call)
		h.notifyConnectedChan <- struct{}{}
	}
	h.workerCtx, h.cancelWorkerCtx = context.WithCancel(context.Background())
	defer h.cancelWorkerCtx()

	connectCtx, cancelConnect := context.WithCancel(context.Background())
	defer cancelConnect()

	_, err := h.Connect(connectCtx)
	r.NoError(err)
	r.Equal("start-1", <-events)
	r.Equal(ConnectionStateActive, h.State())

	// A reconnectable generation failure should move through RECONNECTING and
	// return to ACTIVE when the replacement generation connects.
	h.notifyConnectDoneChan <- connectReport{
		reconnect: true,
		err:       newReconnectErr(errors.New("read loop failed")),
	}

	r.Equal("start-2", <-events)
	r.Eventually(func() bool {
		return h.State() == ConnectionStateActive
	}, time.Second, time.Millisecond)

	cancelConnect()
	r.NoError(h.Close())
}

func TestManagerStateInitialRetryStaysConnectingUntilEstablished(t *testing.T) {
	r := require.New(t)

	var logOutput bytes.Buffer
	logger := slog.New(slog.NewTextHandler(&logOutput, &slog.HandlerOptions{
		Level: slog.LevelDebug,
	}))

	events := make(chan string, 4)
	releaseSecondConnect := make(chan struct{})
	var releaseOnce sync.Once
	defer releaseOnce.Do(func() {
		close(releaseSecondConnect)
	})

	h := &connectHandler{
		opts:                   Opts{IsDev: true},
		logger:                 logger,
		notifyConnectDoneChan:  make(chan connectReport),
		notifyConnectedChan:    make(chan struct{}),
		initiateConnectionChan: make(chan struct{}, 1),
		notifyFlushChan:        make(chan struct{}, 1),
		state:                  ConnectionStateConnecting,
		messageBuffer:          newMessageBuffer(newWorkerApiClient("", nil), logger),
		reconnectBackoff: func(int) time.Duration {
			return 0
		},
	}
	var startCalls atomic.Int32
	h.startConnection = func(context.Context, connectionEstablishData, ...connectOpt) {
		call := startCalls.Add(1)
		events <- fmt.Sprintf("start-%d", call)

		if call == 1 {
			h.notifyConnectDoneChan <- connectReport{
				reconnect: true,
				err:       newReconnectErr(errors.New("startup read loop failed")),
			}
			return
		}

		<-releaseSecondConnect
		h.notifyConnectedChan <- struct{}{}
	}
	h.workerCtx, h.cancelWorkerCtx = context.WithCancel(context.Background())
	defer h.cancelWorkerCtx()

	connectCtx, cancelConnect := context.WithCancel(context.Background())
	defer cancelConnect()

	connectErr := make(chan error, 1)
	go func() {
		_, err := h.Connect(connectCtx)
		connectErr <- err
	}()

	r.Equal("start-1", <-events)
	r.Equal("start-2", <-events)

	// Before the first successful websocket, retrying is still startup.
	// Public state should remain CONNECTING without logging an invalid
	// CONNECTING -> RECONNECTING transition.
	r.Equal(ConnectionStateConnecting, h.State())
	r.NotContains(logOutput.String(), "invalid worker connection state transition")
	r.NotContains(logOutput.String(), "to=RECONNECTING")

	releaseOnce.Do(func() {
		close(releaseSecondConnect)
	})

	select {
	case err := <-connectErr:
		r.NoError(err)
	case <-time.After(time.Second):
		t.Fatal("timed out waiting for initial reconnect")
	}
	r.Equal(ConnectionStateActive, h.State())

	cancelConnect()
	r.NoError(h.Close())
}

func TestManagerStateInvalidTransitionIsLoggedAndIgnored(t *testing.T) {
	r := require.New(t)

	var logOutput bytes.Buffer
	logger := slog.New(slog.NewTextHandler(&logOutput, &slog.HandlerOptions{
		Level: slog.LevelDebug,
	}))
	h := &connectHandler{
		logger: logger,
		state:  ConnectionStateActive,
	}

	// ACTIVE -> CONNECTING would mix startup state back into an established
	// manager lifecycle, so validation should leave the public state unchanged.
	h.setState(ConnectionStateConnecting, "test invalid transition")

	r.Equal(ConnectionStateActive, h.State())
	r.Contains(logOutput.String(), "invalid worker connection state transition")
	r.Contains(logOutput.String(), "from=ACTIVE")
	r.Contains(logOutput.String(), "to=CONNECTING")
}

func TestCloseIsIdempotent(t *testing.T) {
	r := require.New(t)

	releaseClose := make(chan struct{})
	h := &connectHandler{
		logger: slog.New(slog.DiscardHandler),
		state:  ConnectionStateReconnecting,
	}
	h.workerCtx, h.cancelWorkerCtx = context.WithCancel(context.Background())
	h.gracefulCloseEg.Go(func() error {
		// Hold the first close open so the second Close call exercises the
		// idempotent fast path while CLOSING is still observable.
		<-releaseClose
		return nil
	})

	done := make(chan error, 1)
	go func() {
		done <- h.Close()
	}()

	deadline := time.After(time.Second)
	for h.State() != ConnectionStateClosing {
		select {
		case <-deadline:
			t.Fatal("timed out waiting for CLOSING state")
		default:
			time.Sleep(time.Millisecond)
		}
	}

	r.NoError(h.Close())

	close(releaseClose)
	select {
	case err := <-done:
		r.NoError(err)
	case <-time.After(time.Second):
		t.Fatal("timed out waiting for first Close")
	}

	r.Equal(ConnectionStateClosed, h.State())
	r.NoError(h.Close())
}
