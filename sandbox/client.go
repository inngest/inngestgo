package sandbox

import (
	"context"
	"encoding/base64"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"slices"
	"strings"
	"time"

	"github.com/inngest/inngestgo/internal/signingkey"
	"github.com/inngest/inngestgo/pkg/env"
)

// pollInterval is the delay between status checks while the SDK waits for a
// sandbox or snapshot to change state.
var pollInterval = time.Second

// ClientOpts configures a Client.  empty fields take their value from the
// same environment variables as the Inngest client.
type ClientOpts struct {
	// SigningKey defaults to INNGEST_SIGNING_KEY.
	SigningKey string
	// SigningKeyFallback defaults to INNGEST_SIGNING_KEY_FALLBACK.  the
	// client uses it when the API rejects SigningKey.
	SigningKeyFallback string
	// APIBaseURL defaults to INNGEST_API_BASE_URL, then INNGEST_BASE_URL,
	// then the dev server in dev mode, then https://api.inngest.com.
	APIBaseURL string
	// Env defaults to INNGEST_ENV.  it selects a branch environment.
	Env string
	// Dev defaults to whether INNGEST_DEV is set.  in dev mode the client
	// does not need a signing key.
	Dev *bool
	// HTTPClient defaults to http.DefaultClient.
	HTTPClient *http.Client
}

// Client calls the sandbox API directly.  its calls are not durable.  each
// call runs every time your code reaches it.  inside an Inngest function, use
// the package functions such as Create, which run each operation as a step.
type Client struct {
	t *transport
}

// NewClient returns a Client.  it fails only when a signing key is malformed.
func NewClient(opts ClientOpts) (*Client, error) {
	dev := env.IsDev()
	if opts.Dev != nil {
		dev = *opts.Dev
	}
	t := &transport{
		baseURL: firstNonEmpty(opts.APIBaseURL, os.Getenv("INNGEST_API_BASE_URL"), os.Getenv("INNGEST_BASE_URL")),
		env:     firstNonEmpty(opts.Env, os.Getenv("INNGEST_ENV")),
		dev:     dev,
		http:    opts.HTTPClient,
	}
	if t.baseURL == "" {
		t.baseURL = "https://api.inngest.com"
		if dev {
			t.baseURL = env.DevServerURL()
		}
	}
	if t.http == nil {
		t.http = http.DefaultClient
	}

	var err error
	if t.key, err = hashKey(firstNonEmpty(opts.SigningKey, os.Getenv("INNGEST_SIGNING_KEY"))); err != nil {
		return nil, fmt.Errorf("invalid signing key: %w", err)
	}
	if t.fallbackKey, err = hashKey(firstNonEmpty(opts.SigningKeyFallback, os.Getenv("INNGEST_SIGNING_KEY_FALLBACK"))); err != nil {
		return nil, fmt.Errorf("invalid fallback signing key: %w", err)
	}
	return &Client{t: t}, nil
}

func hashKey(key string) (string, error) {
	if key == "" {
		return "", nil
	}
	hashed, err := signingkey.Hash([]byte(key))
	return string(hashed), err
}

func firstNonEmpty(values ...string) string {
	for _, v := range values {
		if v != "" {
			return v
		}
	}
	return ""
}

func sandboxPath(id string) string {
	return "/v2/sandboxes/" + url.PathEscape(id)
}

func processPath(sandboxID, processID string) string {
	return sandboxPath(sandboxID) + "/processes/" + url.PathEscape(processID)
}

func snapshotPath(id string) string {
	return "/v2/snapshots/" + url.PathEscape(id)
}

// Create creates a sandbox and, unless opts.NoWait is set, waits for it to
// reach RUNNING.
func (c *Client) Create(ctx context.Context, opts CreateOpts) (*Sandbox, error) {
	body, wait, err := normalizeCreate(opts)
	if err != nil {
		return nil, err
	}
	return c.create(ctx, body, wait)
}

