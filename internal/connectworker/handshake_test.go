package connectworker

import (
	"context"
	"errors"
	"log/slog"
	"testing"
	"time"

	"github.com/inngest/inngest/pkg/connect/wsproto"
	connectproto "github.com/inngest/inngest/proto/gen/connect/v1"
	"github.com/stretchr/testify/require"
	"google.golang.org/protobuf/proto"
)

func TestPerformConnectHandshakePrepareConnect(t *testing.T) {
	tests := []struct {
		name           string
		prepare        func(req *connectproto.WorkerConnectRequestData) error
		wantFunctions  string
		wantCapability string
		wantErr        string
	}{
		{
			name:           "no hook sends the message as built",
			wantFunctions:  `[{"id":"app-fn"}]`,
			wantCapability: `{"connect":"v1"}`,
		},
		{
			name: "hook changes the apps and capabilities",
			prepare: func(req *connectproto.WorkerConnectRequestData) error {
				req.Apps[0].Functions = []byte(`[{"id":"app-fn","changed":true}]`)
				req.Capabilities = []byte(`{"connect":"v1","extra":"v1"}`)
				return nil
			},
			wantFunctions:  `[{"id":"app-fn","changed":true}]`,
			wantCapability: `{"connect":"v1","extra":"v1"}`,
		},
		{
			name: "hook error fails the attempt",
			prepare: func(*connectproto.WorkerConnectRequestData) error {
				return errors.New("no launch token")
			},
			wantErr: "could not prepare connect message: no launch token",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			r := require.New(t)
			clientConn, serverConn := newTestWebsocketPair(t)

			// the fake gateway sends hello, reads WORKER_CONNECT, and answers
			// ready.
			connectSeen := make(chan *connectproto.WorkerConnectRequestData, 1)
			go func() {
				ctx := context.Background()
				_ = wsproto.Write(ctx, serverConn, &connectproto.ConnectMessage{
					Kind: connectproto.GatewayMessageType_GATEWAY_HELLO,
				})
				var msg connectproto.ConnectMessage
				if wsproto.Read(ctx, serverConn, &msg) != nil {
					return
				}
				var req connectproto.WorkerConnectRequestData
				if proto.Unmarshal(msg.Payload, &req) != nil {
					return
				}
				connectSeen <- &req
				ready, _ := proto.Marshal(&connectproto.GatewayConnectionReadyData{
					HeartbeatInterval:   "10s",
					ExtendLeaseInterval: "5s",
				})
				_ = wsproto.Write(ctx, serverConn, &connectproto.ConnectMessage{
					Kind:    connectproto.GatewayMessageType_GATEWAY_CONNECTION_READY,
					Payload: ready,
				})
			}()

			h := &connectHandler{
				opts:   Opts{PrepareConnect: tt.prepare},
				logger: slog.New(slog.DiscardHandler),
			}
			_, err := h.performConnectHandshake(context.Background(), "connection-id", clientConn, &connectproto.StartResponse{
				ConnectionId: "connection-id",
			}, connectionEstablishData{
				marshaledCapabilities: []byte(`{"connect":"v1"}`),
				apps: []*connectproto.AppConfiguration{{
					AppName:   "app",
					Functions: []byte(`[{"id":"app-fn"}]`),
				}},
			}, time.Now())

			if tt.wantErr != "" {
				r.ErrorContains(err, tt.wantErr)
				r.True(shouldReconnect(err))
				select {
				case <-connectSeen:
					t.Fatal("gateway received WORKER_CONNECT after the hook failed")
				case <-time.After(50 * time.Millisecond):
				}
				return
			}

			r.NoError(err)
			req := <-connectSeen
			r.JSONEq(tt.wantFunctions, string(req.Apps[0].Functions))
			r.JSONEq(tt.wantCapability, string(req.Capabilities))
		})
	}
}
