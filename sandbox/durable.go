package sandbox

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"time"

	inngesterrors "github.com/inngest/inngestgo/errors"
	"github.com/inngest/inngestgo/internal/sdkrequest"
	"github.com/inngest/inngestgo/pkg/env"
	"github.com/inngest/inngestgo/step"
)

// maxDurableOutputBytes bounds the command output that a durable Run
// records.  a step records at most 4 MiB, and base64 grows output by a third,
// so 2 MiB of raw output fits with room to spare.
const maxDurableOutputBytes = 2 << 20

// stepResult is what a durable sandbox step records.  when the operation
// fails, Error keeps the API error.  a later attempt reads it back from the
// step state and returns the same *Error.
type stepResult[T any] struct {
	Action Action `json:"action"`
	Result T      `json:"result"`
	Error  *Error `json:"error,omitempty"`
}

// clientFromContext returns a client that uses the signing key, API URL, and
// branch environment of the current function run.  outside a function it
// reads the same environment variables as NewClient.
func clientFromContext(ctx context.Context) (*Client, error) {
	mgr, ok := sdkrequest.Manager(ctx)
	if !ok {
		return NewClient(ClientOpts{})
	}
	base := mgr.APIBaseURL()
	if base == "" {
		base = env.APIServerURL(nil)
	}
	var branch string
	if req := mgr.Request(); req != nil {
		branch = req.CallCtx.Env
	}
	key, err := hashKey(mgr.SigningKey())
	if err != nil {
		return nil, fmt.Errorf("invalid signing key: %w", err)
	}
	fallbackKey, err := hashKey(mgr.SigningKeyFallback())
	if err != nil {
		return nil, fmt.Errorf("invalid fallback signing key: %w", err)
	}
	return &Client{t: &transport{
		baseURL:     base,
		key:         key,
		fallbackKey: fallbackKey,
		env:         branch,
		dev:         env.IsDev(),
		http:        http.DefaultClient,
	}}, nil
}

// durable runs fn as the step id.  a retryable API error makes the step
// retry.  any other sandbox error fails the step without retries.
func durable[T any](ctx context.Context, id string, action Action, fn func(ctx context.Context, c *Client) (T, error)) (T, error) {
	res, err := step.Run(ctx, id, func(ctx context.Context) (stepResult[T], error) {
		c, err := clientFromContext(ctx)
		if err != nil {
			return stepResult[T]{Action: action}, inngesterrors.NoRetryError(err)
		}
		out, err := fn(ctx, c)
		if err == nil {
			return stepResult[T]{Action: action, Result: out}, nil
		}

		var se *Error
		if errors.As(err, &se) {
			r := stepResult[T]{Action: action, Error: se}
			if se.Retryable {
				return r, se
			}
			return r, inngesterrors.NoRetryError(se)
		}
		var ve *ValidationError
		if errors.As(err, &ve) {
			return stepResult[T]{Action: action}, inngesterrors.NoRetryError(ve)
		}
		return stepResult[T]{Action: action}, err
	})
	if err != nil {
		var zero T
		// a step that failed in an earlier request comes back as a StepError.
		// rebuild the sandbox error so callers can read its code.
		if inngesterrors.IsStepError(err) && res.Error != nil {
			se := *res.Error
			se.cause = err
			return zero, &se
		}
		return zero, err
	}
	return res.Result, nil
}

// fitOutput keeps the end of each stream when the output is too large to
// record.  each stream gets half of the budget, and a stream gives the part it
// does not use to the other stream.
func fitOutput(stdout, stderr []byte) ([]byte, []byte, OutputInfo) {
	orig := StreamBytes{Stdout: len(stdout), Stderr: len(stderr)}
	if orig.Stdout+orig.Stderr <= maxDurableOutputBytes {
		return stdout, stderr, OutputInfo{}
	}
	share := maxDurableOutputBytes / 2
	keepOut := min(orig.Stdout, share)
	keepErr := min(orig.Stderr, share)
	remaining := maxDurableOutputBytes - keepOut - keepErr
	extra := min(orig.Stdout-keepOut, remaining)
	keepOut += extra
	remaining -= extra
	keepErr += min(orig.Stderr-keepErr, remaining)

	kept := StreamBytes{Stdout: keepOut, Stderr: keepErr}
	return stdout[orig.Stdout-keepOut:], stderr[orig.Stderr-keepErr:], OutputInfo{
		Truncated:     true,
		Strategy:      "tail",
		OriginalBytes: &orig,
		RetainedBytes: &kept,
	}
}

