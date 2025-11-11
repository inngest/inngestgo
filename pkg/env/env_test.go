package env

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestAPIServerURL(t *testing.T) {
	t.Run("returns production URL when INNGEST_DEV is unset", func(t *testing.T) {
		t.Setenv("INNGEST_DEV", "")
		assert.Equal(t, "https://api.inngest.com", APIServerURL())
	})

	t.Run("returns DevServerOrigin when INNGEST_DEV=1", func(t *testing.T) {
		t.Setenv("INNGEST_DEV", "1")
		assert.Equal(t, DevServerOrigin, APIServerURL())
	})

	t.Run("returns DevServerOrigin when INNGEST_DEV is non-URL value", func(t *testing.T) {
		t.Setenv("INNGEST_DEV", "true")
		assert.Equal(t, DevServerOrigin, APIServerURL())
	})

	t.Run("returns specified URL when INNGEST_DEV is a valid URL", func(t *testing.T) {
		customURL := "http://192.168.1.254:8288"
		t.Setenv("INNGEST_DEV", customURL)
		assert.Equal(t, customURL, APIServerURL())
	})

	t.Run("returns specified URL when INNGEST_DEV is a valid HTTPS URL", func(t *testing.T) {
		customURL := "https://dev.example.com:9000"
		t.Setenv("INNGEST_DEV", customURL)
		assert.Equal(t, customURL, APIServerURL())
	})
}
