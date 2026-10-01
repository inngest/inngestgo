package stephttp

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/inngest/inngest/pkg/enums"
	"github.com/inngest/inngestgo/internal/sdkrequest"
	"github.com/inngest/inngestgo/step"
	"github.com/oklog/ulid/v2"
	"github.com/stretchr/testify/require"
)

// blockingAPI holds every CheckpointNewRun call until release is closed.
type blockingAPI struct {
	called  chan []sdkrequest.GeneratorOpcode
	release chan struct{}
}

func (b *blockingAPI) CheckpointNewRun(ctx context.Context, runID ulid.ULID, input NewAPIRunData, steps ...sdkrequest.GeneratorOpcode) (*CheckpointRun, error) {
	b.called <- steps
	<-b.release
	return &CheckpointRun{RunID: runID}, nil
}

func (b *blockingAPI) CheckpointSteps(ctx context.Context, run CheckpointRun, steps []sdkrequest.GeneratorOpcode) error {
	return nil
}

func (b *blockingAPI) CheckpointResponse(ctx context.Context, run CheckpointRun, result APIResult) error {
	return nil
}

func (b *blockingAPI) GetSteps(ctx context.Context, runID ulid.ULID) (map[string]json.RawMessage, error) {
	return nil, nil
}

func TestFinishedRunCheckpointsInBackground(t *testing.T) {
	api := &blockingAPI{
		called:  make(chan []sdkrequest.GeneratorOpcode, 1),
		release: make(chan struct{}),
	}

	p := Setup(SetupOpts{
		Domain:   "test.example.com",
		Optional: OptionalSetupOpts{TrackAllEndpoints: true},
	})
	p.api = api

	handler := p.ServeHTTP(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte("ok"))
	})

	rec := httptest.NewRecorder()
	served := make(chan struct{})
	go func() {
		handler(rec, httptest.NewRequest(http.MethodGet, "/test", nil))
		close(served)
	}()

	select {
	case <-served:
	case <-time.After(5 * time.Second):
		t.Fatal("handler waited for the checkpoint to finish")
	}
	require.Equal(t, "ok", rec.Body.String())

	var steps []sdkrequest.GeneratorOpcode
	select {
	case steps = <-api.called:
	case <-time.After(5 * time.Second):
		t.Fatal("checkpoint was not sent")
	}
	require.Len(t, steps, 1)
	require.Equal(t, enums.OpcodeRunComplete, steps[0].Op)

	done := p.Wait(t.Context())

	// Wait polls once a second, so 1.5 seconds covers at least one check.
	select {
	case <-done:
		t.Fatal("Wait returned while a checkpoint was in flight")
	case <-time.After(1500 * time.Millisecond):
	}

	close(api.release)

	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("Wait did not return after the checkpoint finished")
	}
}

func TestHandledStepErrorFinishesRun(t *testing.T) {
	tests := []struct {
		name          string
		asyncResponse AsyncResponse
	}{
		{name: "token response", asyncResponse: AsyncResponseToken{}},
		{name: "redirect response", asyncResponse: AsyncResponseRedirect{}},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			api := &blockingAPI{
				called:  make(chan []sdkrequest.GeneratorOpcode, 1),
				release: make(chan struct{}),
			}
			close(api.release)

			p := Setup(SetupOpts{
				Optional: OptionalSetupOpts{DefaultAsyncResponse: tt.asyncResponse},
			})
			p.api = api

			handler := p.ServeHTTP(func(w http.ResponseWriter, r *http.Request) {
				_, err := step.Run(r.Context(), "a", func(ctx context.Context) (int, error) {
					return 0, fmt.Errorf("boom")
				})
				if err != nil {
					http.Error(w, "handled", http.StatusUnauthorized)
				}
			})

			rec := httptest.NewRecorder()
			handler(rec, httptest.NewRequest(http.MethodGet, "/test", nil))

			require.Equal(t, http.StatusUnauthorized, rec.Code)
			require.Equal(t, "handled\n", rec.Body.String())
			require.Empty(t, rec.Header().Get("Location"))

			var steps []sdkrequest.GeneratorOpcode
			select {
			case steps = <-api.called:
			case <-time.After(5 * time.Second):
				t.Fatal("checkpoint was not sent")
			}
			require.Len(t, steps, 2)
			require.Equal(t, enums.OpcodeStepFailed, steps[0].Op)
			require.Equal(t, enums.OpcodeRunComplete, steps[1].Op)
		})
	}
}