// DurableSandbox is a sandbox used from an Inngest function.  each method
// runs as a step with the ID you pass, so a retry or replay reuses the
// recorded result instead of calling the API again.
type DurableSandbox struct {
	SandboxInfo
}

// DurableProcess is a background process used from an Inngest function.
type DurableProcess struct {
	ProcessInfo
}

// DurableSnapshot is a snapshot used from an Inngest function.
type DurableSnapshot struct {
	SnapshotInfo
}

// Create creates a sandbox as the step id and, unless opts.NoWait is set,
// waits for it to reach RUNNING.  destroy the sandbox when you are done, or
// use With to destroy it for you.
func Create(ctx context.Context, id string, opts CreateOpts) (*DurableSandbox, error) {
	body, wait, err := normalizeCreate(opts)
	if err != nil {
		return nil, err
	}
	info, err := durable(ctx, id, ActionCreate, func(ctx context.Context, c *Client) (SandboxInfo, error) {
		s, err := c.create(ctx, body, wait)
		if err != nil {
			return SandboxInfo{}, err
		}
		return s.SandboxInfo, nil
	})
	if err != nil {
		return nil, err
	}
	return &DurableSandbox{info}, nil
}

// List lists sandboxes as the step id.
func List(ctx context.Context, id string, opts ListOpts) (ListResult[*DurableSandbox], error) {
	if _, _, err := normalizeList(opts.Cursor, opts.Limit); err != nil {
		return ListResult[*DurableSandbox]{}, err
	}
	res, err := durable(ctx, id, ActionList, func(ctx context.Context, c *Client) (ListResult[SandboxInfo], error) {
		l, err := c.List(ctx, opts)
		if err != nil {
			return ListResult[SandboxInfo]{}, err
		}
		out := ListResult[SandboxInfo]{Items: make([]SandboxInfo, 0, len(l.Items)), Page: l.Page, FetchedAt: l.FetchedAt}
		for _, s := range l.Items {
			out.Items = append(out.Items, s.SandboxInfo)
		}
		return out, nil
	})
	if err != nil {
		return ListResult[*DurableSandbox]{}, err
	}
	out := ListResult[*DurableSandbox]{Items: make([]*DurableSandbox, 0, len(res.Items)), Page: res.Page, FetchedAt: res.FetchedAt}
	for _, info := range res.Items {
		out.Items = append(out.Items, &DurableSandbox{info})
	}
	return out, nil
}

// Get loads a sandbox as the step id.  it returns nil and no error when the
// sandbox does not exist.
func Get(ctx context.Context, id string, sandboxID string) (*DurableSandbox, error) {
	if err := validateUUID(sandboxID, "sandbox ID"); err != nil {
		return nil, err
	}
	info, err := durable(ctx, id, ActionGet, func(ctx context.Context, c *Client) (*SandboxInfo, error) {
		s, err := c.Get(ctx, sandboxID)
		if err != nil || s == nil {
			return nil, err
		}
		return &s.SandboxInfo, nil
	})
	if err != nil || info == nil {
		return nil, err
	}
	return &DurableSandbox{*info}, nil
}

// ListSnapshots lists snapshots as the step id.
func ListSnapshots(ctx context.Context, id string, opts SnapshotListOpts) (ListResult[*DurableSnapshot], error) {
	if _, _, err := normalizeList(opts.Cursor, opts.Limit); err != nil {
		return ListResult[*DurableSnapshot]{}, err
	}
	if opts.Name != "" {
		if err := validateName(opts.Name, "name"); err != nil {
			return ListResult[*DurableSnapshot]{}, err
		}
	}
	res, err := durable(ctx, id, ActionSnapshotList, func(ctx context.Context, c *Client) (ListResult[SnapshotInfo], error) {
		return c.listSnapshots(ctx, opts)
	})
	if err != nil {
		return ListResult[*DurableSnapshot]{}, err
	}
	out := ListResult[*DurableSnapshot]{Items: make([]*DurableSnapshot, 0, len(res.Items)), Page: res.Page, FetchedAt: res.FetchedAt}
	for _, info := range res.Items {
		out.Items = append(out.Items, &DurableSnapshot{info})
	}
	return out, nil
}

