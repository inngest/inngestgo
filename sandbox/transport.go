package sandbox

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"slices"
	"strconv"
	"strings"

	"github.com/inngest/inngestgo/pkg/version"
)

// transport sends requests to the sandbox REST API.
type transport struct {
	baseURL string
	// key and fallbackKey are hashed signing keys, sent as bearer tokens.
	key         string
	fallbackKey string
	env         string
	dev         bool
	http        *http.Client
}

// request describes one API call.
type request struct {
	action Action
	method string
	path   string
	query  url.Values
	// body is encoded as JSON.  raw is sent as is, and wins over body.
	body        any
	raw         io.Reader
	contentType string
	// statuses are the success statuses.  any other status is an error.
	statuses []int

	sandboxID  string
	processID  string
	snapshotID string
}

type envelope struct {
	Data     json.RawMessage `json:"data"`
	Metadata *struct {
		FetchedAt string `json:"fetchedAt"`
	} `json:"metadata"`
	Page *struct {
		Cursor  *string `json:"cursor"`
		HasMore bool    `json:"hasMore"`
		Limit   int     `json:"limit"`
	} `json:"page"`
}

type errorItem struct {
	Code    string `json:"code"`
	Message string `json:"message"`
}

// safeRepeat lists the actions that have no extra effect when sent twice.  a
// transport failure on one of these is retryable.  on any other action the
// outcome is unknown.
var safeRepeat = map[Action]bool{
	ActionCreate:           true,
	ActionDestroy:          true,
	ActionPause:            true,
	ActionResume:           true,
	ActionFileUpload:       true,
	ActionList:             true,
	ActionGet:              true,
	ActionWaitUntilRunning: true,
	ActionProcessList:      true,
	ActionProcessGet:       true,
	ActionProcessWait:      true,
	ActionProcessOutput:    true,
	ActionProcessStream:    true,
	ActionLogsStream:       true,
	ActionFileDownload:     true,
	ActionSnapshotList:     true,
	ActionSnapshotGet:      true,
	ActionSnapshotWait:     true,
	ActionSnapshotDelete:   true,
}

func transportFailureMessage(a Action) string {
	switch a {
	case ActionCreate:
		return "The sandbox create response was not confirmed.  Repeating the same create is safe"
	case ActionDestroy:
		return "Sandbox teardown may have been accepted.  Get the sandbox; repeating destroy is safe"
	case ActionFileUpload:
		return "The sandbox file may have been replaced.  Repeating the same upload is safe"
	case ActionExec:
		return "The sandbox command may have run.  Inspect its external effects before running it again"
	case ActionProcessStart:
		return "A sandbox process may be running.  List processes and reconcile before starting another"
	case ActionProcessSignal:
		return "The sandbox process signal may have been delivered.  Get or wait for the process before sending another"
	case ActionSnapshotCreate:
		return "Snapshot creation may have started and a retry may create a second snapshot.  List snapshots before creating another"
	case ActionSnapshotDelete:
		return "Snapshot deletion may have started.  Repeating delete is safe"
	default:
		return "Compute is temporarily unavailable.  Retry this operation"
	}
}

func (r request) newError(code ErrorCode, msg string) *Error {
	return &Error{
		Action:     r.action,
		Code:       code,
		Message:    msg,
		SandboxID:  r.sandboxID,
		ProcessID:  r.processID,
		SnapshotID: r.snapshotID,
	}
}

func (r request) transportFailure(cause error) *Error {
	safe := safeRepeat[r.action]
	code := CodeOperationAmbiguous
	if safe {
		code = CodeComputeUnavailable
	}
	e := r.newError(code, transportFailureMessage(r.action))
	e.Ambiguous = !safe
	e.Retryable = safe
	e.cause = cause
	return e
}

func (r request) itemError(status int, item errorItem, requestID string, details []map[string]any) *Error {
	code := ErrorCode(item.Code)
	msg := item.Message
	ambiguous := code == CodeOperationAmbiguous || code == CodeExecOutputTooLarge || code == CodeExecTimedOut
	if code == CodeOperationAmbiguous {
		msg = transportFailureMessage(r.action)
		if r.action == ActionCreate {
			msg = "A sandbox was created, but its current resource could not be loaded.  List sandboxes and reconcile by name before creating another"
		}
	}
	availability := code == CodeComputeUnavailable || code == CodeRateLimited ||
		status == http.StatusTooManyRequests || status == http.StatusServiceUnavailable

	e := r.newError(code, msg)
	e.Status = status
	e.Ambiguous = ambiguous
	e.Retryable = !ambiguous && availability
	e.RequestID = requestID
	e.Details = details
	return e
}

