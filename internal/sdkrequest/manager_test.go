package sdkrequest

import (
	"context"
	"fmt"
	"runtime"
	"sync"
	"testing"

	"github.com/inngest/inngest/pkg/enums"
	internalcheckpoint "github.com/inngest/inngestgo/internal/checkpoint"
	"github.com/inngest/inngestgo/internal/fn"
	"github.com/inngest/inngestgo/internal/opcode"
	publiccheckpoint "github.com/inngest/inngestgo/pkg/checkpoint"
	"github.com/stretchr/testify/require"
)

type mockFn struct {
	fn.ServableFunction
}

func (mockFn) Config() fn.FunctionOpts {
	return fn.FunctionOpts{Checkpoint: &publiccheckpoint.Config{}}
}

// backgroundCommitCheckpointer models the interval-based checkpointer: steps
// are committed from another goroutine while request execution may continue.
type backgroundCommitCheckpointer struct {
	wg sync.WaitGroup
}

func (c *backgroundCommitCheckpointer) WithStep(_ context.Context, step opcode.Step, cb internalcheckpoint.Callback) {
	c.wg.Add(1)
	go func() {
		defer c.wg.Done()

		// Yield so the callback overlaps with AppendOp/Ops under -race.
		runtime.Gosched()

		cb([]opcode.Step{step}, nil)
	}()
}

func (c *backgroundCommitCheckpointer) Close() {
	c.wg.Wait()
}

func TestRequestCtxManager(t *testing.T) {
	// The manager's pending op buffer feeds the SDK response. Checkpointing can
	// remove committed ops from that same buffer, so ownership and
	// synchronization need to be explicit.
	t.Run("Ops returns a copy so response code cannot mutate the pending op buffer", func(t *testing.T) {
		r := require.New(t)

		mgr := &requestCtxManager{
			l: &sync.RWMutex{},
			ops: []GeneratorOpcode{{
				ID: "original",
				Op: enums.OpcodeStepRun,
			}},
		}

		ops := mgr.Ops()
		r.Len(ops, 1)
		ops[0].ID = "mutated"
		r.Equal("original", mgr.Ops()[0].ID)
	})

	// Interval-based checkpoint commits happen in the background while the same
	// request may continue appending ops and reading them to build a response.
	// This test must be run with -race to catch unsynchronized access between
	// AppendOp, Ops, and the checkpoint callback that removes committed ops.
	t.Run("background checkpoint commits can remove ops while the request appends and reads", func(t *testing.T) {
		r := require.New(t)
		ctx := context.Background()

		checkpointer := backgroundCommitCheckpointer{}
		mgr := requestCtxManager{
			checkpointer: &checkpointer,
			fn:           mockFn{},
			l:            &sync.RWMutex{},
			mode:         StepModeCheckpoint,
			request:      &Request{},
		}

		// Keep a reader active while the main goroutine appends ops. This
		// represents response-building code reading pending ops during
		// execution.
		stopReader := make(chan struct{})
		readerStarted := make(chan struct{})
		var readerWG sync.WaitGroup
		readerWG.Add(1)
		go func() {
			defer readerWG.Done()

			// Signal that concurrent reads are active before appends begin.
			close(readerStarted)
			for {
				select {
				case <-stopReader:
					return
				default:
					// Repeatedly read the pending response buffer while
					// AppendOp and checkpoint callbacks may write to it.
					_ = mgr.Ops()

					// Yield so the reader keeps interleaving with AppendOp and
					// callbacks.
					runtime.Gosched()
				}
			}
		}()
		// Avoid a false pass where all appends complete before the reader
		// starts.
		<-readerStarted

		const opCount = 1000
		for i := range opCount {
			mgr.AppendOp(ctx, opcode.Step{
				ID: fmt.Sprintf("step-%d", i),
				Op: enums.OpcodeStepRun,
			})
		}

		checkpointer.Close()
		close(stopReader)
		readerWG.Wait()
		r.Empty(mgr.Ops())
	})
}
