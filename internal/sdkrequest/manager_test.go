package sdkrequest

import (
	"context"
	"runtime"
	"sync"
	"testing"

	"github.com/inngest/inngest/pkg/enums"
	internalcheckpoint "github.com/inngest/inngestgo/internal/checkpoint"
	"github.com/inngest/inngestgo/internal/fn"
	"github.com/inngest/inngestgo/internal/opcode"
)

func TestOpsConcurrentCheckpointRemoval(t *testing.T) {
	oldProcs := runtime.GOMAXPROCS(4)
	t.Cleanup(func() {
		runtime.GOMAXPROCS(oldProcs)
	})

	mgr := NewManager(Opts{
		Mode: StepModeCheckpoint,
		Fn: testServableFunction{
			config: fn.FunctionOpts{
				Checkpoint: &internalcheckpoint.Config{},
			},
		},
		Request: &Request{},
	})

	requestMgr, ok := mgr.(*requestCtxManager)
	if !ok {
		t.Fatalf("expected *requestCtxManager, got %T", mgr)
	}

	checkpointer := &concurrentCallbackCheckpointer{
		iterations: 10_000,
		ready:      make(chan struct{}),
		release:    make(chan struct{}),
		done:       make(chan struct{}),
	}
	requestMgr.checkpointer = checkpointer

	mgr.AppendOp(context.Background(), GeneratorOpcode{
		Op: enums.OpcodeStepRun,
		ID: "step-1",
	})

	<-checkpointer.ready

	var readers sync.WaitGroup
	for i := 0; i < 8; i++ {
		readers.Add(1)
		go func() {
			defer readers.Done()

			for {
				select {
				case <-checkpointer.done:
					return
				default:
					// Run with -race: without opsLock, this read races with the
					// checkpoint callback deleting from requestCtxManager.ops.
					_ = mgr.Ops()
					runtime.Gosched()
				}
			}
		}()
	}

	close(checkpointer.release)
	<-checkpointer.done
	readers.Wait()
}

type concurrentCallbackCheckpointer struct {
	iterations int
	ready      chan struct{}
	release    chan struct{}
	done       chan struct{}
}

func (c *concurrentCallbackCheckpointer) WithStep(ctx context.Context, step opcode.Step, cb internalcheckpoint.Callback) {
	go func() {
		close(c.ready)
		<-c.release

		for i := 0; i < c.iterations; i++ {
			cb([]opcode.Step{step}, nil)
			runtime.Gosched()
		}

		close(c.done)
	}()
}

func (c *concurrentCallbackCheckpointer) Close() {}

type testServableFunction struct {
	config fn.FunctionOpts
}

func (f testServableFunction) FullyQualifiedID() string {
	return ""
}

func (f testServableFunction) ID() string {
	return ""
}

func (f testServableFunction) Name() string {
	return ""
}

func (f testServableFunction) Config() fn.FunctionOpts {
	return f.config
}

func (f testServableFunction) Trigger() fn.Triggerable {
	return fn.MultipleTriggers{}
}

func (f testServableFunction) ZeroEvent() any {
	return nil
}

func (f testServableFunction) Func() any {
	return nil
}