func (t *transport) send(ctx context.Context, r request) (*http.Response, error) {
	if t.key == "" && !t.dev {
		return nil, validationErrorf("a signing key is required to use sandboxes; set INNGEST_SIGNING_KEY")
	}

	u, err := url.Parse(strings.TrimSuffix(t.baseURL, "/") + r.path)
	if err != nil {
		return nil, &ValidationError{Message: "invalid API base URL " + t.baseURL, cause: err}
	}
	if len(r.query) > 0 {
		u.RawQuery = r.query.Encode()
	}

	var body io.Reader
	contentType := r.contentType
	switch {
	case r.raw != nil:
		body = r.raw
	case r.body != nil:
		byt, err := json.Marshal(r.body)
		if err != nil {
			return nil, &ValidationError{Message: "request body is not valid JSON", cause: err}
		}
		body = bytes.NewReader(byt)
		contentType = "application/json"
	}

	req, err := http.NewRequestWithContext(ctx, r.method, u.String(), body)
	if err != nil {
		return nil, &ValidationError{Message: "invalid request", cause: err}
	}
	if contentType != "" {
		req.Header.Set("Content-Type", contentType)
	}
	req.Header.Set("X-Inngest-SDK", "go:v"+version.SDKVersion)
	if t.env != "" {
		req.Header.Set("X-Inngest-Env", t.env)
	}
	if t.key != "" {
		req.Header.Set("Authorization", "Bearer "+t.key)
	}

	resp, err := t.http.Do(req)
	if err != nil {
		if ctx.Err() != nil {
			return nil, ctx.Err()
		}
		return nil, r.transportFailure(err)
	}

	// a 401 means the API did not run the operation, so the same request can
	// go again with the fallback key.  a streamed body cannot be read twice.
	if resp.StatusCode == http.StatusUnauthorized && t.fallbackKey != "" && (body == nil || req.GetBody != nil) {
		retry := req.Clone(ctx)
		if req.GetBody != nil {
			if retry.Body, err = req.GetBody(); err != nil {
				return resp, nil
			}
		}
		retry.Header.Set("Authorization", "Bearer "+t.fallbackKey)
		_ = resp.Body.Close()
		resp, err = t.http.Do(retry)
		if err != nil {
			if ctx.Err() != nil {
				return nil, ctx.Err()
			}
			return nil, r.transportFailure(err)
		}
	}
	return resp, nil
}

// do sends r and returns the response when its status is a success status.
func (t *transport) do(ctx context.Context, r request) (*http.Response, error) {
	resp, err := t.send(ctx, r)
	if err != nil {
		return nil, err
	}
	ok := resp.StatusCode >= 200 && resp.StatusCode < 300
	if len(r.statuses) > 0 {
		ok = slices.Contains(r.statuses, resp.StatusCode)
	}
	if !ok {
		defer func() { _ = resp.Body.Close() }()
		return nil, t.responseError(r, resp)
	}
	return resp, nil
}

