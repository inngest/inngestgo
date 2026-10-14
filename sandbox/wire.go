package sandbox

import (
	"bytes"
	"encoding/base64"
	"encoding/json"
	"regexp"
	"strconv"
	"time"
)

var sha256Regexp = regexp.MustCompile(`^[a-f0-9]{64}$`)

// the decoders here check API responses before the SDK uses them.  a durable
// step records what they return, so a bad response fails the step instead of
// leaving bad state in the run.

func checkResources(r Resources, what string) error {
	if r.VCPU <= 0 || r.MemoryMB <= 0 || uint64(r.VCPU) > 0xffffffff || uint64(r.MemoryMB) > 0xffffffff {
		return validationErrorf("invalid %s: resources must be positive uint32 values", what)
	}
	return nil
}

func decodeSandbox(raw json.RawMessage) (SandboxInfo, error) {
	var s SandboxInfo
	if len(raw) == 0 || string(raw) == "null" {
		return s, validationErrorf("invalid sandbox resource: missing data")
	}
	if err := json.Unmarshal(raw, &s); err != nil {
		return s, &ValidationError{Message: "invalid sandbox resource", cause: err}
	}
	return s, checkSandbox(s)
}

func checkSandbox(s SandboxInfo) error {
	if err := validateUUID(s.ID, "sandbox ID"); err != nil {
		return err
	}
	if err := validateName(s.Name, "sandbox name"); err != nil {
		return err
	}
	if err := validateUUID(s.VPCID, "VPC ID"); err != nil {
		return err
	}
	if s.ImageRef == "" || s.CreatedAt.IsZero() {
		return validationErrorf("invalid sandbox resource: missing imageRef or createdAt")
	}
	switch s.Status {
	case StatusPending, StatusStarting, StatusPausing, StatusRunning, StatusPaused,
		StatusResuming, StatusTerminating, StatusTerminated, StatusFailed:
	default:
		return validationErrorf("invalid sandbox resource: unknown status %q", s.Status)
	}
	return checkResources(s.Resources, "sandbox resource")
}

func decodeProcess(sandboxID string, raw json.RawMessage) (ProcessInfo, error) {
	var p ProcessInfo
	if len(raw) == 0 || string(raw) == "null" {
		return p, validationErrorf("invalid sandbox process: missing data")
	}
	if err := json.Unmarshal(raw, &p); err != nil {
		return p, &ValidationError{Message: "invalid sandbox process", cause: err}
	}
	p.SandboxID = sandboxID
	return p, checkProcess(p)
}

func checkProcess(p ProcessInfo) error {
	if err := validateUUID(p.ID, "process ID"); err != nil {
		return err
	}
	if len(p.Command) == 0 || len(p.Command) > maxArgvCount {
		return validationErrorf("invalid sandbox process: command must have 1 to %d arguments", maxArgvCount)
	}
	if p.PID < 0 || int64(p.PID) > 0x7fffffff {
		return validationErrorf("invalid sandbox process: pid must fit a signed 32-bit integer")
	}
	switch p.State {
	case ProcessStarting, ProcessRunning, ProcessExited, ProcessKilled, ProcessFailed, ProcessLost:
	default:
		return validationErrorf("invalid sandbox process: unknown state %q", p.State)
	}
	if (p.State == ProcessExited) != (p.ExitCode != nil) {
		return validationErrorf("invalid sandbox process: exitCode must be present only for EXITED processes")
	}
	if (p.State == ProcessKilled) != (p.TerminationSignal != nil) {
		return validationErrorf("invalid sandbox process: terminationSignal must be present only for KILLED processes")
	}
	if p.ExitCode != nil && (int64(*p.ExitCode) < -0x80000000 || int64(*p.ExitCode) > 0x7fffffff) {
		return validationErrorf("invalid sandbox process: exitCode must be a signed 32-bit integer")
	}
	if p.TerminationSignal != nil && (*p.TerminationSignal < 1 || *p.TerminationSignal > 64) {
		return validationErrorf("invalid sandbox process: terminationSignal must be between 1 and 64")
	}
	return nil
}

// wireUint accepts a JSON number or a decimal string.  the API can send
// proto3 JSON, which encodes 64-bit integers as strings.
type wireUint int64

func (u *wireUint) UnmarshalJSON(b []byte) error {
	b = bytes.Trim(b, `"`)
	if len(b) == 0 || string(b) == "null" {
		*u = 0
		return nil
	}
	n, err := strconv.ParseInt(string(b), 10, 64)
	if err != nil || n < 0 {
		return validationErrorf("invalid unsigned integer %s", b)
	}
	*u = wireUint(n)
	return nil
}