// GetSnapshot loads a snapshot as the step id.  it returns nil and no error
// when the snapshot does not exist.
func GetSnapshot(ctx context.Context, id string, snapshotID string) (*DurableSnapshot, error) {
	if err := validateUUID(snapshotID, "snapshot ID"); err != nil {
		return nil, err
	}
	info, err := durable(ctx, id, ActionSnapshotGet, func(ctx context.Context, c *Client) (*SnapshotInfo, error) {
		s, err := c.GetSnapshot(ctx, snapshotID)
		if err != nil || s == nil {
			return nil, err
		}
		return &s.SnapshotInfo, nil
	})
	if err != nil || info == nil {
		return nil, err
	}
	return &DurableSnapshot{*info}, nil
}

// execResult is what a durable Run records.  output is base64 in the step
// state, because command output is not always valid UTF-8.
type execResult struct {
	Stdout   []byte     `json:"stdout"`
	Stderr   []byte     `json:"stderr"`
	Encoding string     `json:"encoding"`
	ExitCode int        `json:"exitCode"`
	Output   OutputInfo `json:"output"`
}

// Run runs cmd as the step id and waits for it to exit.  a non-zero exit code
// is a result, not an error.  when stdout and stderr together exceed 2 MiB,
// the result keeps the end of each and sets Output.Truncated.
func (s *DurableSandbox) Run(ctx context.Context, id string, cmd Command, opts CommandOpts) (CommandResult, error) {
	spec, err := normalizeSpec(cmd, opts.Environment, opts.Cwd)
	if err != nil {
		return CommandResult{}, err
	}
	timeout, err := duration(opts.Timeout, defaultCommandWait, maxCommandTimeout, "Timeout")
	if err != nil {
		return CommandResult{}, err
	}
	res, err := durable(ctx, id, ActionExec, func(ctx context.Context, c *Client) (execResult, error) {
		r, err := c.sandbox(s.SandboxInfo).run(ctx, spec, timeout)
		if err != nil {
			return execResult{}, err
		}
		stdout, stderr, info := fitOutput([]byte(r.Stdout), []byte(r.Stderr))
		return execResult{Stdout: stdout, Stderr: stderr, Encoding: "base64", ExitCode: r.ExitCode, Output: info}, nil
	})
	if err != nil {
		return CommandResult{}, err
	}
	return CommandResult{
		Stdout:   string(res.Stdout),
		Stderr:   string(res.Stderr),
		ExitCode: res.ExitCode,
		Output:   res.Output,
	}, nil
}

// StartProcess starts a background process as the step id.
func (s *DurableSandbox) StartProcess(ctx context.Context, id string, opts ProcessOpts) (*DurableProcess, error) {
	spec, err := normalizeSpec(opts.Command, opts.Environment, opts.Cwd)
	if err != nil {
		return nil, err
	}
	info, err := durable(ctx, id, ActionProcessStart, func(ctx context.Context, c *Client) (ProcessInfo, error) {
		return c.sandbox(s.SandboxInfo).startProcess(ctx, spec)
	})
	if err != nil {
		return nil, err
	}
	return &DurableProcess{info}, nil
}

// ListProcesses lists the sandbox's processes as the step id.
func (s *DurableSandbox) ListProcesses(ctx context.Context, id string, opts ListOpts) (ListResult[*DurableProcess], error) {
	if _, _, err := normalizeList(opts.Cursor, opts.Limit); err != nil {
		return ListResult[*DurableProcess]{}, err
	}
	res, err := durable(ctx, id, ActionProcessList, func(ctx context.Context, c *Client) (ListResult[ProcessInfo], error) {
		return c.sandbox(s.SandboxInfo).listProcesses(ctx, opts)
	})
	if err != nil {
		return ListResult[*DurableProcess]{}, err
	}
	out := ListResult[*DurableProcess]{Items: make([]*DurableProcess, 0, len(res.Items)), Page: res.Page, FetchedAt: res.FetchedAt}
	for _, info := range res.Items {
		out.Items = append(out.Items, &DurableProcess{info})
	}
	return out, nil
}

