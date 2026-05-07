package inngestgo

import (
	"context"
	"log/slog"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestGetEventKey(t *testing.T) {
	t.Run("env var", func(t *testing.T) {
		c := apiClient{}
		t.Setenv("INNGEST_EVENT_KEY", "env-var")
		assert.Equal(t, "env-var", c.GetEventKey())
	})

	t.Run("field", func(t *testing.T) {
		c := apiClient{
			ClientOpts: ClientOpts{
				EventKey: StrPtr("field"),
			},
		}
		assert.Equal(t, "field", c.GetEventKey())
	})

	t.Run("field overrides env var", func(t *testing.T) {
		t.Setenv("INNGEST_EVENT_KEY", "env-var")
		c := apiClient{
			ClientOpts: ClientOpts{EventKey: StrPtr("field")},
		}
		assert.Equal(t, "field", c.GetEventKey())
	})

	t.Run("no event key in Cloud mode", func(t *testing.T) {
		// t.Setenv("INNGEST_EVENT_KEY", "")
		c := apiClient{}
		assert.Equal(t, "", c.GetEventKey())
	})

	t.Run("no event key in Dev mode", func(t *testing.T) {
		t.Setenv("INNGEST_DEV", "1")
		c := apiClient{}
		assert.Equal(t, "NO_EVENT_KEY_SET", c.GetEventKey())
	})
}

func TestNewClientUsesEnvConfiguredDefaultLogger(t *testing.T) {
	t.Setenv("LOG_LEVEL", "debug")
	t.Setenv("LOG_HANDLER", "json")

	c, err := NewClient(ClientOpts{AppID: "test"})
	require.NoError(t, err)

	opts := c.Options()
	assert.True(t, opts.Logger.Enabled(context.Background(), slog.LevelDebug))
	_, ok := opts.Logger.Handler().(*slog.JSONHandler)
	assert.True(t, ok)
}

func TestNewClientUsesTextLoggerHandler(t *testing.T) {
	t.Setenv("LOG_HANDLER", "txt")

	c, err := NewClient(ClientOpts{AppID: "test"})
	require.NoError(t, err)

	opts := c.Options()
	_, ok := opts.Logger.Handler().(*slog.TextHandler)
	assert.True(t, ok)
}

func TestNewClientKeepsProvidedLogger(t *testing.T) {
	t.Setenv("LOG_LEVEL", "debug")
	t.Setenv("LOG_HANDLER", "json")

	provided := slog.New(slog.DiscardHandler)
	c, err := NewClient(ClientOpts{
		AppID:  "test",
		Logger: provided,
	})
	require.NoError(t, err)

	assert.Same(t, provided, c.Options().Logger)
}
