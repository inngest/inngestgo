package connect

import (
	"log/slog"
	"os"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestMaxWorkerConcurrency(t *testing.T) {
	t.Run("returns user provided value", func(t *testing.T) {
		r := require.New(t)

		maxConcurrency := int64(100)
		h := &connectHandler{
			opts: Opts{
				MaxWorkerConcurrency: &maxConcurrency,
			},
			logger: slog.Default(),
		}

		result := h.maxWorkerConcurrency()
		r.NotNil(result)
		r.Equal(int64(100), *result)
	})

	t.Run("returns environment variable value when user value not provided", func(t *testing.T) {
		r := require.New(t)

		// Set environment variable
		t.Setenv(maxWorkerConcurrencyEnvKey, "50")

		h := &connectHandler{
			opts: Opts{
				MaxWorkerConcurrency: nil,
			},
			logger: slog.Default(),
		}

		result := h.maxWorkerConcurrency()
		r.NotNil(result)
		r.Equal(int64(50), *result)
	})

	t.Run("returns default value when neither user value nor env var provided", func(t *testing.T) {
		r := require.New(t)

		// Ensure environment variable is not set
		_ = os.Unsetenv(maxWorkerConcurrencyEnvKey)

		h := &connectHandler{
			opts: Opts{
				MaxWorkerConcurrency: nil,
			},
			logger: slog.Default(),
		}

		result := h.maxWorkerConcurrency()
		r.NotNil(result)
		r.Equal(defaultMaxWorkerConcurrency, *result)
	})

	t.Run("user provided value takes precedence over environment variable", func(t *testing.T) {
		r := require.New(t)

		// Set environment variable
		t.Setenv(maxWorkerConcurrencyEnvKey, "50")

		maxConcurrency := int64(200)
		h := &connectHandler{
			opts: Opts{
				MaxWorkerConcurrency: &maxConcurrency,
			},
			logger: slog.Default(),
		}

		result := h.maxWorkerConcurrency()
		r.NotNil(result)
		r.Equal(int64(200), *result)
	})

	t.Run("handles invalid environment variable gracefully", func(t *testing.T) {
		r := require.New(t)

		// Set invalid environment variable
		t.Setenv(maxWorkerConcurrencyEnvKey, "invalid")

		h := &connectHandler{
			opts: Opts{
				MaxWorkerConcurrency: nil,
			},
			logger: slog.Default(),
		}

		result := h.maxWorkerConcurrency()
		r.NotNil(result)
		// Should return default value when env var is invalid
		r.Equal(defaultMaxWorkerConcurrency, *result)
	})
}
