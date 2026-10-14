package sandbox

import (
	"context"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestClientLifecycle(t *testing.T) {
	ctx := context.Background()
	f := newFakeAPI(t)
	c := f.client(t)

	sb, err := c.Create(ctx, CreateOpts{Name: "box", VCPU: 2, MemoryMB: 2048, Environment: map[string]string{"A": "1"}})
	require.NoError(t, err)
	assert.Equal(t, StatusRunning, sb.Status, "create waits for RUNNING")
	assert.Equal(t, 1, f.count("GET /v2/sandboxes/{id}"))
	assert.Contains(t, f.auth[0], "Bearer signkey-test-", "the client sends the hashed key")
	assert.NotContains(t, f.auth[0], "12345678", "the client never sends the raw key")

	t.Run("run", func(t *testing.T) {
		res, err := sb.Run(ctx, Shell("echo hi"), CommandOpts{})
		require.NoError(t, err)
		assert.Equal(t, "/bin/sh -c echo hi|30000ms", res.Stdout)
		assert.Equal(t, "warn", res.Stderr)
		assert.Equal(t, 3, res.ExitCode)
	})

	t.Run("processes", func(t *testing.T) {
		p, err := sb.StartProcess(ctx, ProcessOpts{Command: Argv("/usr/bin/sleep", "60")})
		require.NoError(t, err)
		assert.Equal(t, ProcessRunning, p.State)
		assert.Equal(t, sb.ID, p.SandboxID)

		got, err := sb.GetProcess(ctx, p.ID)
		require.NoError(t, err)
		assert.Equal(t, p.ID, got.ID)

		missing, err := sb.GetProcess(ctx, "8b8d6a43-5d3f-4a62-9a9e-2a4a5f6c7d8e")
		require.NoError(t, err)
		assert.Nil(t, missing)

		list, err := sb.ListProcesses(ctx, ListOpts{})
		require.NoError(t, err)
		assert.Len(t, list.Items, 1)

		out, err := p.Output(ctx, OutputOpts{})
		require.NoError(t, err)
		assert.Equal(t, "hello", string(out.Chunks[0].Data))

		stream, err := p.StreamOutput(ctx, OutputOpts{})
		require.NoError(t, err)
		var chunks []string
		for {
			chunk, err := stream.Next()
			if errors.Is(err, io.EOF) {
				break
			}
			require.NoError(t, err)
			chunks = append(chunks, string(chunk.Data))
		}
		assert.Equal(t, []string{"one", "two"}, chunks)
		require.NoError(t, stream.Close())

		done, err := p.Wait(ctx, WaitOpts{})
		require.NoError(t, err)
		assert.Equal(t, ProcessExited, done.State)
		require.NotNil(t, done.ExitCode)
		assert.Equal(t, 0, *done.ExitCode)
		require.NotNil(t, done.StartedAt)
		require.NotNil(t, done.EndedAt)
		assert.Equal(t, fakeNow, *done.StartedAt)
		assert.Equal(t, fakeNow.Add(time.Second), *done.EndedAt)

		require.NoError(t, p.Signal(ctx, SignalOpts{Signal: 15}))
	})

	t.Run("files", func(t *testing.T) {
		res, err := sb.Upload(ctx, UploadOpts{Path: "/tmp/a.txt", Data: strings.NewReader("contents"), Mode: 0o644})
		require.NoError(t, err)
		assert.Equal(t, int64(8), res.BytesWritten)

		rc, err := sb.Download(ctx, "/tmp/a.txt")
		require.NoError(t, err)
		byt, _ := io.ReadAll(rc)
		_ = rc.Close()
		assert.Equal(t, "contents", string(byt))

		_, err = sb.Download(ctx, "/tmp/missing")
		var se *Error
		require.ErrorAs(t, err, &se)
		assert.Equal(t, 404, se.Status)
		assert.Equal(t, "req-1", se.RequestID)
	})

	t.Run("pause and resume", func(t *testing.T) {
		paused, err := sb.Pause(ctx, LifecycleOpts{})
		require.NoError(t, err)
		assert.Equal(t, StatusPaused, paused.Status)

		resumed, err := paused.Resume(ctx, LifecycleOpts{})
		require.NoError(t, err)
		assert.Equal(t, StatusRunning, resumed.Status)
	})

	t.Run("snapshot and clone", func(t *testing.T) {
		snap, err := sb.Snapshot(ctx, SnapshotOpts{Name: "base"})
		require.NoError(t, err)
		assert.Equal(t, SnapshotReady, snap.Status)
		assert.Equal(t, int64(1024), snap.StoredBytes, "storedBytes may arrive as a string")

		list, err := c.ListSnapshots(ctx, SnapshotListOpts{Name: "base"})
		require.NoError(t, err)
		assert.Len(t, list.Items, 1)

		clone, err := snap.Clone(ctx, CloneOpts{Name: "clone"})
		require.NoError(t, err)
		assert.Equal(t, StatusRunning, clone.Status)
		assert.Equal(t, sb.Resources, clone.Resources)

		require.NoError(t, snap.Delete(ctx))
		gone, err := c.GetSnapshot(ctx, snap.ID)
		require.NoError(t, err)
		assert.Nil(t, gone)
	})

	t.Run("destroy", func(t *testing.T) {
		res, err := sb.Destroy(ctx)
		require.NoError(t, err)
		assert.Equal(t, StatusTerminating, res.Status)

		gone, err := c.Get(ctx, sb.ID)
		require.NoError(t, err)
		assert.Nil(t, gone)
	})
}

func TestRunRejectsMissingExitCode(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		writeData(w, http.StatusOK, map[string]any{
			"stdout": "", "stderr": "", "encoding": "base64",
		})
	}))
	defer srv.Close()

	c, err := NewClient(ClientOpts{APIBaseURL: srv.URL, SigningKey: "signkey-test-12345678"})
	require.NoError(t, err)
	sb := c.sandbox(SandboxInfo{ID: "8b8d6a43-5d3f-4a62-9a9e-2a4a5f6c7d8e"})
	_, err = sb.Run(context.Background(), Shell("true"), CommandOpts{})
	var ve *ValidationError
	require.ErrorAs(t, err, &ve)
	assert.Contains(t, err.Error(), "exitCode")
}