func (c *Client) create(ctx context.Context, body createBody, wait time.Duration) (*Sandbox, error) {
	status, env, err := c.t.json(ctx, request{
		action:   ActionCreate,
		method:   http.MethodPost,
		path:     "/v2/sandboxes",
		body:     body,
		statuses: []int{200, 201, 202},
	})
	if err != nil {
		return nil, err
	}
	info, err := decodeSandbox(env.Data)
	if err != nil {
		return nil, err
	}
	if (status == 201 && info.Status != StatusRunning) || (status == 202 && info.Status != StatusStarting) {
		return nil, validationErrorf("sandbox create returned HTTP %d with status %s", status, info.Status)
	}
	if wait == 0 {
		return c.sandbox(info), nil
	}
	return c.waitUntilRunning(ctx, info, wait, ActionCreate)
}

// List lists sandboxes.
func (c *Client) List(ctx context.Context, opts ListOpts) (ListResult[*Sandbox], error) {
	cursor, limit, err := normalizeList(opts.Cursor, opts.Limit)
	if err != nil {
		return ListResult[*Sandbox]{}, err
	}
	_, env, err := c.t.json(ctx, request{
		action:   ActionList,
		method:   http.MethodGet,
		path:     "/v2/sandboxes",
		query:    listQuery(cursor, limit),
		statuses: []int{200},
	})
	if err != nil {
		return ListResult[*Sandbox]{}, err
	}
	page, fetchedAt, err := listPage(env, "sandbox list")
	if err != nil {
		return ListResult[*Sandbox]{}, err
	}
	raws, err := listData[jsonRaw](env, "sandbox list")
	if err != nil {
		return ListResult[*Sandbox]{}, err
	}
	out := ListResult[*Sandbox]{Items: make([]*Sandbox, 0, len(raws)), Page: page, FetchedAt: fetchedAt}
	for _, raw := range raws {
		info, err := decodeSandbox(raw)
		if err != nil {
			return ListResult[*Sandbox]{}, err
		}
		out.Items = append(out.Items, c.sandbox(info))
	}
	return out, nil
}

// Get loads a sandbox.  it returns nil and no error when the sandbox does not
// exist.
func (c *Client) Get(ctx context.Context, sandboxID string) (*Sandbox, error) {
	if err := validateUUID(sandboxID, "sandbox ID"); err != nil {
		return nil, err
	}
	info, err := c.get(ctx, sandboxID, ActionGet)
	if IsNotFound(err) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	return c.sandbox(info), nil
}

func (c *Client) get(ctx context.Context, sandboxID string, action Action) (SandboxInfo, error) {
	_, env, err := c.t.json(ctx, request{
		action:    action,
		method:    http.MethodGet,
		path:      sandboxPath(sandboxID),
		statuses:  []int{200},
		sandboxID: sandboxID,
	})
	if err != nil {
		return SandboxInfo{}, err
	}
	return decodeSandbox(env.Data)
}

// ListSnapshots lists snapshots.
func (c *Client) ListSnapshots(ctx context.Context, opts SnapshotListOpts) (ListResult[*Snapshot], error) {
	infos, err := c.listSnapshots(ctx, opts)
	if err != nil {
		return ListResult[*Snapshot]{}, err
	}
	out := ListResult[*Snapshot]{Items: make([]*Snapshot, 0, len(infos.Items)), Page: infos.Page, FetchedAt: infos.FetchedAt}
	for _, info := range infos.Items {
		out.Items = append(out.Items, c.snapshot(info))
	}
	return out, nil
}

func (c *Client) listSnapshots(ctx context.Context, opts SnapshotListOpts) (ListResult[SnapshotInfo], error) {
	cursor, limit, err := normalizeList(opts.Cursor, opts.Limit)
	if err != nil {
		return ListResult[SnapshotInfo]{}, err
	}
	q := listQuery(cursor, limit)
	if opts.Name != "" {
		if err := validateName(opts.Name, "name"); err != nil {
			return ListResult[SnapshotInfo]{}, err
		}
		q.Set("name", opts.Name)
	}
	_, env, err := c.t.json(ctx, request{
		action:   ActionSnapshotList,
		method:   http.MethodGet,
		path:     "/v2/snapshots",
		query:    q,
		statuses: []int{200},
	})
	if err != nil {
		return ListResult[SnapshotInfo]{}, err
	}
	page, fetchedAt, err := listPage(env, "snapshot list")
	if err != nil {
		return ListResult[SnapshotInfo]{}, err
	}
	raws, err := listData[jsonRaw](env, "snapshot list")
	if err != nil {
		return ListResult[SnapshotInfo]{}, err
	}
	out := ListResult[SnapshotInfo]{Items: make([]SnapshotInfo, 0, len(raws)), Page: page, FetchedAt: fetchedAt}
	for _, raw := range raws {
		info, err := decodeSnapshot(raw)
		if err != nil {
			return ListResult[SnapshotInfo]{}, err
		}
		out.Items = append(out.Items, info)
	}
	return out, nil
}

