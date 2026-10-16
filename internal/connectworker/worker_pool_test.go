package connectworker

import (
	"context"
	"sync"
	"testing"
	"time"

	connectproto "github.com/inngest/inngest/proto/gen/connect/v1"
	"github.com/stretchr/testify/require"
)

func TestWorkerPoolServesQueuedMessagesAfterStop(t *testing.T) {
	r := require.New(t)

	var (
		mu      sync.Mutex
		handled []string
	)
	started := make(chan struct{})
	release := make(chan struct{})

	var wp *workerPool
	ctx, cancel := context.WithCancel(context.Background())
	wp = NewWorkerPool(ctx, 1, func(msg workerPoolMsg) {
		defer wp.Done()
		if msg.req.RequestId == "first" {
			close(started)
			<-release
		}
		mu.Lock()
		handled = append(handled, msg.req.RequestId)
		mu.Unlock()
	})

	add := func(id string) {
		wp.Add(workerPoolMsg{req: &connectproto.GatewayExecutorRequestData{RequestId: id}})
	}

	// the only worker blocks on "first", so "queued" waits in the channel
	// when the pool stops.
	add("first")
	<-started
	add("queued")
	cancel()

	r.Eventually(func() bool {
		wp.stopLock.RLock()
		defer wp.stopLock.RUnlock()
		return wp.stopped
	}, time.Second, time.Millisecond)
	add("after-stop")
	close(release)

	waited := make(chan struct{})
	go func() {
		wp.Wait()
		close(waited)
	}()
	select {
	case <-waited:
	case <-time.After(5 * time.Second):
		t.Fatal("Wait did not return after the pool stopped")
	}

	mu.Lock()
	defer mu.Unlock()
	r.ElementsMatch([]string{"first", "queued", "after-stop"}, handled)
}
