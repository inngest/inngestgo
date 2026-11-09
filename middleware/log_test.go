package middleware_test

import (
	"bytes"
	"context"
	"encoding/json"
	"log/slog"
	"sync"
	"testing"

	"github.com/inngest/inngestgo/middleware"
	"github.com/stretchr/testify/require"
)

func TestLogMiddlewareDerivedLoggers(t *testing.T) {
	for _, tt := range []struct {
		name   string
		derive func(*slog.Logger) *slog.Logger
		fields map[string]any
	}{
		{
			name:   "parent",
			derive: func(l *slog.Logger) *slog.Logger { return l },
			fields: map[string]any{"status": "ready"},
		},
		{
			name:   "attributes",
			derive: func(l *slog.Logger) *slog.Logger { return l.With("order_id", "order-42") },
			fields: map[string]any{"order_id": "order-42", "status": "ready"},
		},
		{
			name:   "group",
			derive: func(l *slog.Logger) *slog.Logger { return l.WithGroup("order") },
			fields: map[string]any{"order": map[string]any{"status": "ready"}},
		},
		{
			name: "nested",
			derive: func(l *slog.Logger) *slog.Logger {
				return l.With("order_id", "order-42").WithGroup("order").With("item", "book").WithGroup("details")
			},
			fields: map[string]any{
				"order_id": "order-42",
				"order":    map[string]any{"item": "book", "details": map[string]any{"status": "ready"}},
			},
		},
	} {
		t.Run(tt.name, func(t *testing.T) {
			var output bytes.Buffer
			newMiddleware := middleware.LogMiddleware(slog.New(slog.NewJSONHandler(&output, &slog.HandlerOptions{Level: slog.LevelInfo})))
			ctx := context.Background()
			input := &middleware.TransformableInput{}
			input.WithContext(ctx)
			m := newMiddleware()
			m.TransformInput(ctx, middleware.CallContext{}, input)
			parent := middleware.LoggerFromContext(input.Context())
			child := tt.derive(parent)
			sibling := parent.With("sibling", true)

			parent.Info("replay")
			child.Info("replay", "status", "ready")
			sibling.Info("replay")
			require.Empty(t, output.String(), "derived loggers must remain silent during replay")

			m.BeforeExecution(ctx, middleware.CallContext{})
			child.Debug("filtered")
			child.Info("enabled", "status", "ready")
			sibling.Info("sibling")
			parent.Info("parent")

			// A new request must not inherit the previous request's enabled gate.
			fresh := newMiddleware()
			fresh.TransformInput(ctx, middleware.CallContext{}, input)
			tt.derive(middleware.LoggerFromContext(input.Context())).Info("replay", "status", "ready")

			var wg sync.WaitGroup
			for range 8 {
				wg.Go(func() {
					m.BeforeExecution(ctx, middleware.CallContext{})
					child.Info("concurrent", "status", "ready")
				})
			}
			wg.Wait()

			decoder := json.NewDecoder(&output)
			for i := range 11 {
				var record map[string]any
				require.NoError(t, decoder.Decode(&record))
				delete(record, "time")
				want := map[string]any{"level": "INFO", "msg": "concurrent"}
				switch i {
				case 0:
					want["msg"] = "enabled"
				case 1:
					want["msg"] = "sibling"
					want["sibling"] = true
				case 2:
					want["msg"] = "parent"
				}
				if i == 0 || i >= 3 {
					for key, value := range tt.fields {
						want[key] = value
					}
				}
				require.Equal(t, want, record)
			}
			require.False(t, decoder.More(), "filtered and fresh-request logs must not be written")
		})
	}
}
