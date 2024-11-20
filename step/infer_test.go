package step

import (
	"context"
	"testing"

	openai "github.com/sashabaranov/go-openai"
)

func TestInferTypes(t *testing.T) {
	ctx := context.Background()

	t.Run("It handles OpenAI requests using a 3rd party provider", func(t *testing.T) {
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
