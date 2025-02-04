package inngestgo

import (
	"testing"

	"github.com/stretchr/testify/assert"
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
