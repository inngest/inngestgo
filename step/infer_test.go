package step

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/inngest/inngest/pkg/enums"
	"github.com/inngest/inngestgo/internal/middleware"
	"github.com/inngest/inngestgo/internal/sdkrequest"
	openai "github.com/sashabaranov/go-openai"
	"github.com/stretchr/testify/require"
)

type inferTestResponse struct {
	Text  string `json:"text"`
	Count int    `json:"count"`
}

func TestInferTypes(t *testing.T) {
	t.Run("It handles OpenAI requests using a 3rd party provider", func(t *testing.T) {
		ctx, cancel := context.WithCancel(context.Background())
		req := &sdkrequest.Request{
			Steps: map[string]json.RawMessage{
				"7d3bbb5cbbc497d78ad547d8d39cbea2b3b8b69e": []byte("{}"),
			},
		}

		mw := middleware.New()
		mgr := sdkrequest.NewManager(sdkrequest.Opts{
			Middleware: mw,
			Cancel:     cancel,
			Request:    req,
			Mode:       sdkrequest.StepModeYield,
		})
		ctx = sdkrequest.SetManager(ctx, mgr)

		resp, err := Infer[openai.ChatCompletionRequest, openai.ChatCompletionResponse](
			ctx,
			"openai",
			InferOpts[openai.ChatCompletionRequest]{
				Opts: InferRequestOpts{
					URL:     "https://api.openai.com/v1/chat/completions",
					AuthKey: "foo",
					Format:  InferFormatOpenAIChat,
				},
				Body: openai.ChatCompletionRequest{
					Model: "gpt-4o",
					Messages: []openai.ChatCompletionMessage{
						{Role: "system", Content: "Write a story in 20 words or less"},
					},
				},
			},
		)
		if err != nil {
			panic(err.Error())
		}
		// Resp is fully typed.
		_ = resp
	})

	t.Run("unmarshals replayed responses into struct values", func(t *testing.T) {
		expected := inferTestResponse{Text: "hello", Count: 2}
		ctx := newInferReplayContext(t, "value-output", expected)

		resp, err := Infer[map[string]any, inferTestResponse](
			ctx,
			"value-output",
			InferOpts[map[string]any]{
				Opts: InferRequestOpts{
					URL:     "https://example.com/infer",
					AuthKey: "foo",
					Format:  InferFormatOpenAIChat,
				},
				Body: map[string]any{"prompt": "hello"},
			},
		)

		require.NoError(t, err)
		require.Equal(t, expected, resp)
	})

	t.Run("unmarshals replayed responses into struct pointers", func(t *testing.T) {
		expected := inferTestResponse{Text: "hello", Count: 2}
		ctx := newInferReplayContext(t, "pointer-output", expected)

		resp, err := Infer[map[string]any, *inferTestResponse](
			ctx,
			"pointer-output",
			InferOpts[map[string]any]{
				Opts: InferRequestOpts{
					URL:     "https://example.com/infer",
					AuthKey: "foo",
					Format:  InferFormatOpenAIChat,
				},
				Body: map[string]any{"prompt": "hello"},
			},
		)

		require.NoError(t, err)
		require.Equal(t, &expected, resp)
	})
}

func newInferReplayContext(t *testing.T, stepID string, data any) context.Context {
	t.Helper()

	wrapped, err := json.Marshal(map[string]any{"data": data})
	require.NoError(t, err)

	op := sdkrequest.UnhashedOp{
		Op: enums.OpcodeAIGateway,
		ID: stepID,
	}
	req := &sdkrequest.Request{
		Steps: map[string]json.RawMessage{
			op.MustHash(): wrapped,
		},
	}

	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)

	mw := middleware.New()
	mgr := sdkrequest.NewManager(sdkrequest.Opts{
		Middleware: mw,
		Cancel:     cancel,
		Request:    req,
		Mode:       sdkrequest.StepModeYield,
	})

	return sdkrequest.SetManager(ctx, mgr)
}
