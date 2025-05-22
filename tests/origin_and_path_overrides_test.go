package tests

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/inngest/inngest/pkg/sdk"
	"github.com/inngest/inngestgo"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestOriginAndPathOverrides(t *testing.T) {
	// The serve origin and path can be overridden.

	t.Run("env vars", func(t *testing.T) {
		r := require.New(t)
		t.Setenv("INNGEST_SERVE_HOST", "foo.bar.baz")
		t.Setenv("INNGEST_SERVE_PATH", "/qux/quux")

		var body *sdk.RegisterRequest
		mockInngestServer := httptest.NewServer(http.HandlerFunc(func(
			w http.ResponseWriter,
			r *http.Request,
		) {
			byt, _ := io.ReadAll(r.Body)
			_ = json.Unmarshal(byt, &body)
			w.WriteHeader(http.StatusOK)
		}))
		defer mockInngestServer.Close()

		appName := randomSuffix("app")
		ic, err := inngestgo.NewClient(inngestgo.ClientOpts{
			APIBaseURL: inngestgo.StrPtr(mockInngestServer.URL),
			AppID:      appName,
			Dev:        inngestgo.BoolPtr(true),
		})
		r.NoError(err)
		server := httptest.NewServer(ic.Serve())
		defer server.Close()

		req, err := http.NewRequest(
			"PUT",
			fmt.Sprintf("%s/api/inngest?msg=hi", server.URL),
			nil,
		)
		r.NoError(err)
		_, err = http.DefaultClient.Do(req)
		r.NoError(err)
		r.EventuallyWithT(func(t *assert.CollectT) {
			a := assert.New(t)
			a.NotNil(body)
		}, 5*time.Second, 10*time.Millisecond)

		r.Equal("http://foo.bar.baz/qux/quux?msg=hi", body.URL)
	})

	t.Run("fields", func(t *testing.T) {
		r := require.New(t)

		var body *sdk.RegisterRequest
		mockInngestServer := httptest.NewServer(http.HandlerFunc(func(
			w http.ResponseWriter,
			r *http.Request,
		) {
			byt, _ := io.ReadAll(r.Body)
			_ = json.Unmarshal(byt, &body)
			w.WriteHeader(http.StatusOK)
		}))
		defer mockInngestServer.Close()

		appName := randomSuffix("app")
		ic, err := inngestgo.NewClient(inngestgo.ClientOpts{
			APIBaseURL: inngestgo.StrPtr(mockInngestServer.URL),
			AppID:      appName,
			Dev:        inngestgo.BoolPtr(true),
		})
		r.NoError(err)
		server := httptest.NewServer(ic.ServeWithOpts(inngestgo.ServeOpts{
			Origin: inngestgo.StrPtr("foo.bar.baz"),
			Path:   inngestgo.StrPtr("/qux/quux"),
		}))
		defer server.Close()

		req, err := http.NewRequest(
			"PUT",
			fmt.Sprintf("%s/api/inngest?msg=hi", server.URL),
			nil,
		)
		r.NoError(err)
		_, err = http.DefaultClient.Do(req)
		r.NoError(err)
		r.EventuallyWithT(func(t *assert.CollectT) {
			a := assert.New(t)
			a.NotNil(body)
		}, 5*time.Second, 10*time.Millisecond)

		r.Equal("http://foo.bar.baz/qux/quux?msg=hi", body.URL)
	})
}
