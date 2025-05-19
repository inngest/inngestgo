package fn

import (
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

func TestTimeoutMarhsal(t *testing.T) {
	t.Run("empty", func(t *testing.T) {
		v := Timeouts{}
		byt, err := v.MarshalJSON()
		require.NoError(t, err)
		require.EqualValues(t, `{}`, byt)
	})

	t.Run("start", func(t *testing.T) {
		v := Timeouts{Start: ptr(time.Second)}
		byt, err := v.MarshalJSON()
		require.NoError(t, err)
		require.EqualValues(t, `{"start":"1s"}`, string(byt))
	})

	t.Run("finish", func(t *testing.T) {
		v := Timeouts{Finish: ptr(24 * time.Hour)}
		byt, err := v.MarshalJSON()
		require.NoError(t, err)
		require.EqualValues(t, `{"finish":"1d"}`, string(byt))
	})

	t.Run("both", func(t *testing.T) {
		v := Timeouts{
			Start:  ptr((2 * time.Hour) + (30 * time.Minute)),
			Finish: ptr(24 * time.Hour),
		}
		byt, err := v.MarshalJSON()
		require.NoError(t, err)
		require.EqualValues(t, `{"finish":"1d","start":"2h30m"}`, string(byt))
	})
}

func ptr[T any](i T) *T { return &i }
