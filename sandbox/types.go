package sandbox

import (
	"io"
	"os"
	"time"
)

// Status is the lifecycle state of a sandbox.
type Status string

const (
	StatusPending     Status = "PENDING"
	StatusStarting    Status = "STARTING"
	StatusPausing     Status = "PAUSING"
	StatusRunning     Status = "RUNNING"
	StatusPaused      Status = "PAUSED"
	StatusResuming    Status = "RESUMING"
	StatusTerminating Status = "TERMINATING"
	StatusTerminated  Status = "TERMINATED"
	StatusFailed      Status = "FAILED"
)

// Resources is the compute given to a sandbox or recorded in a snapshot.
type Resources struct {
	VCPU     int `json:"vcpu"`
	MemoryMB int `json:"memoryMb"`
}

// SandboxInfo is the state of a sandbox when it was last loaded from the API.
type SandboxInfo struct {
	ID        string     `json:"id"`
	Name      string     `json:"name"`
	Status    Status     `json:"status"`
	VPCID     string     `json:"vpcId"`
	ImageRef  string     `json:"imageRef"`
	Resources Resources  `json:"resources"`
	CreatedAt time.Time  `json:"createdAt"`
	StartedAt *time.Time `json:"startedAt,omitempty"`
	EndedAt   *time.Time `json:"endedAt,omitempty"`
	// Error describes why the sandbox failed, if it did.
	Error string `json:"error,omitempty"`
}

// CreateOpts configures a new sandbox.
//
// set VCPU and MemoryMB to start a fresh sandbox.  set SnapshotID to start
// from a snapshot instead.  a snapshot sandbox takes its resources and
// environment from the snapshot, so VCPU, MemoryMB, Environment, and Secrets
// must stay empty.
type CreateOpts struct {
	// Name is the stable identity of an active sandbox.  creating the same
	// name again with the same configuration returns the existing sandbox.
	// names are case-sensitive and have at most 255 characters.
	Name string

	VCPU     int
	MemoryMB int

	// Environment is inherited by every command and process in the sandbox.
	// the API stores these values as sandbox configuration.  use Secrets for
	// workspace secrets.
	Environment map[string]string

	// Secrets are workspace secret names.  each secret is injected under the
	// same environment variable name, for example "OPENAI_API_KEY".  values
	// stay in snapshots of the sandbox.
	Secrets []string

	// SnapshotID starts the sandbox from this snapshot.
	SnapshotID string

	// RunningTimeout is how long to wait for the sandbox to reach RUNNING.
	// it defaults to 120 seconds and must not exceed 5 minutes.
	RunningTimeout time.Duration

	// NoWait returns as soon as the API accepts the create, with the sandbox
	// in STARTING or RUNNING.
	NoWait bool
}

// ListOpts pages through a list.  Limit defaults to 50 and must not exceed
// 250.
type ListOpts struct {
	Cursor string
	Limit  int
}

// Page describes where a list result sits in the full list.  pass Cursor to
// the next call to read the next page.
type Page struct {
	Cursor  string `json:"cursor,omitempty"`
	HasMore bool   `json:"hasMore"`
	Limit   int    `json:"limit"`
}

// ListResult is one page of results.
type ListResult[T any] struct {
	Items     []T    `json:"items"`
	Page      Page   `json:"page"`
	FetchedAt string `json:"fetchedAt"`
}

// Command is the argv of a command.  the first element must be an absolute
// path to an executable.  use Shell to run a shell script.
type Command []string

// Shell returns a command that runs script with /bin/sh.  the script is passed
// to the shell unchanged, so quoting, variables, pipes, and redirects work.
func Shell(script string) Command {
	return Command{"/bin/sh", "-c", script}
}

// Argv returns a command that runs path with args and no shell.
func Argv(path string, args ...string) Command {
	return append(Command{path}, args...)
}

// CommandOpts configures one command.
type CommandOpts struct {
	// Environment overrides matching sandbox environment values for this
	// command only.
	Environment map[string]string
	Cwd         string
	// Timeout defaults to 30 seconds and must not exceed 5 minutes.
	Timeout time.Duration
}

// StreamBytes counts bytes for each output stream.
type StreamBytes struct {
	Stdout int `json:"stdout"`
	Stderr int `json:"stderr"`
}

// OutputInfo says whether command output was cut short.
type OutputInfo struct {
	Truncated bool `json:"truncated"`
	// Strategy is "tail" when output was truncated.  the end of each stream
	// is kept, because command failures usually appear at the end.
	Strategy      string       `json:"strategy,omitempty"`
	OriginalBytes *StreamBytes `json:"originalBytes,omitempty"`
	RetainedBytes *StreamBytes `json:"retainedBytes,omitempty"`
}

// CommandResult is the outcome of a command that ran to completion.  a
// command that exits non-zero is a result, not an error.
type CommandResult struct {
	// Stdout holds the raw bytes the command wrote.  it is not always valid
	// UTF-8.
	Stdout   string     `json:"stdout"`
	Stderr   string     `json:"stderr"`
	ExitCode int        `json:"exitCode"`
	Output   OutputInfo `json:"output"`
}

// DestroyResult is the outcome of a destroy.  Sandbox is set only while the
// sandbox is TERMINATING.
type DestroyResult struct {
	Status  Status       `json:"status"`
	Sandbox *SandboxInfo `json:"sandbox,omitempty"`
}

// LifecycleOpts bounds how long Pause, Resume, and Snapshot wait.  timeout
// defaults to 5 minutes, which is also the maximum.
type LifecycleOpts struct {
	Timeout time.Duration
}

// ProcessState is the lifecycle state of a background process.
type ProcessState string

