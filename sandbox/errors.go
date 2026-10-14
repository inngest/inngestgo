package sandbox

import (
	"errors"
	"fmt"
)

// Action names a sandbox API operation.  errors carry the action that failed.
type Action string

const (
	ActionCreate           Action = "create"
	ActionList             Action = "list"
	ActionGet              Action = "get"
	ActionWaitUntilRunning Action = "waitUntilRunning"
	ActionExec             Action = "exec"
	ActionDestroy          Action = "destroy"
	ActionPause            Action = "pause"
	ActionResume           Action = "resume"
	ActionProcessStart     Action = "process.start"
	ActionProcessList      Action = "process.list"
	ActionProcessGet       Action = "process.get"
	ActionProcessSignal    Action = "process.signal"
	ActionProcessWait      Action = "process.wait"
	ActionProcessOutput    Action = "process.output"
	ActionProcessStream    Action = "process.stream"
	ActionLogsStream       Action = "logs.stream"
	ActionFileUpload       Action = "file.upload"
	ActionFileDownload     Action = "file.download"
	ActionSnapshotCreate   Action = "snapshot.create"
	ActionSnapshotList     Action = "snapshot.list"
	ActionSnapshotGet      Action = "snapshot.get"
	ActionSnapshotWait     Action = "snapshot.waitUntilReady"
	ActionSnapshotDelete   Action = "snapshot.delete"
)

// ErrorCode is a machine-readable error code from the sandbox API or the SDK.
// the API can return codes that are not listed here.
type ErrorCode string

const (
	CodeAccessDenied             ErrorCode = "access_denied"
	CodeComputeUnavailable       ErrorCode = "compute_unavailable"
	CodeInternalError            ErrorCode = "internal_error"
	CodeInvalidFieldFormat       ErrorCode = "invalid_field_format"
	CodeInvalidRequest           ErrorCode = "invalid_request"
	CodeMissingField             ErrorCode = "missing_field"
	CodeOperationAmbiguous       ErrorCode = "operation_ambiguous"
	CodeRateLimited              ErrorCode = "rate_limited"
	CodeExecOutputTooLarge       ErrorCode = "sandbox_exec_output_too_large"
	CodeExecTimedOut             ErrorCode = "sandbox_exec_timed_out"
	CodeNameTaken                ErrorCode = "sandbox_name_taken"
	CodeNotFound                 ErrorCode = "sandbox_not_found"
	CodeStartFailed              ErrorCode = "sandbox_start_failed"
	CodeStartTimedOut            ErrorCode = "sandbox_start_timed_out"
	CodeProcessNotFound          ErrorCode = "sandbox_process_not_found"
	CodeProcessOutputNotRetained ErrorCode = "sandbox_process_output_not_retained"
	CodeProcessWaitTimedOut      ErrorCode = "sandbox_process_wait_timed_out"
	CodeSnapshotLimitExceeded    ErrorCode = "sandbox_snapshot_limit_exceeded"
	CodeSnapshotNameTaken        ErrorCode = "sandbox_snapshot_name_taken"
	CodeSnapshotNotFound         ErrorCode = "sandbox_snapshot_not_found"
	CodeSnapshotNotReady         ErrorCode = "sandbox_snapshot_not_ready"
	CodeSnapshotWaitTimedOut     ErrorCode = "sandbox_snapshot_wait_timed_out"
)

// Error is a failed sandbox operation.  use errors.As to read it from an
// error returned by this package.
type Error struct {
	Action  Action    `json:"action"`
	Code    ErrorCode `json:"code"`
	Message string    `json:"message"`
	// Status is the HTTP status of the API response.  it is zero when no
	// response arrived.
	Status     int    `json:"status,omitempty"`
	SandboxID  string `json:"sandboxId,omitempty"`
	ProcessID  string `json:"processId,omitempty"`
	SnapshotID string `json:"snapshotId,omitempty"`
	// Ambiguous means the operation may have taken effect.  check its
	// effects before you send it again.
	Ambiguous bool `json:"ambiguous"`
	// Retryable means the same operation can be sent again safely.
	Retryable bool             `json:"retryable"`
	RequestID string           `json:"requestId,omitempty"`
	Details   []map[string]any `json:"details,omitempty"`

	cause error
}

func (e *Error) Error() string {
	return fmt.Sprintf("sandbox %s: %s (%s)", e.Action, e.Message, e.Code)
}

// Unwrap returns the underlying cause.  for a durable operation that failed
// in an earlier attempt, the cause is the step's errors.StepError.
func (e *Error) Unwrap() error {
	return e.cause
}

// IsNotFound reports whether err is a sandbox, process, or snapshot that does
// not exist.
func IsNotFound(err error) bool {
	var e *Error
	if !errors.As(err, &e) {
		return false
	}
	switch e.Code {
	case CodeNotFound, CodeProcessNotFound, CodeSnapshotNotFound:
		return true
	}
	return false
}

// ValidationError reports invalid input or an invalid API response.  invalid
// input is rejected before a request is sent.
type ValidationError struct {
	Message string `json:"message"`

	cause error
}

func (e *ValidationError) Error() string {
	return "sandbox: " + e.Message
}

func (e *ValidationError) Unwrap() error {
	return e.cause
}

func validationErrorf(format string, args ...any) *ValidationError {
	return &ValidationError{Message: fmt.Sprintf(format, args...)}
}
