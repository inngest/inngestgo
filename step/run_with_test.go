package step

import (
	"context"
	"encoding/json"
	"errors"
	"testing"

	"github.com/inngest/inngestgo/internal/sdkrequest"
	"github.com/stretchr/testify/require"
)

type runWithStore struct {
	calls int
	ctx   context.Context
	err   error
}

type runWithResult struct {
	ID string `json:"id"`
}

func (s *runWithStore) Get(ctx context.Context, id string) (runWithResult, error) {
	s.calls++
	s.ctx = ctx
	return runWithResult{
		ID: id,
	}, s.err
}

func TestRunWithReplay(t *testing.T) {
	store := &runWithStore{}
	manager := sdkrequest.NewManager(sdkrequest.Opts{
		Mode: sdkrequest.StepModeManual,
	})
	t.Cleanup(manager.CloseCheckpointer)
	ctx := sdkrequest.SetManager(context.Background(), manager)
	result, err := RunWith(ctx, "load build", store.Get, "build-id")
	require.NoError(t, err)
	require.Equal(t, "build-id", result.ID)
	require.Equal(t, 1, store.calls)
	require.True(t, IsWithinStep(store.ctx))
	require.Len(t, manager.Ops(), 1)
	op := manager.Ops()[0]
	require.Equal(t, "load build", op.Name)
	data, err := json.Marshal(map[string]json.RawMessage{
		"data": op.Data,
	})
	require.NoError(t, err)
	replay := sdkrequest.NewManager(sdkrequest.Opts{
		Mode: sdkrequest.StepModeManual,
		Request: &sdkrequest.Request{
			Steps: map[string]json.RawMessage{
				op.ID: data,
			},
		},
	})

	t.Cleanup(replay.CloseCheckpointer)
	ctx = sdkrequest.SetManager(context.Background(), replay)
	cached, err := RunWith(ctx, "load build", store.Get, "different-id")
	require.NoError(t, err)
	require.Equal(t, result, cached)
	require.Equal(t, 1, store.calls)
	require.Empty(t, replay.Ops())
}

func TestRunWithDirect(t *testing.T) {
	for _, expected := range []error{nil, errors.New("load failed")} {
		store := &runWithStore{
			err: expected,
		}
		ctx, cancel := context.WithCancel(context.Background())
		result, err := RunWith(ctx, "load build", store.Get, "build-id")
		cancel()
		require.Equal(t, expected, err)
		require.Equal(t, "build-id", result.ID)
		require.Equal(t, ctx, store.ctx)
		require.ErrorIs(t, store.ctx.Err(), context.Canceled)
	}
}
