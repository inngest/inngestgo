package sandbox

import (
	"context"

	inngesterrors "github.com/inngest/inngestgo/errors"
	"github.com/inngest/inngestgo/internal/sdkrequest"
)

// With creates a sandbox, passes it to fn, and destroys it when fn is done.
// the create step is id and the destroy step is id + ":destroy".
//
// empty fields of opts take defaults.  Name defaults to "<run ID>-<id>", so
// no other run shares the sandbox.  a fresh sandbox defaults to 2 vCPUs and
// 2048 MB of memory.
//
// the sandbox stays alive when fn returns an error that Inngest will retry.
// the retry replays the recorded create and gets the same sandbox.  the
// sandbox is destroyed after success, after a step that failed for good,
// after a NoRetryError, and on the last attempt.  a destroy that fails for
// good does not change the result of fn.
func With[T any](ctx context.Context, id string, opts CreateOpts, fn func(ctx context.Context, sb *DurableSandbox) (T, error)) (T, error) {
	var zero T
	if opts.Name == "" {
		opts.Name = id
		if mgr, ok := sdkrequest.Manager(ctx); ok && mgr.Request() != nil && mgr.Request().CallCtx.RunID != "" {
			opts.Name = mgr.Request().CallCtx.RunID + "-" + id
		}
	}
	if opts.SnapshotID == "" {
		if opts.VCPU == 0 {
			opts.VCPU = 2
		}
		if opts.MemoryMB == 0 {
			opts.MemoryMB = 2048
		}
	}

	sb, err := Create(ctx, id, opts)
	if err != nil {
		return zero, err
	}

	// a panic in fn, such as the SDK's signal that a step was planned, skips
	// the destroy.  the sandbox is still needed when the function resumes.
	out, err := fn(ctx, sb)
	if err != nil && !isFinal(ctx, err) {
		return out, err
	}

	_, derr := sb.Destroy(ctx, id+":destroy")
	if err != nil {
		return out, err
	}
	if derr != nil && !inngesterrors.IsStepError(derr) {
		return out, derr
	}
	return out, nil
}

// isFinal reports whether err ends the run, so the sandbox is no longer
// needed.  without a known attempt limit, the run is assumed to retry.
func isFinal(ctx context.Context, err error) bool {
	if inngesterrors.IsStepError(err) || inngesterrors.IsNoRetryError(err) {
		return true
	}
	mgr, ok := sdkrequest.Manager(ctx)
	if !ok || mgr.Request() == nil {
		return false
	}
	call := mgr.Request().CallCtx
	return call.MaxAttempts != nil && call.Attempt >= *call.MaxAttempts-1
}
