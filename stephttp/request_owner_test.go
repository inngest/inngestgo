package stephttp

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
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
	// inputs receives the run data of each call when it is not nil.
	inputs chan NewAPIRunData
}

func (b *blockingAPI) CheckpointNewRun(ctx context.Context, runID ulid.ULID, input NewAPIRunData, steps ...sdkrequest.GeneratorOpcode) (*CheckpointRun, error) {
	if b.inputs != nil {
		b.inputs <- input
	}
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

func TestNewRunStoresRequestBody(t *testing.T) {
	const body = `{"hello":"world"}`

	tests := []struct {
		name string
		// read is how many bytes the handler reads.  -1 reads the whole body.
		read     int
		omit     bool
		expected string
	}{
		{name: "handler reads the whole body", read: -1, expected: body},
		{name: "handler reads no body", read: 0, expected: body},
		{name: "handler reads part of the body", read: 5, expected: body},
		{name: "config omits the body", read: -1, omit: true, expected: ""},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			api := &blockingAPI{
				called:  make(chan []sdkrequest.GeneratorOpcode, 1),
				release: make(chan struct{}),
				inputs:  make(chan NewAPIRunData, 1),
			}
			close(api.release)

			p := Setup(SetupOpts{
				Optional: OptionalSetupOpts{TrackAllEndpoints: true},
			})
			p.api = api

			var handlerRead string
			handler := p.ServeHTTP(func(w http.ResponseWriter, r *http.Request) {
				Configure(r.Context(), FnOpts{OmitRequestBody: tt.omit})

				var byt []byte
				switch tt.read {
				case -1:
					byt, _ = io.ReadAll(r.Body)
				case 0:
				default:
					byt = make([]byte, tt.read)
					_, _ = io.ReadFull(r.Body, byt)
				}
				_ = r.Body.Close()
				handlerRead = string(byt)
				_, _ = w.Write([]byte("ok"))
			})

			req := httptest.NewRequest(http.MethodPost, "/test", strings.NewReader(body))
			handler(httptest.NewRecorder(), req)

			switch tt.read {
			case -1:
				require.Equal(t, body, handlerRead)
			default:
				require.Equal(t, body[:tt.read], handlerRead)
			}

			select {
			case input := <-api.inputs:
				require.Equal(t, tt.expected, string(input.Body))
			case <-time.After(5 * time.Second):
				t.Fatal("checkpoint was not sent")
			}
		})
	}
}
