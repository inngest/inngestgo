package inngestgo

import (
	"encoding/json"
	"net/url"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestRegisterFunctionOrder(t *testing.T) {
	for _, tc := range []struct {
		name    string
		batches [][]string
		want    []string
	}{
		{name: "empty", batches: [][]string{nil}, want: []string{}},
		{name: "single", batches: [][]string{{"alpha"}}, want: []string{"app-alpha"}},
		{name: "one batch", batches: [][]string{{"zeta", "alpha", "middle"}}, want: []string{"app-alpha", "app-middle", "app-zeta"}},
		{name: "incremental", batches: [][]string{{"zeta"}, {"middle"}, {"alpha"}}, want: []string{"app-alpha", "app-middle", "app-zeta"}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			h := &handler{}
			for _, batch := range tc.batches {
				functions := make([]ServableFunction, 0, len(batch))
				for _, id := range batch {
					functions = append(functions, servableFunc{appID: "app", fc: FunctionOpts{ID: id}})
				}
				h.Register(functions...)
			}
			// Re-registering an unchanged set must not reshuffle its sync payload.
			for range 32 {
				h.Register(h.GetFunctions()...)
				got := make([]string, 0, len(h.GetFunctions()))
				for _, f := range h.GetFunctions() {
					got = append(got, f.FullyQualifiedID())
				}
				require.Equal(t, tc.want, got)
			}
		})
	}
}

func TestRegisterReplacesFunctionBySlug(t *testing.T) {
	h := &handler{}
	old := servableFunc{appID: "app", fc: FunctionOpts{ID: "alpha", Name: "old"}}
	replacement := servableFunc{appID: "app", fc: FunctionOpts{ID: "alpha", Name: "replacement"}}
	last := servableFunc{appID: "app", fc: FunctionOpts{ID: "alpha", Name: "last"}}
	h.Register(old, servableFunc{appID: "app", fc: FunctionOpts{ID: "zeta"}})
	h.Register(replacement, last)
	require.Len(t, h.GetFunctions(), 2)
	require.Equal(t, "app-alpha", h.GetFunctions()[0].FullyQualifiedID())
	require.Equal(t, "last", h.GetFunctions()[0].Name())
	require.Equal(t, "app-zeta", h.GetFunctions()[1].FullyQualifiedID())
}

func TestRegisterStableConnectFunctionPayload(t *testing.T) {
	functions := []ServableFunction{
		servableFunc{appID: "app", fc: FunctionOpts{ID: "alpha"}, trigger: EventTrigger("test/alpha", nil)},
		servableFunc{appID: "app", fc: FunctionOpts{ID: "middle"}, trigger: EventTrigger("test/middle", nil)},
		servableFunc{appID: "app", fc: FunctionOpts{ID: "zeta"}, trigger: EventTrigger("test/zeta", nil)},
	}
	connectURL := url.URL{Scheme: "ws", Host: "connect"}
	serialize := func(functions []ServableFunction) []byte {
		configs, err := createFunctionConfigs("app", functions, connectURL, true)
		require.NoError(t, err)
		payload, err := json.Marshal(configs)
		require.NoError(t, err)
		return payload
	}
	want := serialize(functions)
	for _, order := range [][]int{{0, 1, 2}, {2, 1, 0}, {1, 2, 0}} {
		for range 16 {
			h := &handler{}
			for _, i := range order {
				h.Register(functions[i])
			}
			// Connect hashes this ordered JSON array. Equivalent registrations
			// must produce the same bytes across workers and restarts.
			require.Equal(t, string(want), string(serialize(h.GetFunctions())))
		}
	}
}
