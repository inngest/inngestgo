package event

import (
	"testing"

	"github.com/stretchr/testify/require"
)

func TestValidateEventDataType(t *testing.T) {
	t.Run("valid", func(t *testing.T) {
		r := require.New(t)

		err := ValidateEventDataType(nil)
		r.NoError(err)

		err = ValidateEventDataType(map[string]any{})
		r.NoError(err)

		err = ValidateEventDataType(struct{}{})
		r.NoError(err)

		val := struct{}{}
		err = ValidateEventDataType(&val)
		r.NoError(err)

		mapVal := map[string]any{}
		err = ValidateEventDataType(&mapVal)
		r.NoError(err)
	})

	t.Run("invalid", func(t *testing.T) {
		r := require.New(t)

		err := ValidateEventDataType(1)
		r.Error(err)

		val := 1
		err = ValidateEventDataType(&val)
		r.Error(err)

		err = ValidateEventDataType(func() {})
		r.Error(err)

		err = ValidateEventDataType("hi")
		r.Error(err)

		err = ValidateEventDataType(true)
		r.Error(err)

		err = ValidateEventDataType([]map[string]any{})
		r.Error(err)

		err = ValidateEventDataType([]struct{}{})
		r.Error(err)

		var structPtr *struct{}
		err = ValidateEventDataType(structPtr)
		r.Error(err)

		var mapPtr *map[string]any
		err = ValidateEventDataType(mapPtr)
		r.Error(err)
	})
}

func TestGenericEventMap(t *testing.T) {
	t.Run("nil pointer data becomes empty map", func(t *testing.T) {
		var data *struct{}

		out := GenericEvent[*struct{}]{
			Name: "test.event",
			Data: data,
		}.Map()

		require.Equal(t, map[string]any{}, out["data"])
	})
}
