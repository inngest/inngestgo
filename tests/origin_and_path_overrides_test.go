package tests

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"testing"
	"time"

	"github.com/inngest/inngestgo"
	"github.com/inngest/inngestgo/internal/types"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestOriginAndPathOverrides(t *testing.T) {
	// The serve origin and path can be overridden.

	t.Run("env vars", func(t *testing.T) {
		r := require.New(t)
		t.Setenv("INNGEST_SERVE_HOST", "foo.bar.baz")
		t.Setenv("INNGEST_SERVE_PATH", "/qux/quux")

		var body *types.RegisterRequest
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
		fn, err := inngestgo.CreateFunction(
			ic,
			inngestgo.FunctionOpts{ID: "my-fn"},
			inngestgo.EventTrigger("event", nil),
			func(ctx context.Context, input inngestgo.Input[any]) (any, error) {
				return nil, nil
			},
		)
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

		expectedURL, err := url.Parse("http://foo.bar.baz/qux/quux?msg=hi")
		r.NoError(err)
		r.Equal(expectedURL.String(), body.URL)

		r.Len(body.Functions, 1)
		for _, f := range body.Functions {
			r.Len(f.Steps, 1)
			for _, step := range f.Steps {
				parsedURL, err := url.Parse(step.Runtime["url"].(string))
				r.NoError(err)
				r.Equal(expectedURL.Scheme, parsedURL.Scheme)
				r.Equal(expectedURL.Host, parsedURL.Host)
				r.Equal(expectedURL.Path, parsedURL.Path)

				qp := parsedURL.Query()
				r.Equal("hi", qp.Get("msg"))
				r.Equal("step", qp.Get("step"))
				r.Equal(fn.FullyQualifiedID(), qp.Get("fnId"))
			}
		}
	})

	t.Run("fields", func(t *testing.T) {
		r := require.New(t)

		var body *types.RegisterRequest
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
