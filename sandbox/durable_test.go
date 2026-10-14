package sandbox

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"testing"

	"github.com/inngest/inngest/pkg/enums"
	inngesterrors "github.com/inngest/inngestgo/errors"
	"github.com/inngest/inngestgo/internal/sdkrequest"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// fnContext returns a context for one request to a function.  steps run
// inline and their opcodes collect in the returned manager.  steps holds the
// state of steps from earlier requests.
func fnContext(f *fakeAPI, steps map[string]json.RawMessage, call sdkrequest.CallCtx) (context.Context, sdkrequest.InvocationManager) {
	mgr := sdkrequest.NewManager(sdkrequest.Opts{
		SigningKey:         "signkey-test-12345678",
		SigningKeyFallback: "signkey-test-aabbccdd",
		Mode:               sdkrequest.StepModeManual,
		APIBaseURL:         f.srv.URL,
		Request:            &sdkrequest.Request{Steps: steps, CallCtx: call},
	})
	return sdkrequest.SetManager(context.Background(), mgr), mgr
}

// stateFrom converts opcodes into the step state the executor sends on the
// next request.
func stateFrom(t *testing.T, ops []sdkrequest.GeneratorOpcode) map[string]json.RawMessage {
	t.Helper()
	steps := map[string]json.RawMessage{}
	for _, op := range ops {
		if op.Error != nil {
			byt, err := json.Marshal(map[string]any{"error": op.Error})
			require.NoError(t, err)
			steps[op.ID] = byt
			continue
		}
		steps[op.ID] = op.Data
	}
	return steps
}

func opNames(ops []sdkrequest.GeneratorOpcode) []string {
	names := make([]string, len(ops))
	for i, op := range ops {
		names[i] = op.Name
	}
	return names
}

func TestDurableReplay(t *testing.T) {
	f := newFakeAPI(t)

	// fn is the body of an Inngest function.
	fn := func(ctx context.Context) (CommandResult, *DurableSnapshot, DestroyResult) {
		sb, err := Create(ctx, "create", CreateOpts{Name: "box", VCPU: 2, MemoryMB: 2048})
		require.NoError(t, err)
		res, err := sb.Run(ctx, "build", Shell("make"), CommandOpts{})
		require.NoError(t, err)
		snap, err := sb.Snapshot(ctx, "snap", SnapshotOpts{Name: "base"})
		require.NoError(t, err)
		destroyed, err := sb.Destroy(ctx, "destroy")
		require.NoError(t, err)
		return res, snap, destroyed
	}

	ctx, mgr := fnContext(f, nil, sdkrequest.CallCtx{})
	res, snap, destroyed := fn(ctx)
	assert.Equal(t, "/bin/sh -c make|30000ms", res.Stdout)
	assert.Equal(t, SnapshotReady, snap.Status)
	assert.Equal(t, StatusTerminating, destroyed.Status)
	assert.Equal(t, []string{"create", "build", "snap", "snap:wait-until-ready", "destroy"}, opNames(mgr.Ops()))
	for _, op := range mgr.Ops() {
		assert.Equal(t, enums.OpcodeStepRun, op.Op)
	}
	hashed, err := hashKey("signkey-test-12345678")
	require.NoError(t, err)
	assert.Equal(t, "Bearer "+hashed, f.auth[0], "durable steps hash the run's signing key")

	calls := len(f.auth)
	ctx, mgr = fnContext(f, stateFrom(t, mgr.Ops()), sdkrequest.CallCtx{})
	res2, snap2, destroyed2 := fn(ctx)
	assert.Equal(t, calls, len(f.auth), "a replay must not call the API")
	assert.Empty(t, mgr.Ops())
	assert.Equal(t, res, res2)
	assert.Equal(t, snap.ID, snap2.ID)
	assert.Equal(t, destroyed.Status, destroyed2.Status)
}

func TestDurableFallbackSigningKey(t *testing.T) {
	f := newFakeAPI(t)
	f.failNext("POST /v2/sandboxes", 401, string(CodeAccessDenied))

	ctx, _ := fnContext(f, nil, sdkrequest.CallCtx{})
	_, err := Create(ctx, "create", CreateOpts{Name: "box", VCPU: 1, MemoryMB: 512, NoWait: true})
	require.NoError(t, err)
	require.Len(t, f.auth, 2)

	primary, err := hashKey("signkey-test-12345678")
	require.NoError(t, err)
	fallback, err := hashKey("signkey-test-aabbccdd")
	require.NoError(t, err)
	assert.Equal(t, "Bearer "+primary, f.auth[0])
	assert.Equal(t, "Bearer "+fallback, f.auth[1])
}

