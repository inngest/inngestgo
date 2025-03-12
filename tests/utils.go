package tests

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/inngest/inngestgo"
)

const (
	StatusFailed = "Failed"
)

type Run struct {
	Output any
	Status string `json:"status"`
}

func getRun(id string) (*Run, error) {
	res, err := http.Get(fmt.Sprintf("http://localhost:8288/v1/runs/%s", id))
	if err != nil {
		return nil, err
	}
	defer res.Body.Close()

	byt, err := io.ReadAll(res.Body)
	if err != nil {
		return nil, err
	}

	var body struct {
		Data Run `json:"data"`
	}
	err = json.Unmarshal(byt, &body)
	if err != nil {
		return nil, err
	}

	return &body.Data, nil
}

func waitForRun(id *string, status string) (*Run, error) {
	start := time.Now()
	timeout := 5 * time.Second

	for {
		if time.Now().After(start.Add(timeout)) {
			break
		}

		if id == nil || *id != "" {
			run, err := getRun(*id)
			if err != nil {
				return nil, err
			}
			if run.Status == status {
				return run, nil
			}
		}
		<-time.After(100 * time.Millisecond)
	}

	if id == nil || *id == "" {
		return nil, fmt.Errorf("run ID is empty")
	}

	return nil, fmt.Errorf("run did not reach status %s", status)
}

func randomSuffix(s string) string {
	return s + uuid.NewString()
}

type serveOpts struct {
	Middleware func(http.Handler) http.Handler
}

func serve(
	t *testing.T,
	c inngestgo.Client,
	opts ...serveOpts,
) (*httptest.Server, func() error) {
	var o serveOpts
	if len(opts) > 0 {
		o = opts[0]
	}

	handler := c.Serve()
	if o.Middleware != nil {
		handler = o.Middleware(handler)
	}

	mux := http.NewServeMux()
	mux.HandleFunc("/", handler.ServeHTTP)
	server := httptest.NewServer(mux)

	sync := func() error {
		t.Helper()
		req, err := http.NewRequest(http.MethodPut, server.URL, nil)
		if err != nil {
			return err
		}
		resp, err := http.DefaultClient.Do(req)
		if err != nil {
			return err
		}
		byt, _ := io.ReadAll(resp.Body)
		if resp.StatusCode > 299 {
			return fmt.Errorf("invalid status code: %d (%s)", resp.StatusCode, byt)
		}
		_ = resp.Body.Close()
		return nil
	}

	return server, sync
}