// GetSnapshot loads a snapshot.  it returns nil and no error when the
// snapshot does not exist.
func (c *Client) GetSnapshot(ctx context.Context, snapshotID string) (*Snapshot, error) {
	if err := validateUUID(snapshotID, "snapshot ID"); err != nil {
		return nil, err
	}
	info, err := c.getSnapshot(ctx, snapshotID, ActionSnapshotGet)
	if IsNotFound(err) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	return c.snapshot(info), nil
}

func (c *Client) getSnapshot(ctx context.Context, snapshotID string, action Action) (SnapshotInfo, error) {
	_, env, err := c.t.json(ctx, request{
		action:     action,
		method:     http.MethodGet,
		path:       snapshotPath(snapshotID),
		statuses:   []int{200},
		snapshotID: snapshotID,
	})
	if err != nil {
		return SnapshotInfo{}, err
	}
	return decodeSnapshot(env.Data)
}

func listQuery(cursor string, limit int) url.Values {
	q := url.Values{"limit": {formatInt(int64(limit))}}
	if cursor != "" {
		q.Set("cursor", cursor)
	}
	return q
}

// waitUntilRunning polls until the sandbox is RUNNING.  a retryable API
// error does not end the wait, so a short outage does not fail a start.
func (c *Client) waitUntilRunning(ctx context.Context, info SandboxInfo, timeout time.Duration, action Action) (*Sandbox, error) {
	pollCtx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	deadline, _ := pollCtx.Deadline()
	timedOut := func() *Error {
		return &Error{
			Action:    action,
			Code:      CodeStartTimedOut,
			Message:   fmt.Sprintf("Sandbox did not reach RUNNING within %s", timeout),
			SandboxID: info.ID,
			Details:   []map[string]any{{"status": string(info.Status), "timeoutMs": timeout.Milliseconds()}},
		}
	}
	for {
		switch info.Status {
		case StatusRunning:
			return c.sandbox(info), nil
		case StatusPending, StatusStarting:
		default:
			e := &Error{
				Action:    action,
				Code:      CodeStartFailed,
				Message:   fmt.Sprintf("Sandbox entered %s before reaching RUNNING", info.Status),
				SandboxID: info.ID,
				Details:   []map[string]any{{"status": string(info.Status)}},
			}
			if info.Error != "" {
				e.Details[0]["error"] = info.Error
			}
			return nil, e
		}

		remaining := time.Until(deadline)
		if remaining <= 0 {
			return nil, pollError(ctx, timedOut())
		}
		if err := sleep(pollCtx, min(pollInterval, remaining)); err != nil {
			return nil, pollError(ctx, timedOut())
		}

		next, err := c.get(pollCtx, info.ID, action)
		if err != nil {
			if pollCtx.Err() != nil {
				return nil, pollError(ctx, timedOut())
			}
			if isRetryable(err) {
				continue
			}
			return nil, err
		}
		info = next
	}
}

// pollStatus waits for a pause or resume to reach target.
func (c *Client) pollStatus(ctx context.Context, info SandboxInfo, target Status, action Action, timeout time.Duration) (*Sandbox, error) {
	pollCtx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	deadline, _ := pollCtx.Deadline()
	timedOut := func() *Error {
		return &Error{
			Action:    action,
			Code:      CodeStartTimedOut,
			Message:   fmt.Sprintf("Sandbox did not reach %s within %s", target, timeout),
			SandboxID: info.ID,
		}
	}
	for info.Status != target {
		if info.Status == StatusFailed || info.Status == StatusTerminated {
			return nil, &Error{
				Action:    action,
				Code:      CodeInvalidRequest,
				Message:   fmt.Sprintf("Sandbox entered %s before reaching %s", info.Status, target),
				SandboxID: info.ID,
			}
		}
		remaining := time.Until(deadline)
		if remaining <= 0 {
			return nil, pollError(ctx, timedOut())
		}
		if err := sleep(pollCtx, min(pollInterval, remaining)); err != nil {
			return nil, pollError(ctx, timedOut())
		}
		next, err := c.get(pollCtx, info.ID, action)
		if err != nil {
			if pollCtx.Err() != nil {
				return nil, pollError(ctx, timedOut())
			}
			return nil, err
		}
		if next.ID != info.ID || next.Name != info.Name {
			return nil, validationErrorf("sandbox %s returned an unrelated sandbox", action)
		}
		info = next
	}
	return c.sandbox(info), nil
}

