package connect

import (
	"log/slog"
	"sync"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestConnectionLifecycleNewHandshakingActive(t *testing.T) {
	r := require.New(t)

	conn := newLifecycleTestConnection(nil)

	r.Equal(connPhaseNew, conn.phase())
	r.NoError(conn.transition(connPhaseHandshaking, "dialed"))
	r.NoError(conn.markActive("ready"))
	r.Equal(connPhaseActive, conn.phase())
}

func TestConnectionLifecycleRetireDisablesWritesAndNotifiesFlush(t *testing.T) {
	r := require.New(t)

	flushNotify := make(chan struct{}, 1)
	conn := newLifecycleTestConnection(flushNotify)
	r.NoError(conn.transition(connPhaseHandshaking, "dialed"))
	r.NoError(conn.markActive("ready"))

	r.True(conn.retire("read failed"))
	r.Equal(connPhaseRetired, conn.phase())

	select {
	case <-flushNotify:
	default:
		t.Fatal("expected flush notification")
	}

	r.False(conn.retire("again"))
}

func TestConnectionLifecycleDrainAndCloseTransitions(t *testing.T) {
	r := require.New(t)

	conn := newLifecycleTestConnection(nil)
	r.NoError(conn.transition(connPhaseHandshaking, "dialed"))
	r.NoError(conn.markActive("ready"))

	r.NoError(conn.beginDrain("gateway closing"))
	r.Equal(connPhaseDraining, conn.phase())
	r.True(conn.retire("replacement connected"))
	r.Equal(connPhaseRetired, conn.phase())
	r.True(conn.closeNow("done"))
	r.Equal(connPhaseClosed, conn.phase())
	r.False(conn.closeNow("again"))
}

func TestConnectionLifecycleClosingRetires(t *testing.T) {
	r := require.New(t)

	conn := newLifecycleTestConnection(nil)
	r.NoError(conn.transition(connPhaseHandshaking, "dialed"))
	r.NoError(conn.markActive("ready"))

	r.NoError(conn.beginClose("worker shutdown"))
	r.Equal(connPhaseClosing, conn.phase())
	r.True(conn.retire("worker pool drained"))
	r.Equal(connPhaseRetired, conn.phase())
}

func TestConnectionLifecycleActiveToClosedAppliesRetireEffects(t *testing.T) {
	r := require.New(t)

	flushNotify := make(chan struct{}, 1)
	conn := newLifecycleTestConnection(flushNotify)
	r.NoError(conn.transition(connPhaseHandshaking, "dialed"))
	r.NoError(conn.markActive("ready"))

	r.True(conn.closeNow("transport closed"))
	r.Equal(connPhaseClosed, conn.phase())

	select {
	case <-flushNotify:
	default:
		t.Fatal("expected flush notification")
	}
}

func TestConnectionLifecycleConcurrentCloseSideEffectsRunOnce(t *testing.T) {
	r := require.New(t)

	flushNotify := make(chan struct{}, 10)
	conn := newLifecycleTestConnection(flushNotify)
	r.NoError(conn.transition(connPhaseHandshaking, "dialed"))
	r.NoError(conn.markActive("ready"))

	var wg sync.WaitGroup
	var closedCount int
	var countLock sync.Mutex
	for range 10 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			if conn.closeNow("concurrent close") {
				countLock.Lock()
				closedCount++
				countLock.Unlock()
			}
		}()
	}
	wg.Wait()

	r.Equal(1, closedCount)
	r.Equal(connPhaseClosed, conn.phase())
	r.Len(flushNotify, 1)
}

func TestConnectionLifecycleInvalidTransitionRejected(t *testing.T) {
	r := require.New(t)

	conn := newLifecycleTestConnection(nil)

	r.Error(conn.markActive("ready before handshake"))
	r.Equal(connPhaseNew, conn.phase())
}

func newLifecycleTestConnection(flushNotify chan struct{}) *connection {
	conn := &connection{}
	conn.initLifecycle(slog.New(slog.DiscardHandler), flushNotify)
	return conn
}

func activateTestConnection(t *testing.T, conn *connection) {
	t.Helper()

	conn.initLifecycle(slog.New(slog.DiscardHandler), nil)
	require.NoError(t, conn.transition(connPhaseHandshaking, "test"))
	require.NoError(t, conn.markActive("test"))
}
