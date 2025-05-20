package tests

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/inngest/inngestgo"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func Test_INNGEST_BASE_URL(t *testing.T) {
	cases := []string{"cloud", "dev"}
	for _, c := range cases {
		t.Run(c, func(t *testing.T) {
			if c == "cloud" {
				t.Setenv("INNGEST_SIGNING_KEY", "deadbeef")
			} else {
				t.Setenv("INNGEST_DEV", "1")
			}

			r := require.New(t)
			ctx := context.Background()

			// Mock Inngest server to ensure the SDK sends requests to the
			// correct URL.
			counter := 0
			mockInngestServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				counter++
				w.WriteHeader(http.StatusOK)
			}))
			defer mockInngestServer.Close()

			t.Setenv("INNGEST_BASE_URL", mockInngestServer.URL)
			appName := randomSuffix("app")
			ic, err := inngestgo.NewClient(inngestgo.ClientOpts{AppID: appName})
			r.NoError(err)
			server := createApp(t, ic)
			defer server.Close()

			// Sync to INNGEST_BASE_URL.
			req, err := http.NewRequest("PUT", fmt.Sprintf("%s/api/inngest", server.URL), nil)
			r.NoError(err)
			_, err = http.DefaultClient.Do(req)
			r.NoError(err)
			r.EventuallyWithT(func(t *assert.CollectT) {
				a := assert.New(t)
				a.Equal(1, counter)
			}, 5*time.Second, 10*time.Millisecond)

			// Send events to INNGEST_BASE_URL.
			_, err = ic.Send(ctx, inngestgo.Event{Name: "event"})
			r.NoError(err)
			r.EventuallyWithT(func(t *assert.CollectT) {
				a := assert.New(t)
				a.Equal(2, counter)
			}, 5*time.Second, 10*time.Millisecond)
		})
	}
}