// GetProcess loads a process as the step id.  it returns nil and no error
// when the process does not exist.
func (s *DurableSandbox) GetProcess(ctx context.Context, id string, processID string) (*DurableProcess, error) {
	if err := validateUUID(processID, "process ID"); err != nil {
		return nil, err
	}
	info, err := durable(ctx, id, ActionProcessGet, func(ctx context.Context, c *Client) (*ProcessInfo, error) {
		return c.sandbox(s.SandboxInfo).getProcess(ctx, processID)
	})
	if err != nil || info == nil {
		return nil, err
	}
	return &DurableProcess{*info}, nil
}

// WaitUntilRunning waits as the step id for a STARTING sandbox to reach
// RUNNING.  timeout must be set and must not exceed 5 minutes.
func (s *DurableSandbox) WaitUntilRunning(ctx context.Context, id string, timeout time.Duration) (*DurableSandbox, error) {
	if timeout == 0 {
		return nil, validationErrorf("timeout must be set")
	}
	timeout, err := duration(timeout, 0, maxRunningTimeout, "timeout")
	if err != nil {
		return nil, err
	}
	info, err := durable(ctx, id, ActionWaitUntilRunning, func(ctx context.Context, c *Client) (SandboxInfo, error) {
		r, err := c.waitUntilRunning(ctx, s.SandboxInfo, timeout, ActionWaitUntilRunning)
		if err != nil {
			return SandboxInfo{}, err
		}
		return r.SandboxInfo, nil
	})
	if err != nil {
		return nil, err
	}
	return &DurableSandbox{info}, nil
}

// Pause pauses the sandbox as the step id and waits for it to reach PAUSED.
func (s *DurableSandbox) Pause(ctx context.Context, id string, opts LifecycleOpts) (*DurableSandbox, error) {
	return s.lifecycle(ctx, id, ActionPause, opts, (*Sandbox).Pause)
}

// Resume resumes the sandbox as the step id and waits for it to reach
// RUNNING.
func (s *DurableSandbox) Resume(ctx context.Context, id string, opts LifecycleOpts) (*DurableSandbox, error) {
	return s.lifecycle(ctx, id, ActionResume, opts, (*Sandbox).Resume)
}

func (s *DurableSandbox) lifecycle(ctx context.Context, id string, action Action, opts LifecycleOpts, fn func(*Sandbox, context.Context, LifecycleOpts) (*Sandbox, error)) (*DurableSandbox, error) {
	if _, err := duration(opts.Timeout, maxLifecycleTimeout, maxLifecycleTimeout, "Timeout"); err != nil {
		return nil, err
	}
	info, err := durable(ctx, id, action, func(ctx context.Context, c *Client) (SandboxInfo, error) {
		r, err := fn(c.sandbox(s.SandboxInfo), ctx, opts)
		if err != nil {
			return SandboxInfo{}, err
		}
		return r.SandboxInfo, nil
	})
	if err != nil {
		return nil, err
	}
	return &DurableSandbox{info}, nil
}

// Snapshot snapshots the sandbox and waits for the snapshot to be READY.  it
// runs two steps.  the step id creates the snapshot, and the step
// id + ":wait-until-ready" waits for it.  a retry of the wait does not
// create a second snapshot.
func (s *DurableSandbox) Snapshot(ctx context.Context, id string, opts SnapshotOpts) (*DurableSnapshot, error) {
	if opts.Name != "" {
		if err := validateName(opts.Name, "name"); err != nil {
			return nil, err
		}
	}
	timeout, err := duration(opts.Timeout, maxLifecycleTimeout, maxLifecycleTimeout, "Timeout")
	if err != nil {
		return nil, err
	}
	created, err := durable(ctx, id, ActionSnapshotCreate, func(ctx context.Context, c *Client) (SnapshotInfo, error) {
		return c.createSnapshot(ctx, s.ID, opts.Name)
	})
	if err != nil {
		return nil, err
	}
	return (&DurableSnapshot{created}).WaitUntilReady(ctx, id+":wait-until-ready", timeout)
}

