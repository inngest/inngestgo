package sandbox

import (
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"
)

// fakeAPI is an in-memory sandbox API.  each state change takes effect on
// the next GET, so the SDK's polling runs at least once.
type fakeAPI struct {
	t   *testing.T
	srv *httptest.Server

	mu        sync.Mutex
	sandboxes map[string]*fakeSandbox
	snapshots map[string]map[string]any
	processes map[string]map[string]any
	files     map[string][]byte
	// calls counts requests by "METHOD /path" with IDs replaced by {id}.
	calls map[string]int
	auth  []string
	// fail makes the next matching request fail with this status and code.
	fail map[string]fakeFailure
	// execStdout overrides what exec writes to stdout.
	execStdout []byte
}

type fakeFailure struct {
	status int
	code   string
}

type fakeSandbox struct {
	info SandboxInfo
	next Status
}

func newFakeAPI(t *testing.T) *fakeAPI {
	t.Helper()
	f := &fakeAPI{
		t:         t,
		sandboxes: map[string]*fakeSandbox{},
		snapshots: map[string]map[string]any{},
		processes: map[string]map[string]any{},
		files:     map[string][]byte{},
		calls:     map[string]int{},
		fail:      map[string]fakeFailure{},
	}
	f.srv = httptest.NewServer(http.HandlerFunc(f.serve))
	t.Cleanup(f.srv.Close)

	prev := pollInterval
	pollInterval = time.Millisecond
	t.Cleanup(func() { pollInterval = prev })
	return f
}

func (f *fakeAPI) client(t *testing.T) *Client {
	t.Helper()
	c, err := NewClient(ClientOpts{APIBaseURL: f.srv.URL, SigningKey: "signkey-test-12345678"})
	if err != nil {
		t.Fatal(err)
	}
	return c
}

func (f *fakeAPI) count(key string) int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.calls[key]
}

func (f *fakeAPI) failNext(key string, status int, code string) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.fail[key] = fakeFailure{status, code}
}

var fakeNow = time.Date(2026, 10, 14, 12, 0, 0, 0, time.UTC)

func writeData(w http.ResponseWriter, status int, data any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(map[string]any{"data": data})
}

func writeList(w http.ResponseWriter, items any) {
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(map[string]any{
		"data":     items,
		"page":     map[string]any{"hasMore": false, "limit": 50},
		"metadata": map[string]any{"fetchedAt": fakeNow.Format(time.RFC3339)},
	})
}

func writeError(w http.ResponseWriter, status int, code string) {
	w.Header().Set("X-Request-Id", "req-1")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(map[string]any{
		"errors": []map[string]any{{"code": code, "message": "fake " + code}},
	})
}

func (f *fakeAPI) serve(w http.ResponseWriter, r *http.Request) {
	f.mu.Lock()
	defer f.mu.Unlock()

	parts := strings.Split(strings.Trim(r.URL.Path, "/"), "/")
	norm := make([]string, len(parts))
	for i, p := range parts {
		norm[i] = p
		if _, err := uuid.Parse(p); err == nil {
			norm[i] = "{id}"
		}
	}
	key := r.Method + " /" + strings.Join(norm, "/")
	f.calls[key]++
	f.auth = append(f.auth, r.Header.Get("Authorization"))

	if fail, ok := f.fail[key]; ok {
		delete(f.fail, key)
		writeError(w, fail.status, fail.code)
		return
	}

	switch {
	case key == "POST /v2/sandboxes":
		var body createBody
		_ = json.NewDecoder(r.Body).Decode(&body)
		res := Resources{VCPU: body.VCPU, MemoryMB: body.MemoryMB}
		if body.SnapshotID != "" {
			snap, ok := f.snapshots[body.SnapshotID]
			if !ok {
				writeError(w, 404, string(CodeSnapshotNotFound))
				return
			}
			res = snap["resources"].(Resources)
		}
		sb := &fakeSandbox{
			info: SandboxInfo{
				ID: uuid.NewString(), Name: body.Name, Status: StatusStarting,
				VPCID: uuid.NewString(), ImageRef: "image:1", Resources: res, CreatedAt: fakeNow,
			},
			next: StatusRunning,
		}
		f.sandboxes[sb.info.ID] = sb
		writeData(w, 202, sb.info)

	case key == "GET /v2/sandboxes":
		items := []SandboxInfo{}
		for _, sb := range f.sandboxes {
			items = append(items, sb.info)
		}
		writeList(w, items)

	case key == "GET /v2/snapshots":
		items := []map[string]any{}
		for _, s := range f.snapshots {
			if name := r.URL.Query().Get("name"); name == "" || s["name"] == name {
				items = append(items, s)
			}
		}
		writeList(w, items)

	case strings.HasPrefix(key, "GET /v2/snapshots/{id}"):
		snap, ok := f.snapshots[parts[2]]
		if !ok {
			writeError(w, 404, string(CodeSnapshotNotFound))
			return
		}
		snap["status"] = SnapshotReady
		snap["compatibilityId"] = "compat-1"
		writeData(w, 200, snap)

	case key == "DELETE /v2/snapshots/{id}":
		delete(f.snapshots, parts[2])
		w.WriteHeader(204)

	case len(parts) >= 3 && parts[0] == "v2" && parts[1] == "sandboxes":
		sb, ok := f.sandboxes[parts[2]]
		if !ok {
			writeError(w, 404, string(CodeNotFound))
			return
		}
		f.serveSandbox(w, r, key, sb, parts)

	default:
		f.t.Errorf("unexpected request %s", key)
		w.WriteHeader(500)
	}
}