func (c *Client) createSnapshot(ctx context.Context, sandboxID, name string) (SnapshotInfo, error) {
	r := request{
		action:    ActionSnapshotCreate,
		method:    http.MethodPost,
		path:      sandboxPath(sandboxID) + "/snapshots",
		statuses:  []int{201, 202},
		sandboxID: sandboxID,
	}
	// an unnamed snapshot request has no body at all.
	if name != "" {
		r.body = map[string]string{"name": name}
	}
	status, env, err := c.t.json(ctx, r)
	if err != nil {
		return SnapshotInfo{}, err
	}
	snap, err := decodeSnapshot(env.Data)
	if err != nil {
		return SnapshotInfo{}, err
	}
	if (status == 201 && snap.Status != SnapshotReady) || (status == 202 && snap.Status != SnapshotCreating) {
		return SnapshotInfo{}, validationErrorf("snapshot create returned HTTP %d with status %s", status, snap.Status)
	}
	return snap, nil
}

// waitUntilSnapshotReady polls until the snapshot is READY.  a retryable API
// error does not end the wait.
func (c *Client) waitUntilSnapshotReady(ctx context.Context, snap SnapshotInfo, timeout time.Duration) (SnapshotInfo, error) {
	pollCtx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	deadline, _ := pollCtx.Deadline()
	timedOut := func() *Error {
		return &Error{
			Action:     ActionSnapshotWait,
			Code:       CodeSnapshotWaitTimedOut,
			Message:    fmt.Sprintf("Snapshot did not reach READY within %s", timeout),
			SnapshotID: snap.ID,
			Details:    []map[string]any{{"status": string(snap.Status), "timeoutMs": timeout.Milliseconds()}},
		}
	}
	for {
		switch snap.Status {
		case SnapshotReady:
			return snap, nil
		case SnapshotCreating:
		default:
			return SnapshotInfo{}, &Error{
				Action:     ActionSnapshotWait,
				Code:       CodeSnapshotNotReady,
				Message:    fmt.Sprintf("Snapshot entered %s before reaching READY", snap.Status),
				SnapshotID: snap.ID,
				Details:    []map[string]any{{"status": string(snap.Status)}},
			}
		}
		remaining := time.Until(deadline)
		if remaining <= 0 {
			return SnapshotInfo{}, pollError(ctx, timedOut())
		}
		if err := sleep(pollCtx, min(pollInterval, remaining)); err != nil {
			return SnapshotInfo{}, pollError(ctx, timedOut())
		}
		next, err := c.getSnapshot(pollCtx, snap.ID, ActionSnapshotWait)
		if err != nil {
			if pollCtx.Err() != nil {
				return SnapshotInfo{}, pollError(ctx, timedOut())
			}
			if isRetryable(err) {
				continue
			}
			return SnapshotInfo{}, err
		}
		snap = next
	}
}

func pollError(ctx context.Context, timeoutErr *Error) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	return timeoutErr
}

func (c *Client) deleteSnapshot(ctx context.Context, snapshotID string) error {
	_, _, err := c.t.json(ctx, request{
		action:     ActionSnapshotDelete,
		method:     http.MethodDelete,
		path:       snapshotPath(snapshotID),
		statuses:   []int{204},
		snapshotID: snapshotID,
	})
	return err
}

func isRetryable(err error) bool {
	e, ok := err.(*Error)
	return ok && e.Retryable
}

func sleep(ctx context.Context, d time.Duration) error {
	t := time.NewTimer(d)
	defer t.Stop()
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-t.C:
		return nil
	}
}

// Sandbox is a sandbox loaded through a Client.  its fields are a snapshot of
// the sandbox when it was loaded.  methods that change state return a new
// Sandbox.
type Sandbox struct {
	SandboxInfo
	c *Client
}

func (c *Client) sandbox(info SandboxInfo) *Sandbox {
	return &Sandbox{SandboxInfo: info, c: c}
}

