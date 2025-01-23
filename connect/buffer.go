package connect

import (
	"context"
	"fmt"
	"github.com/coder/websocket"
	"github.com/inngest/inngest/pkg/connect/wsproto"
	connectproto "github.com/inngest/inngest/proto/gen/connect/v1"
	"log/slog"
	"sync"
	"time"
)

type messageBuffer struct {
	buffered   []*connectproto.ConnectMessage
	pendingAck map[string]*connectproto.ConnectMessage
	lock       sync.Mutex
	logger     *slog.Logger
}

func newMessageBuffer(logger *slog.Logger) *messageBuffer {
	return &messageBuffer{
		logger:     logger,
		buffered:   make([]*connectproto.ConnectMessage, 0),
		pendingAck: make(map[string]*connectproto.ConnectMessage),
		lock:       sync.Mutex{},
	}
}

func (m *messageBuffer) flush(ws *websocket.Conn) error {
	m.lock.Lock()
	defer m.lock.Unlock()

	if ws == nil {
		return fmt.Errorf("requires WebSocket connection")
	}

	attempt := 0
	for {
		if attempt == 5 {
			return fmt.Errorf("could not send %d buffered messages", len(m.buffered))
		}

		next := make([]*connectproto.ConnectMessage, 0)
		for _, msg := range m.buffered {
			// always send the message, even if the context is cancelled
			err := wsproto.Write(context.Background(), ws, msg)
			if err != nil {
				m.buffered = append(m.buffered, msg)

				break
			}

			m.logger.Debug("sent buffered message", "msg", msg)
		}
		m.buffered = next

		if len(next) == 0 {
			break
		}

		attempt++
	}

	return nil
}

func (m *messageBuffer) hasMessages() bool {
	m.lock.Lock()
	defer m.lock.Unlock()

	return len(m.buffered) > 0
}

func (m *messageBuffer) append(id string, msg *connectproto.ConnectMessage) {
	m.lock.Lock()
	defer m.lock.Unlock()

	m.buffered = append(m.buffered, msg)

	// In case message was still marked as pending, remove it
	delete(m.pendingAck, id)
}

func (m *messageBuffer) addPending(ctx context.Context, id string, msg *connectproto.ConnectMessage, timeout time.Duration) {
	m.lock.Lock()
	defer m.lock.Unlock()

	m.pendingAck[id] = msg

	go func() {
		for {
			select {
			case <-ctx.Done():
				break
			case <-time.After(timeout):
				break
			}

			m.lock.Lock()
			// If message is still in outgoing messages, it wasn't acknowledged. Add to buffer.
			if _, ok := m.pendingAck[id]; ok {
				m.buffered = append(m.buffered, msg)
				delete(m.pendingAck, id)
			}
			m.lock.Unlock()
		}
	}()
}

func (m *messageBuffer) acknowledge(id string) {
	m.lock.Lock()
	defer m.lock.Unlock()

	delete(m.pendingAck, id)
}