func TestDurableRunBinaryOutput(t *testing.T) {
	f := newFakeAPI(t)
	f.execStdout = []byte{0xff, 0xfe, 'x'}

	ctx, mgr := fnContext(f, nil, sdkrequest.CallCtx{})
	sb, err := Create(ctx, "create", CreateOpts{Name: "box", VCPU: 1, MemoryMB: 512})
	require.NoError(t, err)
	_, err = sb.Run(ctx, "run", Argv("/bin/cat", "/bin/ls"), CommandOpts{})
	require.NoError(t, err)

	// bytes that are not UTF-8 survive the step state.
	ctx, _ = fnContext(f, stateFrom(t, mgr.Ops()), sdkrequest.CallCtx{})
	sb, err = Create(ctx, "create", CreateOpts{Name: "box", VCPU: 1, MemoryMB: 512})
	require.NoError(t, err)
	res, err := sb.Run(ctx, "run", Argv("/bin/cat", "/bin/ls"), CommandOpts{})
	require.NoError(t, err)
	assert.Equal(t, string([]byte{0xff, 0xfe, 'x'}), res.Stdout)
}

func TestDurableRunTruncatesOutput(t *testing.T) {
	f := newFakeAPI(t)
	f.execStdout = append(bytes.Repeat([]byte("a"), 3<<20), []byte("END")...)

	ctx, _ := fnContext(f, nil, sdkrequest.CallCtx{})
	sb, err := Create(ctx, "create", CreateOpts{Name: "box", VCPU: 1, MemoryMB: 512})
	require.NoError(t, err)
	res, err := sb.Run(ctx, "run", Shell("yes"), CommandOpts{})
	require.NoError(t, err)
	assert.True(t, res.Output.Truncated)
	assert.Equal(t, "tail", res.Output.Strategy)
	assert.Len(t, res.Stdout+res.Stderr, maxDurableOutputBytes)
	assert.Equal(t, "warn", res.Stderr, "stderr keeps everything it fits")
	assert.Equal(t, "END", res.Stdout[len(res.Stdout)-3:], "the end of the output is kept")
}

func TestDurableErrors(t *testing.T) {
	t.Run("a final API error fails the step and keeps its code on replay", func(t *testing.T) {
		f := newFakeAPI(t)
		fn := func(ctx context.Context) error {
			sb, err := Create(ctx, "create", CreateOpts{Name: "box", VCPU: 1, MemoryMB: 512})
			require.NoError(t, err)
			_, err = sb.Run(ctx, "run", Shell("true"), CommandOpts{})
			return err
		}

		f.failNext("POST /v2/sandboxes/{id}/exec", 404, string(CodeNotFound))
		ctx, mgr := fnContext(f, nil, sdkrequest.CallCtx{})
		err := fn(ctx)
		assert.True(t, inngesterrors.IsNoRetryError(err))
		assert.True(t, IsNotFound(err))
		ops := mgr.Ops()
		require.Len(t, ops, 2)
		assert.Equal(t, enums.OpcodeStepFailed, ops[1].Op)

		ctx, _ = fnContext(f, stateFrom(t, ops), sdkrequest.CallCtx{})
		err = fn(ctx)
		var se *Error
		require.ErrorAs(t, err, &se)
		assert.Equal(t, CodeNotFound, se.Code)
		assert.Equal(t, ActionExec, se.Action)
		assert.True(t, inngesterrors.IsStepError(err), "the replayed error is still a step error")
	})

	t.Run("a retryable API error retries the step", func(t *testing.T) {
		f := newFakeAPI(t)
		ctx, mgr := fnContext(f, nil, sdkrequest.CallCtx{})
		sb, err := Create(ctx, "create", CreateOpts{Name: "box", VCPU: 1, MemoryMB: 512})
		require.NoError(t, err)

		f.failNext("POST /v2/sandboxes/{id}/exec", 503, string(CodeComputeUnavailable))
		_, err = sb.Run(ctx, "run", Shell("true"), CommandOpts{})
		var se *Error
		require.ErrorAs(t, err, &se)
		assert.True(t, se.Retryable)
		assert.False(t, inngesterrors.IsNoRetryError(err))
		assert.Equal(t, enums.OpcodeStepError, mgr.Ops()[1].Op)
	})

	t.Run("invalid input fails before any step", func(t *testing.T) {
		f := newFakeAPI(t)
		ctx, mgr := fnContext(f, nil, sdkrequest.CallCtx{})
		sb := &DurableSandbox{SandboxInfo{ID: "8b8d6a43-5d3f-4a62-9a9e-2a4a5f6c7d8e"}}
		_, err := sb.Run(ctx, "run", Command{}, CommandOpts{})
		var ve *ValidationError
		require.ErrorAs(t, err, &ve)
		assert.Empty(t, mgr.Ops())
		assert.Empty(t, f.auth)
	})

	t.Run("destroying a missing sandbox succeeds", func(t *testing.T) {
		f := newFakeAPI(t)
		ctx, _ := fnContext(f, nil, sdkrequest.CallCtx{})
		sb := &DurableSandbox{SandboxInfo{ID: "8b8d6a43-5d3f-4a62-9a9e-2a4a5f6c7d8e"}}
		res, err := sb.Destroy(ctx, "destroy")
		require.NoError(t, err)
		assert.Equal(t, StatusTerminated, res.Status)
	})
}