// Run runs cmd and waits for it to exit.  a non-zero exit code is a result,
// not an error.
func (s *Sandbox) Run(ctx context.Context, cmd Command, opts CommandOpts) (CommandResult, error) {
	spec, err := normalizeSpec(cmd, opts.Environment, opts.Cwd)
	if err != nil {
		return CommandResult{}, err
	}
	timeout, err := duration(opts.Timeout, defaultCommandWait, maxCommandTimeout, "Timeout")
	if err != nil {
		return CommandResult{}, err
	}
	return s.run(ctx, spec, timeout)
}

func (s *Sandbox) run(ctx context.Context, spec processSpec, timeout time.Duration) (CommandResult, error) {
	_, env, err := s.c.t.json(ctx, request{
		action: ActionExec,
		method: http.MethodPost,
		path:   sandboxPath(s.ID) + "/exec",
		body: struct {
			processSpec
			Timeout string `json:"timeout"`
		}{spec, msString(timeout)},
		statuses:  []int{200},
		sandboxID: s.ID,
	})
	if err != nil {
		return CommandResult{}, err
	}
	w, err := decodeData[wireExec](env, "sandbox exec result")
	if err != nil {
		return CommandResult{}, err
	}
	if w.Encoding != "base64" {
		return CommandResult{}, validationErrorf("invalid sandbox exec result: unknown encoding %q", w.Encoding)
	}
	if w.ExitCode == nil || int64(*w.ExitCode) < -0x80000000 || int64(*w.ExitCode) > 0x7fffffff {
		return CommandResult{}, validationErrorf("invalid sandbox exec result: exitCode must be a signed 32-bit integer")
	}
	stdout, err := base64.StdEncoding.DecodeString(w.Stdout)
	if err != nil {
		return CommandResult{}, &ValidationError{Message: "sandbox exec stdout is not valid base64", cause: err}
	}
	stderr, err := base64.StdEncoding.DecodeString(w.Stderr)
	if err != nil {
		return CommandResult{}, &ValidationError{Message: "sandbox exec stderr is not valid base64", cause: err}
	}
	res := CommandResult{Stdout: string(stdout), Stderr: string(stderr), ExitCode: *w.ExitCode}
	return res, nil
}

// Logs streams the sandbox log.
func (s *Sandbox) Logs(ctx context.Context, opts LogsOpts) (*OutputStream, error) {
	return s.c.t.stream(ctx, request{
		action:    ActionLogsStream,
		path:      sandboxPath(s.ID) + "/logs",
		query:     url.Values{"follow": {fmt.Sprint(opts.Follow)}},
		sandboxID: s.ID,
	})
}

// Upload writes a file into the sandbox.  the API replaces any file at the
// path, so an upload can be repeated safely.
func (s *Sandbox) Upload(ctx context.Context, opts UploadOpts) (UploadResult, error) {
	if err := validateFilePath(opts.Path); err != nil {
		return UploadResult{}, err
	}
	if opts.Mode&^os.ModePerm != 0 {
		return UploadResult{}, validationErrorf("Mode must contain permission bits only")
	}
	if opts.Data == nil {
		opts.Data = strings.NewReader("")
	}
	q := url.Values{"path": {opts.Path}}
	if opts.Mode != 0 {
		q.Set("mode", fmt.Sprintf("%04o", uint32(opts.Mode)))
	}
	_, env, err := s.c.t.json(ctx, request{
		action:      ActionFileUpload,
		method:      http.MethodPut,
		path:        sandboxPath(s.ID) + "/files",
		query:       q,
		raw:         opts.Data,
		contentType: "application/octet-stream",
		sandboxID:   s.ID,
	})
	if err != nil {
		return UploadResult{}, err
	}
	res, err := decodeData[UploadResult](env, "sandbox file upload result")
	if err != nil {
		return UploadResult{}, err
	}
	if res.BytesWritten < 0 {
		return UploadResult{}, validationErrorf("invalid sandbox file upload result: negative bytesWritten")
	}
	return res, nil
}

