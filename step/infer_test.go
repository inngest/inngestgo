package step

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/inngest/inngestgo/internal/sdkrequest"
	openai "github.com/sashabaranov/go-openai"
)

func TestInferTypes(t *testing.T) {
	t.Run("It handles OpenAI requests using a 3rd party provider", func(t *testing.T) {

		ctx, cancel := context.WithCancel(context.Background())
		req := &sdkrequest.Request{
			Steps: map[string]json.RawMessage{},
		}
		mgr := sdkrequest.NewManager(cancel, req, "")
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
}
