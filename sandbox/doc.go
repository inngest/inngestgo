// Package sandbox runs commands in isolated cloud machines managed by
// Inngest.
//
// this API is experimental.  it can change without a major version bump.
//
// the package has two layers.
//
// inside an Inngest function, use the package functions and the Durable
// types.  each operation runs as a step, so a retry or replay reuses the
// recorded result and does not create a second sandbox or run a command
// again.
//
//	result, err := sandbox.With(ctx, "build", sandbox.CreateOpts{}, func(ctx context.Context, sb *sandbox.DurableSandbox) (sandbox.CommandResult, error) {
//		return sb.Run(ctx, "make", sandbox.Shell("make build"), sandbox.CommandOpts{})
//	})
//
// outside a function, use a Client.  its calls are plain API calls.
//
//	c, err := sandbox.NewClient(sandbox.ClientOpts{})
//	sb, err := c.Create(ctx, sandbox.CreateOpts{Name: "dev", VCPU: 2, MemoryMB: 2048})
//	res, err := sb.Run(ctx, sandbox.Shell("uname -a"), sandbox.CommandOpts{})
//
// errors from the API are *Error values with a Code.  input that the SDK
// rejects is a *ValidationError, and the SDK does not send it.
package sandbox