// Download reads a file from the sandbox.  close the returned reader.
func (s *Sandbox) Download(ctx context.Context, path string) (io.ReadCloser, error) {
	if err := validateFilePath(path); err != nil {
		return nil, err
	}
	resp, err := s.c.t.do(ctx, request{
		action:    ActionFileDownload,
		method:    http.MethodGet,
		path:      sandboxPath(s.ID) + "/files",
		query:     url.Values{"path": {path}},
		sandboxID: s.ID,
	})
	if err != nil {
		return nil, err
	}
	return resp.Body, nil
}

// StartProcess starts a background process and returns once it runs.
func (s *Sandbox) StartProcess(ctx context.Context, opts ProcessOpts) (*Process, error) {
	spec, err := normalizeSpec(opts.Command, opts.Environment, opts.Cwd)
	if err != nil {
		return nil, err
	}
	info, err := s.startProcess(ctx, spec)
	if err != nil {
		return nil, err
	}
	return s.c.process(info), nil
}

func (s *Sandbox) startProcess(ctx context.Context, spec processSpec) (ProcessInfo, error) {
	_, env, err := s.c.t.json(ctx, request{
		action:    ActionProcessStart,
		method:    http.MethodPost,
		path:      sandboxPath(s.ID) + "/processes",
		body:      spec,
		statuses:  []int{201},
		sandboxID: s.ID,
	})
	if err != nil {
		return ProcessInfo{}, err
	}
	info, err := decodeProcess(s.ID, env.Data)
	if err != nil {
		return ProcessInfo{}, err
	}
	if info.State != ProcessRunning || info.PID == 0 {
		return ProcessInfo{}, validationErrorf("sandbox process start returned a process that is not RUNNING")
	}
	return info, nil
}

// ListProcesses lists the sandbox's processes, sorted by ID.
func (s *Sandbox) ListProcesses(ctx context.Context, opts ListOpts) (ListResult[*Process], error) {
	infos, err := s.listProcesses(ctx, opts)
	if err != nil {
		return ListResult[*Process]{}, err
	}
	out := ListResult[*Process]{Items: make([]*Process, 0, len(infos.Items)), Page: infos.Page, FetchedAt: infos.FetchedAt}
	for _, info := range infos.Items {
		out.Items = append(out.Items, s.c.process(info))
	}
	return out, nil
}

func (s *Sandbox) listProcesses(ctx context.Context, opts ListOpts) (ListResult[ProcessInfo], error) {
	cursor, limit, err := normalizeList(opts.Cursor, opts.Limit)
	if err != nil {
		return ListResult[ProcessInfo]{}, err
	}
	_, env, err := s.c.t.json(ctx, request{
		action:    ActionProcessList,
		method:    http.MethodGet,
		path:      sandboxPath(s.ID) + "/processes",
		query:     listQuery(cursor, limit),
		statuses:  []int{200},
		sandboxID: s.ID,
	})
	if err != nil {
		return ListResult[ProcessInfo]{}, err
	}
	page, fetchedAt, err := listPage(env, "process list")
	if err != nil {
		return ListResult[ProcessInfo]{}, err
	}
	raws, err := listData[jsonRaw](env, "process list")
	if err != nil {
		return ListResult[ProcessInfo]{}, err
	}
	out := ListResult[ProcessInfo]{Items: make([]ProcessInfo, 0, len(raws)), Page: page, FetchedAt: fetchedAt}
	for _, raw := range raws {
		info, err := decodeProcess(s.ID, raw)
		if err != nil {
			return ListResult[ProcessInfo]{}, err
		}
		out.Items = append(out.Items, info)
	}
	slices.SortFunc(out.Items, func(a, b ProcessInfo) int { return strings.Compare(a.ID, b.ID) })
	return out, nil
}

// GetProcess loads a process.  it returns nil and no error when the process
// does not exist.
func (s *Sandbox) GetProcess(ctx context.Context, processID string) (*Process, error) {
	info, err := s.getProcess(ctx, processID)
	if err != nil || info == nil {
		return nil, err
	}
	return s.c.process(*info), nil
}

func (s *Sandbox) getProcess(ctx context.Context, processID string) (*ProcessInfo, error) {
	if err := validateUUID(processID, "process ID"); err != nil {
		return nil, err
	}
	_, env, err := s.c.t.json(ctx, request{
		action:    ActionProcessGet,
		method:    http.MethodGet,
		path:      processPath(s.ID, processID),
		statuses:  []int{200},
		sandboxID: s.ID,
		processID: processID,
	})
	if IsNotFound(err) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	info, err := decodeProcess(s.ID, env.Data)
	if err != nil {
		return nil, err
	}
	return &info, nil
}