func TestClientCreateNoWait(t *testing.T) {
	f := newFakeAPI(t)
	sb, err := f.client(t).Create(context.Background(), CreateOpts{Name: "box", VCPU: 1, MemoryMB: 512, NoWait: true})
	require.NoError(t, err)
	assert.Equal(t, StatusStarting, sb.Status)
	assert.Equal(t, 0, f.count("GET /v2/sandboxes/{id}"))
}

func TestClientWaitRetriesAvailabilityErrors(t *testing.T) {
	f := newFakeAPI(t)
	f.failNext("GET /v2/sandboxes/{id}", 503, string(CodeComputeUnavailable))
	sb, err := f.client(t).Create(context.Background(), CreateOpts{Name: "box", VCPU: 1, MemoryMB: 512})
	require.NoError(t, err)
	assert.Equal(t, StatusRunning, sb.Status)
	assert.Equal(t, 2, f.count("GET /v2/sandboxes/{id}"))
}

func TestClientFallbackKey(t *testing.T) {
	var auth []string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		auth = append(auth, r.Header.Get("Authorization"))
		if len(auth) == 1 {
			writeError(w, 401, "access_denied")
			return
		}
		writeList(w, []any{})
	}))
	defer srv.Close()

	c, err := NewClient(ClientOpts{APIBaseURL: srv.URL, SigningKey: "signkey-prod-aa", SigningKeyFallback: "signkey-prod-bb"})
	require.NoError(t, err)
	_, err = c.List(context.Background(), ListOpts{})
	require.NoError(t, err)
	require.Len(t, auth, 2)
	assert.NotEqual(t, auth[0], auth[1])
}

func TestClientRequiresKey(t *testing.T) {
	t.Setenv("INNGEST_SIGNING_KEY", "")
	t.Setenv("INNGEST_DEV", "")
	dev := false
	c, err := NewClient(ClientOpts{APIBaseURL: "http://127.0.0.1:1", Dev: &dev})
	require.NoError(t, err)
	_, err = c.List(context.Background(), ListOpts{})
	var ve *ValidationError
	require.ErrorAs(t, err, &ve)
}

func TestErrorClassification(t *testing.T) {
	tests := []struct {
		name      string
		action    Action
		status    int
		code      ErrorCode
		retryable bool
		ambiguous bool
	}{
		{"unavailable", ActionGet, 503, CodeComputeUnavailable, true, false},
		{"rate limited", ActionExec, 429, CodeRateLimited, true, false},
		{"not found", ActionGet, 404, CodeNotFound, false, false},
		{"exec timed out", ActionExec, 504, CodeExecTimedOut, false, true},
		{"exec output too large", ActionExec, 422, CodeExecOutputTooLarge, false, true},
		{"ambiguous", ActionCreate, 500, CodeOperationAmbiguous, false, true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			e := request{action: tt.action}.itemError(tt.status, errorItem{Code: string(tt.code), Message: "m"}, "", nil)
			assert.Equal(t, tt.retryable, e.Retryable)
			assert.Equal(t, tt.ambiguous, e.Ambiguous)
		})
	}

	t.Run("transport failure", func(t *testing.T) {
		for action, safe := range map[Action]bool{ActionCreate: true, ActionDestroy: true, ActionExec: false, ActionProcessStart: false, ActionSnapshotCreate: false} {
			e := request{action: action}.transportFailure(errors.New("reset"))
			assert.Equal(t, safe, e.Retryable, action)
			assert.Equal(t, !safe, e.Ambiguous, action)
		}
	})
}

func TestStreamErrorFrame(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = io.WriteString(w, `{"type":"log","stream":"STDERR","data":"aGk=","encoding":"base64"}`+"\n")
		_, _ = io.WriteString(w, `{"type":"error","error":{"code":"sandbox_process_output_not_retained","message":"gone"}}`+"\n")
	}))
	defer srv.Close()

	c, err := NewClient(ClientOpts{APIBaseURL: srv.URL, SigningKey: "signkey-prod-aa"})
	require.NoError(t, err)
	p := c.process(ProcessInfo{SandboxID: "s", ID: "p"})
	stream, err := p.StreamOutput(context.Background(), OutputOpts{})
	require.NoError(t, err)

	chunk, err := stream.Next()
	require.NoError(t, err)
	assert.Equal(t, Stderr, chunk.Stream)
	assert.Equal(t, "hi", string(chunk.Data))

	_, err = stream.Next()
	var se *Error
	require.ErrorAs(t, err, &se)
	assert.Equal(t, CodeProcessOutputNotRetained, se.Code)
	assert.Equal(t, ActionProcessStream, se.Action)

	_, err = stream.Next()
	assert.Equal(t, se, err, "a failed stream keeps returning its error")
}
