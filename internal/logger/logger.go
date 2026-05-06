package logger

import (
	"io"
	"log/slog"
	"os"
	"strconv"
	"strings"
)

const (
	// LevelTrace matches the common slog convention of placing trace below debug.
	LevelTrace slog.Level = -8
)

// Default returns the SDK's default slog logger, configured from environment
// variables.
func Default() *slog.Logger {
	return New(os.Stderr)
}

// New returns a slog logger configured from environment variables and writing
// to w. It is exported for tests and internal packages that need custom writers.
func New(w io.Writer) *slog.Logger {
	opts := &slog.HandlerOptions{
		Level: parseLevel(os.Getenv("LOG_LEVEL")),
	}

	switch strings.ToLower(strings.TrimSpace(os.Getenv("LOG_HANDLER"))) {
	case "json":
		return slog.New(slog.NewJSONHandler(w, opts))
	case "txt", "text", "":
		return slog.New(slog.NewTextHandler(w, opts))
	default:
		return slog.New(slog.NewTextHandler(w, opts))
	}
}

func parseLevel(value string) slog.Level {
	switch strings.ToLower(strings.TrimSpace(value)) {
	case "trace":
		return LevelTrace
	case "debug":
		return slog.LevelDebug
	case "info", "":
		return slog.LevelInfo
	case "warn", "warning":
		return slog.LevelWarn
	case "error":
		return slog.LevelError
	default:
		if n, err := strconv.Atoi(value); err == nil {
			return slog.Level(n)
		}
		return slog.LevelInfo
	}
}