const (
	ProcessStarting ProcessState = "STARTING"
	ProcessRunning  ProcessState = "RUNNING"
	ProcessExited   ProcessState = "EXITED"
	ProcessKilled   ProcessState = "KILLED"
	ProcessFailed   ProcessState = "FAILED"
	ProcessLost     ProcessState = "LOST"
)

// Done reports whether the process has stopped.
func (s ProcessState) Done() bool {
	switch s {
	case ProcessExited, ProcessKilled, ProcessFailed, ProcessLost:
		return true
	}
	return false
}

// ProcessInfo is the state of a background process when it was last loaded.
type ProcessInfo struct {
	SandboxID string       `json:"sandboxId"`
	ID        string       `json:"id"`
	Command   Command      `json:"command"`
	PID       int          `json:"pid,omitempty"`
	State     ProcessState `json:"state"`
	// ExitCode is set only for EXITED processes.
	ExitCode *int `json:"exitCode,omitempty"`
	// TerminationSignal is set only for KILLED processes.
	TerminationSignal *int       `json:"terminationSignal,omitempty"`
	StartedAt         *time.Time `json:"startedAt,omitempty"`
	EndedAt           *time.Time `json:"endedAt,omitempty"`
}

// ProcessOpts configures a background process.
type ProcessOpts struct {
	Command Command
	// Environment overrides matching sandbox environment values for this
	// process only.
	Environment map[string]string
	Cwd         string
}

// SignalOpts configures a signal sent to a process.
type SignalOpts struct {
	// Signal is a Linux signal number from 1 to 64, for example 15 for
	// SIGTERM.
	Signal int
	// ExcludeChildren signals only the process itself.  by default the
	// process's descendants get the signal too, so shell commands do not
	// leave child processes running.
	ExcludeChildren bool
}

// WaitOpts bounds a process wait.  timeout defaults to 30 seconds and must
// not exceed 5 minutes.
type WaitOpts struct {
	Timeout time.Duration
}

// OutputOpts selects process output.  TailBytes keeps only the last bytes of
// output and must not exceed 512 KiB.  zero returns all retained output.
type OutputOpts struct {
	TailBytes int
}

// Stream names an output stream.
type Stream string

const (
	Stdout Stream = "STDOUT"
	Stderr Stream = "STDERR"
)

// OutputChunk is one piece of output from a process or the sandbox log.
type OutputChunk struct {
	Stream Stream     `json:"stream"`
	Data   []byte     `json:"data"`
	At     *time.Time `json:"at,omitempty"`
}

// ProcessOutput is the retained output of a process.
type ProcessOutput struct {
	Chunks []OutputChunk `json:"chunks"`
	// Truncated reports that a durable step removed older chunks to fit its
	// result limit.  direct client calls leave it false.
	Truncated bool `json:"truncated,omitempty"`
}

// LogsOpts configures a sandbox log stream.  Follow keeps the stream open for
// new output.
type LogsOpts struct {
	Follow bool
}

// UploadOpts writes Data to an absolute Path in the sandbox, replacing any
// file there.  Mode defaults to the API's default file mode.
type UploadOpts struct {
	Path string
	Data io.Reader
	Mode os.FileMode
}

// UploadResult is the outcome of an upload.
type UploadResult struct {
	Path         string `json:"path"`
	BytesWritten int64  `json:"bytesWritten"`
}

// SnapshotStatus is the lifecycle state of a snapshot.
type SnapshotStatus string

const (
	SnapshotCreating SnapshotStatus = "CREATING"
	SnapshotReady    SnapshotStatus = "READY"
	SnapshotDeleting SnapshotStatus = "DELETING"
	SnapshotDeleted  SnapshotStatus = "DELETED"
	SnapshotFailed   SnapshotStatus = "FAILED"
	SnapshotLost     SnapshotStatus = "LOST"
)

// SnapshotInfo is the state of a snapshot when it was last loaded.
type SnapshotInfo struct {
	ID string `json:"id"`
	// Name is the name given when the snapshot was created, if any.
	Name            string         `json:"name,omitempty"`
	SourceImageID   string         `json:"sourceImageId"`
	Status          SnapshotStatus `json:"status"`
	CompatibilityID string         `json:"compatibilityId,omitempty"`
	Resources       Resources      `json:"resources"`
	MemoryPackCount int            `json:"memoryPackCount"`
	DiskPackCount   int            `json:"diskPackCount"`
	StoredBytes     int64          `json:"storedBytes"`
	CreatedAt       time.Time      `json:"createdAt"`
	UpdatedAt       time.Time      `json:"updatedAt"`
	ExpiresAt       time.Time      `json:"expiresAt"`
	Error           string         `json:"error,omitempty"`
}

// SnapshotOpts configures a snapshot.
type SnapshotOpts struct {
	// Name is optional.  at most one CREATING or unexpired READY snapshot
	// holds a name.  creating another snapshot with a held name fails with
	// CodeSnapshotNameTaken.
	Name string
	// Timeout bounds the wait for the snapshot to become READY.  it defaults
	// to 5 minutes, which is also the maximum.
	Timeout time.Duration
}

// CloneOpts configures a sandbox started from a snapshot.
type CloneOpts struct {
	Name string
	// RunningTimeout defaults to 120 seconds and must not exceed 5 minutes.
	RunningTimeout time.Duration
	// NoWait returns as soon as the API accepts the create.
	NoWait bool
}

// SnapshotListOpts pages through snapshots.  Name returns only snapshots with
// exactly that name, newest first.
type SnapshotListOpts struct {
	Cursor string
	Limit  int
	Name   string
}