// json sends r and decodes the response envelope.  the envelope is nil for a
// 204 response.
func (t *transport) json(ctx context.Context, r request) (int, *envelope, error) {
	resp, err := t.do(ctx, r)
	if err != nil {
		return 0, nil, err
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode == http.StatusNoContent {
		return resp.StatusCode, nil, nil
	}
	env := &envelope{}
	if err := json.NewDecoder(resp.Body).Decode(env); err != nil {
		if ctx.Err() != nil {
			return 0, nil, ctx.Err()
		}
		return 0, nil, r.transportFailure(err)
	}
	return resp.StatusCode, env, nil
}

func (t *transport) responseError(r request, resp *http.Response) error {
	var parsed struct {
		Errors []json.RawMessage `json:"errors"`
	}
	byt, _ := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	item := errorItem{Code: string(CodeInternalError), Message: fmt.Sprintf("Sandbox API returned HTTP %d", resp.StatusCode)}
	var details []map[string]any
	if json.Unmarshal(byt, &parsed) == nil && len(parsed.Errors) > 0 {
		var first errorItem
		if json.Unmarshal(parsed.Errors[0], &first) == nil && first.Code != "" && first.Message != "" {
			item = first
			for _, raw := range parsed.Errors {
				var d map[string]any
				if json.Unmarshal(raw, &d) == nil {
					details = append(details, d)
				}
			}
		}
	}
	return r.itemError(resp.StatusCode, item, resp.Header.Get("X-Request-Id"), details)
}

// listPage converts an envelope's page and metadata.
func listPage(env *envelope, what string) (Page, string, error) {
	if env == nil || env.Page == nil || env.Metadata == nil || env.Metadata.FetchedAt == "" {
		return Page{}, "", validationErrorf("invalid %s: missing page or metadata", what)
	}
	p := Page{HasMore: env.Page.HasMore, Limit: env.Page.Limit}
	if env.Page.Cursor != nil {
		p.Cursor = *env.Page.Cursor
	}
	return p, env.Metadata.FetchedAt, nil
}

// listData decodes the items of a list.  the API omits data when a list is
// empty.
func listData[W any](env *envelope, what string) ([]W, error) {
	if env == nil || len(env.Data) == 0 || string(env.Data) == "null" {
		return nil, nil
	}
	var items []W
	if err := json.Unmarshal(env.Data, &items); err != nil {
		return nil, &ValidationError{Message: "invalid " + what, cause: err}
	}
	return items, nil
}

func decodeData[W any](env *envelope, what string) (W, error) {
	var w W
	if env == nil || len(env.Data) == 0 || string(env.Data) == "null" {
		return w, validationErrorf("invalid %s: missing data", what)
	}
	if err := json.Unmarshal(env.Data, &w); err != nil {
		return w, &ValidationError{Message: "invalid " + what, cause: err}
	}
	return w, nil
}

// OutputStream reads output chunks from a process or the sandbox log.  close
// it when you are done.
type OutputStream struct {
	r       request
	body    io.ReadCloser
	scanner *bufio.Scanner
	err     error
}

func (t *transport) stream(ctx context.Context, r request) (*OutputStream, error) {
	r.method = http.MethodGet
	resp, err := t.do(ctx, r)
	if err != nil {
		return nil, err
	}
	s := bufio.NewScanner(resp.Body)
	// an output frame holds base64 data, so it can be much longer than the
	// default 64 KiB line limit.
	s.Buffer(make([]byte, 64*1024), 8<<20)
	return &OutputStream{r: r, body: resp.Body, scanner: s}, nil
}

// Next returns the next chunk.  it returns io.EOF when the stream ends.
func (s *OutputStream) Next() (OutputChunk, error) {
	if s.err != nil {
		return OutputChunk{}, s.err
	}
	for s.scanner.Scan() {
		line := bytes.TrimSpace(s.scanner.Bytes())
		if len(line) == 0 {
			continue
		}
		var frame struct {
			Type  string    `json:"type"`
			Error errorItem `json:"error"`
			wireChunk
		}
		if err := json.Unmarshal(line, &frame); err != nil {
			return s.fail(&ValidationError{Message: "invalid stream frame", cause: err})
		}
		switch frame.Type {
		case "log":
			chunk, err := frame.decode()
			if err != nil {
				return s.fail(err)
			}
			return chunk, nil
		case "error":
			return s.fail(s.r.itemError(0, frame.Error, "", nil))
		default:
			return s.fail(validationErrorf("invalid stream frame type %q", frame.Type))
		}
	}
	if err := s.scanner.Err(); err != nil {
		return s.fail(s.r.transportFailure(err))
	}
	return s.fail(io.EOF)
}

func (s *OutputStream) fail(err error) (OutputChunk, error) {
	s.err = err
	_ = s.body.Close()
	return OutputChunk{}, err
}

// Close ends the stream and its HTTP request.
func (s *OutputStream) Close() error {
	if s.err == nil {
		s.err = io.EOF
	}
	return s.body.Close()
}

func formatInt(i int64) string {
	return strconv.FormatInt(i, 10)
}