// Destroy terminates the sandbox as the step id.  a sandbox that no longer
// exists counts as TERMINATED, so a repeated destroy succeeds.
func (s *DurableSandbox) Destroy(ctx context.Context, id string) (DestroyResult, error) {
	return durable(ctx, id, ActionDestroy, func(ctx context.Context, c *Client) (DestroyResult, error) {
		r, err := c.sandbox(s.SandboxInfo).Destroy(ctx)
		var se *Error
		if errors.As(err, &se) && se.Code == CodeNotFound {
			return DestroyResult{Status: StatusTerminated}, nil
		}
		return r, err
	})
}

// Signal sends a signal to the process as the step id.
func (p *DurableProcess) Signal(ctx context.Context, id string, opts SignalOpts) error {
	if err := normalizeSignal(opts); err != nil {
		return err
	}
	_, err := durable(ctx, id, ActionProcessSignal, func(ctx context.Context, c *Client) (struct{}, error) {
		return struct{}{}, c.process(p.ProcessInfo).Signal(ctx, opts)
	})
	return err
}

// Wait waits as the step id for the process to stop and returns its final
// state.
func (p *DurableProcess) Wait(ctx context.Context, id string, opts WaitOpts) (*DurableProcess, error) {
	timeout, err := duration(opts.Timeout, defaultCommandWait, maxCommandTimeout, "Timeout")
	if err != nil {
		return nil, err
	}
	info, err := durable(ctx, id, ActionProcessWait, func(ctx context.Context, c *Client) (ProcessInfo, error) {
		return c.process(p.ProcessInfo).wait(ctx, timeout)
	})
	if err != nil {
		return nil, err
	}
	return &DurableProcess{info}, nil
}

// Output returns the retained output of the process as the step id.
func (p *DurableProcess) Output(ctx context.Context, id string, opts OutputOpts) (ProcessOutput, error) {
	if err := normalizeTail(opts); err != nil {
		return ProcessOutput{}, err
	}
	return durable(ctx, id, ActionProcessOutput, func(ctx context.Context, c *Client) (ProcessOutput, error) {
		return c.process(p.ProcessInfo).Output(ctx, opts)
	})
}

// WaitUntilReady waits as the step id for the snapshot to be READY.  timeout
// must be set and must not exceed 5 minutes.
func (s *DurableSnapshot) WaitUntilReady(ctx context.Context, id string, timeout time.Duration) (*DurableSnapshot, error) {
	if timeout == 0 {
		return nil, validationErrorf("timeout must be set")
	}
	timeout, err := duration(timeout, 0, maxLifecycleTimeout, "timeout")
	if err != nil {
		return nil, err
	}
	info, err := durable(ctx, id, ActionSnapshotWait, func(ctx context.Context, c *Client) (SnapshotInfo, error) {
		return c.waitUntilSnapshotReady(ctx, s.SnapshotInfo, timeout)
	})
	if err != nil {
		return nil, err
	}
	return &DurableSnapshot{info}, nil
}

// Clone creates a sandbox from the snapshot as the step id.
func (s *DurableSnapshot) Clone(ctx context.Context, id string, opts CloneOpts) (*DurableSandbox, error) {
	return Create(ctx, id, CreateOpts{
		Name:           opts.Name,
		SnapshotID:     s.ID,
		RunningTimeout: opts.RunningTimeout,
		NoWait:         opts.NoWait,
	})
}

// Delete deletes the snapshot as the step id.
func (s *DurableSnapshot) Delete(ctx context.Context, id string) error {
	_, err := durable(ctx, id, ActionSnapshotDelete, func(ctx context.Context, c *Client) (struct{}, error) {
		return struct{}{}, c.deleteSnapshot(ctx, s.ID)
	})
	return err
}
