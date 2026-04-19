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

func TestTriggerMarshalJSON(t *testing.T) {
	t.Run("event trigger with nil CronTrigger", func(t *testing.T) {
		tr := Trigger{EventTrigger: &EventTrigger{Event: "test/event"}}
		byt, err := tr.MarshalJSON()
		require.NoError(t, err)
		require.JSONEq(t, `{"event":"test/event"}`, string(byt))
	})

	t.Run("cron trigger with nil EventTrigger", func(t *testing.T) {
		tr := Trigger{CronTrigger: &CronTrigger{Cron: "* * * * *"}}
		byt, err := tr.MarshalJSON()
		require.NoError(t, err)
		require.JSONEq(t, `{"cron":"* * * * *"}`, string(byt))
	})

	t.Run("both nil", func(t *testing.T) {
		tr := Trigger{}
		byt, err := tr.MarshalJSON()
		require.NoError(t, err)
		require.EqualValues(t, `{}`, string(byt))
	})

	t.Run("event trigger with expression", func(t *testing.T) {
		expr := "event.data.id == '123'"
		tr := Trigger{EventTrigger: &EventTrigger{Event: "test/event", Expression: &expr}}
		byt, err := tr.MarshalJSON()
		require.NoError(t, err)
		require.JSONEq(t, `{"event":"test/event","expression":"event.data.id == '123'"}`, string(byt))
	})

	t.Run("cron trigger with jitter", func(t *testing.T) {
		jitter := "60"
		tr := Trigger{CronTrigger: &CronTrigger{Cron: "0 * * * *", Jitter: &jitter}}
		byt, err := tr.MarshalJSON()
		require.NoError(t, err)
		require.JSONEq(t, `{"cron":"0 * * * *","jitter":"60"}`, string(byt))
	})
}

func ptr[T any](i T) *T { return &i }
