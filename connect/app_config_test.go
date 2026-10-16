package connect

import (
	"testing"

	"github.com/inngest/inngestgo/internal/fn"
	"github.com/stretchr/testify/require"
)

func TestAppConfigurations(t *testing.T) {
	version := "v1"
	tests := []struct {
		name      string
		apps      []ConnectApp
		wantSlugs stepURLsByApp
	}{
		{
			name: "step URL is logged",
			apps: []ConnectApp{{
				AppName:    "app",
				AppVersion: &version,
				Functions: []fn.SyncConfig{{
					Slug: "app-fn",
					Steps: map[string]fn.SDKStep{
						"step": {ID: "step", Runtime: map[string]any{"url": "ws://connect?fnId=app-fn&step=step"}},
					},
				}},
			}},
			wantSlugs: stepURLsByApp{
				"app": {"app-fn": {"ws://connect?fnId=app-fn&step=step"}},
			},
		},
		{
			name: "step without a URL does not panic",
			apps: []ConnectApp{{
				AppName: "app",
				Functions: []fn.SyncConfig{{
					Slug: "app-fn",
					Steps: map[string]fn.SDKStep{
						"step": {ID: "step", Runtime: map[string]any{}},
					},
				}},
			}},
			wantSlugs: stepURLsByApp{
				"app": {"app-fn": {""}},
			},
		},
		{
			name: "app without functions",
			apps: []ConnectApp{{AppName: "app"}},
			wantSlugs: stepURLsByApp{
				"app": {},
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			r := require.New(t)

			configs, slugs, err := appConfigurations(tt.apps)
			r.NoError(err)
			r.Equal(tt.wantSlugs, slugs)
			r.Len(configs, len(tt.apps))
			for i, app := range tt.apps {
				r.Equal(app.AppName, configs[i].AppName)
				r.Equal(app.AppVersion, configs[i].AppVersion)
				r.NotEmpty(configs[i].Functions)
			}
		})
	}
}