func TestDurableProcessesAndSnapshots(t *testing.T) {
	f := newFakeAPI(t)
	ctx, _ := fnContext(f, nil, sdkrequest.CallCtx{})

	sb, err := Create(ctx, "create", CreateOpts{Name: "box", VCPU: 1, MemoryMB: 512})
	require.NoError(t, err)

	p, err := sb.StartProcess(ctx, "start", ProcessOpts{Command: Shell("sleep 60")})
	require.NoError(t, err)
	got, err := sb.GetProcess(ctx, "get", p.ID)
	require.NoError(t, err)
	assert.Equal(t, p.ID, got.ID)
	list, err := sb.ListProcesses(ctx, "list", ListOpts{})
	require.NoError(t, err)
	assert.Len(t, list.Items, 1)
	out, err := p.Output(ctx, "output", OutputOpts{TailBytes: 10})
	require.NoError(t, err)
	assert.Equal(t, "hello", string(out.Chunks[0].Data))
	done, err := p.Wait(ctx, "wait", WaitOpts{})
	require.NoError(t, err)
	assert.Equal(t, ProcessExited, done.State)
	require.NoError(t, p.Signal(ctx, "signal", SignalOpts{Signal: 9}))

	paused, err := sb.Pause(ctx, "pause", LifecycleOpts{})
	require.NoError(t, err)
	assert.Equal(t, StatusPaused, paused.Status)
	resumed, err := paused.Resume(ctx, "resume", LifecycleOpts{})
	require.NoError(t, err)
	assert.Equal(t, StatusRunning, resumed.Status)

	snap, err := sb.Snapshot(ctx, "snap", SnapshotOpts{})
	require.NoError(t, err)
	snaps, err := ListSnapshots(ctx, "snaps", SnapshotListOpts{})
	require.NoError(t, err)
	assert.Len(t, snaps.Items, 1)
	clone, err := snap.Clone(ctx, "clone", CloneOpts{Name: "clone"})
	require.NoError(t, err)
	assert.Equal(t, StatusRunning, clone.Status)
	require.NoError(t, snap.Delete(ctx, "delete-snap"))
	gone, err := GetSnapshot(ctx, "get-snap", snap.ID)
	require.NoError(t, err)
	assert.Nil(t, gone)

	all, err := List(ctx, "list-sandboxes", ListOpts{})
	require.NoError(t, err)
	assert.Len(t, all.Items, 2)
	loaded, err := Get(ctx, "get-sandbox", sb.ID)
	require.NoError(t, err)
	assert.Equal(t, sb.ID, loaded.ID)
}

func TestWith(t *testing.T) {
	maxAttempts := 3
	tests := []struct {
		name      string
		attempt   int
		err       error
		destroyed bool
	}{
		{"success destroys", 0, nil, true},
		{"a retryable error keeps the sandbox", 0, errors.New("boom"), false},
		{"a no-retry error destroys", 0, inngesterrors.NoRetryError(errors.New("boom")), true},
		{"a step error destroys", 0, inngesterrors.StepError{Message: "boom"}, true},
		{"the last attempt destroys", 2, errors.New("boom"), true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			f := newFakeAPI(t)
			ctx, mgr := fnContext(f, nil, sdkrequest.CallCtx{RunID: "run1", Attempt: tt.attempt, MaxAttempts: &maxAttempts})

			var name string
			out, err := With(ctx, "build", CreateOpts{}, func(ctx context.Context, sb *DurableSandbox) (string, error) {
				name = sb.Name
				assert.Equal(t, Resources{VCPU: 2, MemoryMB: 2048}, sb.Resources)
				return "out", tt.err
			})
			assert.Equal(t, "out", out)
			assert.Equal(t, tt.err, err)
			assert.Equal(t, "run1-build", name)

			want := []string{"build"}
			if tt.destroyed {
				want = append(want, "build:destroy")
			}
			assert.Equal(t, want, opNames(mgr.Ops()))
		})
	}
}

func TestFitOutput(t *testing.T) {
	half := maxDurableOutputBytes / 2
	tests := []struct {
		name             string
		stdout, stderr   int
		keepOut, keepErr int
		truncated        bool
	}{
		{"fits", 10, 10, 10, 10, false},
		{"exactly fits", half, half, half, half, false},
		{"stdout gets unused stderr space", 3 * half, 10, 2*half - 10, 10, true},
		{"stderr gets unused stdout space", 10, 3 * half, 10, 2*half - 10, true},
		{"both overflow", 2 * half, 2 * half, half, half, true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			out, errOut, info := fitOutput(make([]byte, tt.stdout), make([]byte, tt.stderr))
			assert.Len(t, out, tt.keepOut)
			assert.Len(t, errOut, tt.keepErr)
			assert.Equal(t, tt.truncated, info.Truncated)
			if tt.truncated {
				assert.Equal(t, StreamBytes{tt.stdout, tt.stderr}, *info.OriginalBytes)
				assert.Equal(t, StreamBytes{tt.keepOut, tt.keepErr}, *info.RetainedBytes)
			}
		})
	}
}