func (f *fakeAPI) serveSandbox(w http.ResponseWriter, r *http.Request, key string, sb *fakeSandbox, parts []string) {
	switch key {
	case "GET /v2/sandboxes/{id}":
		if sb.next != "" {
			sb.info.Status, sb.next = sb.next, ""
		}
		writeData(w, 200, sb.info)

	case "DELETE /v2/sandboxes/{id}":
		delete(f.sandboxes, sb.info.ID)
		sb.info.Status = StatusTerminating
		writeData(w, 202, sb.info)

	case "POST /v2/sandboxes/{id}/pause":
		sb.info.Status, sb.next = StatusPausing, StatusPaused
		writeData(w, 202, sb.info)

	case "POST /v2/sandboxes/{id}/resume":
		sb.info.Status, sb.next = StatusResuming, StatusRunning
		writeData(w, 202, sb.info)

	case "POST /v2/sandboxes/{id}/exec":
		var body struct {
			Command     []string          `json:"command"`
			Environment map[string]string `json:"environment"`
			Timeout     string            `json:"timeout"`
		}
		_ = json.NewDecoder(r.Body).Decode(&body)
		stdout := []byte(strings.Join(body.Command, " ") + "|" + body.Timeout)
		if f.execStdout != nil {
			stdout = f.execStdout
		}
		writeData(w, 200, map[string]any{
			"stdout":   base64.StdEncoding.EncodeToString(stdout),
			"stderr":   base64.StdEncoding.EncodeToString([]byte("warn")),
			"encoding": "base64",
			"exitCode": 3,
		})

	case "POST /v2/sandboxes/{id}/processes":
		var body processSpec
		_ = json.NewDecoder(r.Body).Decode(&body)
		p := map[string]any{"id": uuid.NewString(), "command": body.Command, "pid": 42, "state": "RUNNING"}
		f.processes[p["id"].(string)] = p
		writeData(w, 201, p)

	case "GET /v2/sandboxes/{id}/processes":
		items := []map[string]any{}
		for _, p := range f.processes {
			items = append(items, p)
		}
		writeList(w, items)

	case "GET /v2/sandboxes/{id}/processes/{id}":
		p, ok := f.processes[parts[4]]
		if !ok {
			writeError(w, 404, string(CodeProcessNotFound))
			return
		}
		writeData(w, 200, p)

	case "POST /v2/sandboxes/{id}/processes/{id}/signals":
		var body map[string]any
		_ = json.NewDecoder(r.Body).Decode(&body)
		if body["includeChildren"] != true {
			f.t.Errorf("signal must include children by default, got %v", body)
		}
		f.processes[parts[4]]["state"] = "KILLED"
		f.processes[parts[4]]["terminationSignal"] = body["signal"]
		w.WriteHeader(204)

	case "POST /v2/sandboxes/{id}/processes/{id}/wait":
		if got := r.URL.Query().Get("timeout"); got != "30000ms" {
			f.t.Errorf("wait timeout = %q", got)
		}
		writeData(w, 200, map[string]any{"id": parts[4], "state": "EXITED", "exitCode": 0})

	case "GET /v2/sandboxes/{id}/processes/{id}/output":
		writeData(w, 200, map[string]any{"chunks": []map[string]any{
			{"stream": "STDOUT", "data": base64.StdEncoding.EncodeToString([]byte("hello")), "encoding": "base64"},
		}})

	case "GET /v2/sandboxes/{id}/processes/{id}/output/stream", "GET /v2/sandboxes/{id}/logs":
		for _, s := range []string{"one", "two"} {
			_, _ = fmt.Fprintf(w, `{"type":"log","stream":"STDOUT","data":%q,"encoding":"base64"}`+"\n\n", base64.StdEncoding.EncodeToString([]byte(s)))
		}

	case "PUT /v2/sandboxes/{id}/files":
		byt, _ := io.ReadAll(r.Body)
		f.files[r.URL.Query().Get("path")] = byt
		writeData(w, 200, map[string]any{"path": r.URL.Query().Get("path"), "bytesWritten": len(byt)})

	case "GET /v2/sandboxes/{id}/files":
		byt, ok := f.files[r.URL.Query().Get("path")]
		if !ok {
			writeError(w, 404, "file_not_found")
			return
		}
		_, _ = w.Write(byt)

	case "POST /v2/sandboxes/{id}/snapshots":
		var body struct {
			Name string `json:"name"`
		}
		_ = json.NewDecoder(r.Body).Decode(&body)
		snap := map[string]any{
			"id": uuid.NewString(), "sourceImageId": strings.Repeat("a", 64), "status": "CREATING",
			"resources": sb.info.Resources, "storedBytes": "1024",
			"createdAt": fakeNow, "updatedAt": fakeNow, "expiresAt": fakeNow.Add(time.Hour),
		}
		if body.Name != "" {
			snap["name"] = body.Name
		}
		f.snapshots[snap["id"].(string)] = snap
		writeData(w, 202, snap)

	default:
		f.t.Errorf("unexpected request %s", key)
		w.WriteHeader(500)
	}
}
