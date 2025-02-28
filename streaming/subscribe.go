package streaming

import (
	"context"
	"net/http"

	"github.com/coder/websocket"
)

var url string

func do() {
	stream, err := Subscribe(context.Background(), "")
	if err != nil {
		return
	}

	for item := range stream {
		switch item.Kind() {
		case StreamMessage:
		case StreamChunk:
		case StreamError:
			break
		}
	}
}

// Subscribe subscribes to a given set of channels and topics as granted by
// the current token.
func Subscribe(ctx context.Context, token string) (chan StreamItem, error) {
	// TODO: URL from client
	c, _, err := websocket.Dial(ctx, url, &websocket.DialOptions{
		HTTPHeader: http.Header{
			"Authorization": []string{"Bearer " + token},
		},
	})
	if err != nil {
		return nil, err
	}

	sender := make(chan StreamItem)

	go func() {
		for {
			if ctx.Err() != nil {
				close(sender)
				return
			}

			_, resp, err := c.Read(ctx)
			if isWebsocketClosed(err) {
				close(sender)
				return
			}
			if err != nil {
				_ = c.CloseNow()
				sender <- StreamItem{err: err}
				close(sender)
				return
			}

			// XXX: Check to see if this is a message or a stream.  The only messages
			// sent via our protocol are either JSON objects or streamed data.
			//
			// Therefore, we check the first character of the message to check the type.
			if len(resp) == 0 {
				continue
			}
			if resp[0] == '{' {
				// Assume this is a fully defined JSON message.
				//
				// TODO: Import message type, parse.
				sender <- StreamItem{message: &Message{}}
				continue
			}

			// TODO: Parse Chunk
			sender <- StreamItem{chunk: &Chunk{}}
		}
	}()

	return sender, nil
}

// Chunk represents a chunk of a stream.
type Chunk struct {
	StreamID string
	Data     string
}

// TODO: Import types from OSS
type Message struct{}

type StreamKind string

const (
	StreamMessage = StreamKind("message")
	StreamChunk   = StreamKind("chunk")
	StreamError   = StreamKind("error")
)

type StreamItem struct {
	message *Message
	chunk   *Chunk
	err     error
}

func (r StreamItem) Kind() StreamKind {
	if r.IsChunk() {
		return StreamChunk
	}
	if r.IsMessage() {
		return StreamMessage
	}
	return StreamError
}

func (r StreamItem) IsMessage() bool {
	return r.message != nil
}

func (r StreamItem) Message() Message {
	return *r.message
}

func (r StreamItem) IsChunk() bool {
	return r.chunk != nil
}

func (r StreamItem) Chunk() Chunk {
	return *r.chunk
}

func (r StreamItem) IsErr() bool {
	return r.err != nil
}

func (r StreamItem) Err() error {
	return r.err
}

func isWebsocketClosed(err error) bool {
	if err == nil {
		return false
	}
	if websocket.CloseStatus(err) != -1 {
		return true
	}
	if err.Error() == "failed to get reader: use of closed network connection" {
		return true
	}
	return false
}