// WaitUntilRunning waits for a STARTING sandbox to reach RUNNING.  timeout
// must be set and must not exceed 5 minutes.
func (s *Sandbox) WaitUntilRunning(ctx context.Context, timeout time.Duration) (*Sandbox, error) {
	if timeout == 0 {
		return nil, validationErrorf("timeout must be set")
	}
	timeout, err := duration(timeout, 0, maxRunningTimeout, "timeout")
	if err != nil {
		return nil, err
	}
	return s.c.waitUntilRunning(ctx, s.SandboxInfo, timeout, ActionWaitUntilRunning)
}

// Pause pauses the sandbox and waits for it to reach PAUSED.
func (s *Sandbox) Pause(ctx context.Context, opts LifecycleOpts) (*Sandbox, error) {
	return s.lifecycle(ctx, ActionPause, "/pause", StatusPaused, opts)
}

// Resume resumes a paused sandbox and waits for it to reach RUNNING.
func (s *Sandbox) Resume(ctx context.Context, opts LifecycleOpts) (*Sandbox, error) {
	return s.lifecycle(ctx, ActionResume, "/resume", StatusRunning, opts)
}

func (s *Sandbox) lifecycle(ctx context.Context, action Action, suffix string, target Status, opts LifecycleOpts) (*Sandbox, error) {
	timeout, err := duration(opts.Timeout, maxLifecycleTimeout, maxLifecycleTimeout, "Timeout")
	if err != nil {
		return nil, err
	}
	_, env, err := s.c.t.json(ctx, request{
		action:    action,
		method:    http.MethodPost,
		path:      sandboxPath(s.ID) + suffix,
		statuses:  []int{200, 201, 202},
		sandboxID: s.ID,
	})
	if err != nil {
		return nil, err
	}
	accepted := s.SandboxInfo
	if env != nil && len(env.Data) > 0 && string(env.Data) != "null" {
		if accepted, err = decodeSandbox(env.Data); err != nil {
			return nil, err
		}
	}
	return s.c.pollStatus(ctx, accepted, target, action, timeout)
}

// Snapshot snapshots the sandbox and waits for the snapshot to be READY.
func (s *Sandbox) Snapshot(ctx context.Context, opts SnapshotOpts) (*Snapshot, error) {
	if opts.Name != "" {
		if err := validateName(opts.Name, "name"); err != nil {
			return nil, err
		}
	}
	timeout, err := duration(opts.Timeout, maxLifecycleTimeout, maxLifecycleTimeout, "Timeout")
	if err != nil {
		return nil, err
	}
	snap, err := s.c.createSnapshot(ctx, s.ID, opts.Name)
	if err != nil {
		return nil, err
	}
	if snap, err = s.c.waitUntilSnapshotReady(ctx, snap, timeout); err != nil {
		return nil, err
	}
	return s.c.snapshot(snap), nil
}

// Destroy terminates the sandbox.
func (s *Sandbox) Destroy(ctx context.Context) (DestroyResult, error) {
	status, env, err := s.c.t.json(ctx, request{
		action:    ActionDestroy,
		method:    http.MethodDelete,
		path:      sandboxPath(s.ID),
		statuses:  []int{202, 204},
		sandboxID: s.ID,
	})
	if err != nil {
		return DestroyResult{}, err
	}
	if status == http.StatusNoContent {
		return DestroyResult{Status: StatusTerminated}, nil
	}
	info, err := decodeSandbox(env.Data)
	if err != nil {
		return DestroyResult{}, err
	}
	if info.Status != StatusTerminating {
		return DestroyResult{}, validationErrorf("sandbox destroy returned a sandbox that is not TERMINATING")
	}
	return DestroyResult{Status: StatusTerminating, Sandbox: &info}, nil
}

// Process is a background process loaded through a Client.
type Process struct {
	ProcessInfo
	c *Client
}

func (c *Client) process(info ProcessInfo) *Process {
	return &Process{ProcessInfo: info, c: c}
}

func (p *Process) request(action Action, method, suffix string) request {
	return request{
		action:    action,
		method:    method,
		path:      processPath(p.SandboxID, p.ID) + suffix,
		sandboxID: p.SandboxID,
		processID: p.ID,
	}
}

