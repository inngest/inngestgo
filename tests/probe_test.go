package tests

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/inngest/inngestgo"
	"github.com/stretchr/testify/require"
)

const sKey = "signkey-prod-000000"

func TestTrustProbe(t *testing.T) {
	devEnv(t)

	t.Run("dev mode", func(t *testing.T) {
		isDev := true

		t.Run("valid signature", func(t *testing.T) {
			r := require.New(t)
			ctx := context.Background()

			appName := randomSuffix("TestTrustProbe")
			c, err := inngestgo.NewClient(inngestgo.ClientOpts{
				AppID:      appName,
				Dev:        inngestgo.BoolPtr(isDev),
				SigningKey: inngestgo.StrPtr(sKey),
			})
			r.NoError(err)
			server := createApp(t, c)
			defer server.Close()

			appURL := fmt.Sprintf("%s?probe=trust", server.URL)
			req, err := http.NewRequest("POST", appURL, nil)
			r.NoError(err)

			reqSig, err := inngestgo.Sign(ctx, time.Now(), []byte(sKey), []byte{})
			r.NoError(err)
			req.Header.Add("x-inngest-signature", reqSig)

			resp, err := http.DefaultClient.Do(req)
			r.NoError(err)
			r.Equal(http.StatusOK, resp.StatusCode)
		})

		t.Run("no signature", func(t *testing.T) {
			r := require.New(t)

			appName := randomSuffix("TestTrustProbe")
			c, err := inngestgo.NewClient(inngestgo.ClientOpts{
				AppID:      appName,
				Dev:        inngestgo.BoolPtr(isDev),
				SigningKey: inngestgo.StrPtr(sKey),
			})
			r.NoError(err)
			server := createApp(t, c)
			defer server.Close()

			appURL := fmt.Sprintf("%s?probe=trust", server.URL)
			req, err := http.NewRequest("POST", appURL, nil)
			r.NoError(err)

			resp, err := http.DefaultClient.Do(req)
			r.NoError(err)
			r.Equal(http.StatusOK, resp.StatusCode)
		})

		t.Run("invalid signature", func(t *testing.T) {
			r := require.New(t)
			ctx := context.Background()

			appName := randomSuffix("TestTrustProbe")
			c, err := inngestgo.NewClient(inngestgo.ClientOpts{
				AppID:      appName,
				Dev:        inngestgo.BoolPtr(isDev),
				SigningKey: inngestgo.StrPtr(sKey),
			})
			r.NoError(err)
			server := createApp(t, c)
			defer server.Close()

			appURL := fmt.Sprintf("%s?probe=trust", server.URL)
			req, err := http.NewRequest("POST", appURL, nil)
			r.NoError(err)

			wrongSKey := []byte("deadbeef")
			reqSig, err := inngestgo.Sign(ctx, time.Now(), wrongSKey, []byte{})
			r.NoError(err)
			req.Header.Add("x-inngest-signature", reqSig)

			resp, err := http.DefaultClient.Do(req)
			r.NoError(err)
			r.Equal(http.StatusOK, resp.StatusCode)
		})
	})

	t.Run("cloud mode", func(t *testing.T) {
		isDev := false

		t.Run("valid signature", func(t *testing.T) {
			r := require.New(t)
			ctx := context.Background()

			appName := randomSuffix("TestTrustProbe")
			c, err := inngestgo.NewClient(inngestgo.ClientOpts{
				AppID:      appName,
				Dev:        inngestgo.BoolPtr(isDev),
				SigningKey: inngestgo.StrPtr(sKey),
			})
			r.NoError(err)
			server := createApp(t, c)
			defer server.Close()

			appURL := fmt.Sprintf("%s?probe=trust", server.URL)
			req, err := http.NewRequest("POST", appURL, nil)
			r.NoError(err)

			reqSig, err := inngestgo.Sign(ctx, time.Now(), []byte(sKey), []byte{})
			r.NoError(err)
			req.Header.Add("x-inngest-signature", reqSig)

			resp, err := http.DefaultClient.Do(req)
			r.NoError(err)

			r.Equal(http.StatusOK, resp.StatusCode)

			respSig := resp.Header.Get("x-inngest-signature")
			r.NotEmpty(respSig)
			defer resp.Body.Close()

			respBody, err := io.ReadAll(resp.Body)
			r.NoError(err)

			valid, err := inngestgo.ValidateResponseSignature(
				ctx,
				respSig,
				[]byte(sKey),
				respBody,
			)
			r.NoError(err)
			r.True(valid)
		})

		t.Run("no signature", func(t *testing.T) {
			r := require.New(t)

			appName := randomSuffix("TestTrustProbe")
			c, err := inngestgo.NewClient(inngestgo.ClientOpts{
				AppID:      appName,
				Dev:        inngestgo.BoolPtr(isDev),
				SigningKey: inngestgo.StrPtr(sKey),
			})
			r.NoError(err)
			server := createApp(t, c)
			defer server.Close()

			appURL := fmt.Sprintf("%s?probe=trust", server.URL)
			req, err := http.NewRequest("POST", appURL, nil)
			r.NoError(err)

			resp, err := http.DefaultClient.Do(req)
			r.NoError(err)
			r.Equal(http.StatusUnauthorized, resp.StatusCode)
		})

		t.Run("invalid signature", func(t *testing.T) {
			r := require.New(t)
			ctx := context.Background()

			appName := randomSuffix("TestTrustProbe")
			c, err := inngestgo.NewClient(inngestgo.ClientOpts{
				AppID:      appName,
				Dev:        inngestgo.BoolPtr(isDev),
				SigningKey: inngestgo.StrPtr(sKey),
			})
			r.NoError(err)
			server := createApp(t, c)
			defer server.Close()

			appURL := fmt.Sprintf("%s?probe=trust", server.URL)
			req, err := http.NewRequest("POST", appURL, nil)
			r.NoError(err)

			wrongSKey := []byte("deadbeef")
			reqSig, err := inngestgo.Sign(ctx, time.Now(), wrongSKey, []byte{})
			r.NoError(err)
			req.Header.Add("x-inngest-signature", reqSig)

			resp, err := http.DefaultClient.Do(req)
			r.NoError(err)
			r.Equal(http.StatusUnauthorized, resp.StatusCode)
		})
	})
}

func createApp(t *testing.T, c inngestgo.Client) *httptest.Server {
	r := require.New(t)
	_, err := inngestgo.CreateFunction(
		c,
		inngestgo.FunctionOpts{
			ID:   "my-fn",
			Name: "my-fn",
		},
		inngestgo.EventTrigger("my-event", nil),
		func(ctx context.Context, input inngestgo.Input[any]) (any, error) {
			return nil, nil
		},
	)
	r.NoError(err)
	server, _ := serve(t, c)
	return server
}
