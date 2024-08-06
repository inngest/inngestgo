package tests

import (
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/inngest/inngestgo"
)

func serve(t *testing.T, h inngestgo.Handler) (*httptest.Server, func() error) {
	server := httptest.NewServer(h)

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