type wireSnapshot struct {
	ID              string         `json:"id"`
	Name            *string        `json:"name"`
	SourceImageID   string         `json:"sourceImageId"`
	Status          SnapshotStatus `json:"status"`
	CompatibilityID *string        `json:"compatibilityId"`
	Resources       Resources      `json:"resources"`
	MemoryPackCount wireUint       `json:"memoryPackCount"`
	DiskPackCount   wireUint       `json:"diskPackCount"`
	StoredBytes     wireUint       `json:"storedBytes"`
	CreatedAt       time.Time      `json:"createdAt"`
	UpdatedAt       time.Time      `json:"updatedAt"`
	ExpiresAt       time.Time      `json:"expiresAt"`
	Error           *string        `json:"error"`
}

func decodeSnapshot(raw json.RawMessage) (SnapshotInfo, error) {
	var w wireSnapshot
	if len(raw) == 0 || string(raw) == "null" {
		return SnapshotInfo{}, validationErrorf("invalid sandbox snapshot: missing data")
	}
	if err := json.Unmarshal(raw, &w); err != nil {
		return SnapshotInfo{}, &ValidationError{Message: "invalid sandbox snapshot", cause: err}
	}
	s := SnapshotInfo{
		ID:              w.ID,
		SourceImageID:   w.SourceImageID,
		Status:          w.Status,
		Resources:       w.Resources,
		MemoryPackCount: int(w.MemoryPackCount),
		DiskPackCount:   int(w.DiskPackCount),
		StoredBytes:     int64(w.StoredBytes),
		CreatedAt:       w.CreatedAt,
		UpdatedAt:       w.UpdatedAt,
		ExpiresAt:       w.ExpiresAt,
	}
	if w.Name != nil {
		s.Name = *w.Name
	}
	if w.CompatibilityID != nil {
		s.CompatibilityID = *w.CompatibilityID
	}
	if w.Error != nil {
		s.Error = *w.Error
	}
	return s, checkSnapshot(s)
}

func checkSnapshot(s SnapshotInfo) error {
	if err := validateUUID(s.ID, "snapshot ID"); err != nil {
		return err
	}
	if s.SourceImageID == "" || s.CreatedAt.IsZero() || s.UpdatedAt.IsZero() || s.ExpiresAt.IsZero() {
		return validationErrorf("invalid sandbox snapshot: missing sourceImageId or timestamps")
	}
	switch s.Status {
	case SnapshotCreating, SnapshotReady, SnapshotDeleting, SnapshotDeleted, SnapshotFailed, SnapshotLost:
	default:
		return validationErrorf("invalid sandbox snapshot: unknown status %q", s.Status)
	}
	if s.Status == SnapshotReady && s.CompatibilityID == "" {
		return validationErrorf("invalid sandbox snapshot: compatibilityId is required for READY snapshots")
	}
	if s.Status == SnapshotReady && !sha256Regexp.MatchString(s.SourceImageID) {
		return validationErrorf("invalid sandbox snapshot: sourceImageId must be a lowercase SHA-256 for READY snapshots")
	}
	return checkResources(s.Resources, "sandbox snapshot")
}

// wireChunk is an output chunk as the API sends it.
type wireChunk struct {
	Stream   Stream     `json:"stream"`
	Data     string     `json:"data"`
	Encoding string     `json:"encoding"`
	At       *time.Time `json:"at,omitempty"`
}

func (w wireChunk) decode() (OutputChunk, error) {
	if w.Stream != Stdout && w.Stream != Stderr {
		return OutputChunk{}, validationErrorf("invalid output chunk: unknown stream %q", w.Stream)
	}
	if w.Encoding != "base64" {
		return OutputChunk{}, validationErrorf("invalid output chunk: unknown encoding %q", w.Encoding)
	}
	data, err := base64.StdEncoding.DecodeString(w.Data)
	if err != nil {
		return OutputChunk{}, &ValidationError{Message: "invalid output chunk: data is not valid base64", cause: err}
	}
	return OutputChunk{Stream: w.Stream, Data: data, At: w.At}, nil
}

// wireExec is the result of POST /exec.
type wireExec struct {
	Stdout   string `json:"stdout"`
	Stderr   string `json:"stderr"`
	Encoding string `json:"encoding"`
	ExitCode *int   `json:"exitCode"`
}

type jsonRaw = json.RawMessage