// Signal sends a signal to the process.
func (p *Process) Signal(ctx context.Context, opts SignalOpts) error {
	if err := normalizeSignal(opts); err != nil {
		return err
	}
	r := p.request(ActionProcessSignal, http.MethodPost, "/signals")
	r.body = map[string]any{"signal": opts.Signal, "includeChildren": !opts.ExcludeChildren}
	r.statuses = []int{204}
	_, _, err := p.c.t.json(ctx, r)
	return err
}

// Wait waits for the process to stop and returns its final state.
func (p *Process) Wait(ctx context.Context, opts WaitOpts) (*Process, error) {
	timeout, err := duration(opts.Timeout, defaultCommandWait, maxCommandTimeout, "Timeout")
	if err != nil {
		return nil, err
	}
	info, err := p.wait(ctx, timeout)
	if err != nil {
		return nil, err
	}
	return p.c.process(info), nil
}

func (p *Process) wait(ctx context.Context, timeout time.Duration) (ProcessInfo, error) {
	r := p.request(ActionProcessWait, http.MethodPost, "/wait")
	r.query = url.Values{"timeout": {msString(timeout)}}
	r.statuses = []int{200}
	_, env, err := p.c.t.json(ctx, r)
	if err != nil {
		return ProcessInfo{}, err
	}
	final, err := decodeData[ProcessInfo](env, "sandbox process wait result")
	if err != nil {
		return ProcessInfo{}, err
	}
	if !final.State.Done() {
		return ProcessInfo{}, validationErrorf("invalid sandbox process wait result: state %s has not stopped", final.State)
	}
	info := p.ProcessInfo
	info.State, info.ExitCode, info.TerminationSignal = final.State, final.ExitCode, final.TerminationSignal
	if final.StartedAt != nil {
		info.StartedAt = final.StartedAt
	}
	if final.EndedAt != nil {
		info.EndedAt = final.EndedAt
	}
	return info, checkProcess(info)
}

// Output returns the retained output of the process.
func (p *Process) Output(ctx context.Context, opts OutputOpts) (ProcessOutput, error) {
	if err := normalizeTail(opts); err != nil {
		return ProcessOutput{}, err
	}
	r := p.request(ActionProcessOutput, http.MethodGet, "/output")
	r.query = url.Values{"tailBytes": {formatInt(int64(opts.TailBytes))}}
	r.statuses = []int{200}
	_, env, err := p.c.t.json(ctx, r)
	if err != nil {
		return ProcessOutput{}, err
	}
	w, err := decodeData[struct {
		Chunks []wireChunk `json:"chunks"`
	}](env, "sandbox process output")
	if err != nil {
		return ProcessOutput{}, err
	}
	out := ProcessOutput{Chunks: make([]OutputChunk, 0, len(w.Chunks))}
	for _, wc := range w.Chunks {
		chunk, err := wc.decode()
		if err != nil {
			return ProcessOutput{}, err
		}
		out.Chunks = append(out.Chunks, chunk)
	}
	return out, nil
}

// StreamOutput streams the output of the process as it is written.
func (p *Process) StreamOutput(ctx context.Context, opts OutputOpts) (*OutputStream, error) {
	if err := normalizeTail(opts); err != nil {
		return nil, err
	}
	r := p.request(ActionProcessStream, http.MethodGet, "/output/stream")
	r.query = url.Values{"tailBytes": {formatInt(int64(opts.TailBytes))}}
	return p.c.t.stream(ctx, r)
}

// Snapshot is a snapshot loaded through a Client.
type Snapshot struct {
	SnapshotInfo
	c *Client
}

func (c *Client) snapshot(info SnapshotInfo) *Snapshot {
	return &Snapshot{SnapshotInfo: info, c: c}
}

// Clone creates a sandbox from the snapshot.
func (s *Snapshot) Clone(ctx context.Context, opts CloneOpts) (*Sandbox, error) {
	return s.c.Create(ctx, CreateOpts{
		Name:           opts.Name,
		SnapshotID:     s.ID,
		RunningTimeout: opts.RunningTimeout,
		NoWait:         opts.NoWait,
	})
}

// Delete deletes the snapshot.
func (s *Snapshot) Delete(ctx context.Context) error {
	return s.c.deleteSnapshot(ctx, s.ID)
}
