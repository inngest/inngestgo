package logger

import (
	"bytes"
	"encoding/json"
	"log/slog"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestNewUsesLogLevel(t *testing.T) {
	t.Setenv("LOG_LEVEL", "debug")
	t.Setenv("LOG_HANDLER", "txt")

	var buf bytes.Buffer
	logger := New(&buf)
	logger.Debug("debug message")

	assert.Contains(t, buf.String(), "debug message")
}

func TestNewFiltersByLogLevel(t *testing.T) {
	t.Setenv("LOG_LEVEL", "error")
	t.Setenv("LOG_HANDLER", "txt")

	var buf bytes.Buffer
	logger := New(&buf)
	logger.Warn("warn message")
	logger.Error("error message")

	assert.NotContains(t, buf.String(), "warn message")
	assert.Contains(t, buf.String(), "error message")
}

func TestNewUsesJSONHandler(t *testing.T) {
	t.Setenv("LOG_LEVEL", "info")
	t.Setenv("LOG_HANDLER", "json")

	var buf bytes.Buffer
	logger := New(&buf)
	logger.Info("json message", "answer", 42)

	var entry map[string]any
	require.NoError(t, json.Unmarshal(buf.Bytes(), &entry))
	assert.Equal(t, "json message", entry[slog.MessageKey])
	assert.Equal(t, float64(42), entry["answer"])
}

func TestNewUsesTextHandler(t *testing.T) {
	t.Setenv("LOG_LEVEL", "info")
	t.Setenv("LOG_HANDLER", "txt")

	var buf bytes.Buffer
	logger := New(&buf)
	logger.Info("text message")

	assert.Contains(t, buf.String(), "msg=\"text message\"")
	assert.NotContains(t, buf.String(), "{\"")
}
