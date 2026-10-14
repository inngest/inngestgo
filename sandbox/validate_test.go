package sandbox

import (
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestNormalizeCreate(t *testing.T) {
	snapshotID := "8b8d6a43-5d3f-4a62-9a9e-2a4a5f6c7d8e"
	type testCase struct {
		name    string
		opts    CreateOpts
		wait    time.Duration
		wantErr string
	}
	tests := []testCase{
		{"fresh", CreateOpts{Name: "box", VCPU: 1, MemoryMB: 512}, defaultRunningWait, ""},
		{"from snapshot", CreateOpts{Name: "box", SnapshotID: snapshotID, RunningTimeout: time.Minute}, time.Minute, ""},
		{"no wait", CreateOpts{Name: "box", VCPU: 1, MemoryMB: 512, NoWait: true}, 0, ""},
		{"missing name", CreateOpts{VCPU: 1, MemoryMB: 512}, 0, "name must not be empty"},
		{"name with edge space", CreateOpts{Name: " box", VCPU: 1, MemoryMB: 512}, 0, "whitespace"},
		{"name with control character", CreateOpts{Name: "b\tox", VCPU: 1, MemoryMB: 512}, 0, "control"},
		{"long name", CreateOpts{Name: strings.Repeat("é", 256), VCPU: 1, MemoryMB: 512}, 0, "255 characters"},
		{"missing resources", CreateOpts{Name: "box"}, 0, "must be positive"},
		{"snapshot with resources", CreateOpts{Name: "box", SnapshotID: snapshotID, VCPU: 1}, 0, "must not set"},
		{"bad snapshot ID", CreateOpts{Name: "box", SnapshotID: "ABC"}, 0, "canonical lowercase UUID"},
		{"secret overlaps environment", CreateOpts{Name: "box", VCPU: 1, MemoryMB: 1, Environment: map[string]string{"K": "v"}, Secrets: []string{"K"}}, 0, "overlap"},
		{"duplicate secret", CreateOpts{Name: "box", VCPU: 1, MemoryMB: 1, Secrets: []string{"K", "K"}}, 0, "duplicate"},
		{"env key with =", CreateOpts{Name: "box", VCPU: 1, MemoryMB: 1, Environment: map[string]string{"A=B": "v"}}, 0, "'='"},
		{"timeout too long", CreateOpts{Name: "box", VCPU: 1, MemoryMB: 1, RunningTimeout: 6 * time.Minute}, 0, "must not exceed"},
	}
	if strconv.IntSize == 64 {
		tooLarge := uint64(1) << 32
		tests = append(tests, testCase{"resources exceed API range", CreateOpts{Name: "box", VCPU: int(tooLarge), MemoryMB: 512}, 0, "must not exceed"})
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, wait, err := normalizeCreate(tt.opts)
			if tt.wantErr != "" {
				var ve *ValidationError
				require.ErrorAs(t, err, &ve)
				assert.Contains(t, err.Error(), tt.wantErr)
				return
			}
			require.NoError(t, err)
			assert.Equal(t, tt.wait, wait)
		})
	}
}

func TestNormalizeSpec(t *testing.T) {
	tests := []struct {
		name    string
		cmd     Command
		env     map[string]string
		cwd     string
		wantErr string
	}{
		{"shell", Shell("echo hi"), nil, "", ""},
		{"argv", Argv("/bin/ls", "-la"), map[string]string{"A": "1"}, "/tmp", ""},
		{"empty", Command{}, nil, "", "must not be empty"},
		{"empty executable", Command{""}, nil, "", "command[0]"},
		{"NUL", Shell("a\x00b"), nil, "", "NUL"},
		{"invalid UTF-8", Shell("\xff"), nil, "", "UTF-8"},
		{"too many args", make(Command, maxArgvCount+1), nil, "", "arguments"},
		{"too many bytes", Shell(strings.Repeat("a", maxArgvBytes)), nil, "", "bytes"},
		{"long cwd", Shell("ls"), nil, strings.Repeat("a", maxCwdBytes+1), "cwd"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			spec, err := normalizeSpec(tt.cmd, tt.env, tt.cwd)
			if tt.wantErr != "" {
				require.Error(t, err)
				assert.Contains(t, err.Error(), tt.wantErr)
				return
			}
			require.NoError(t, err)
			assert.Equal(t, tt.cmd, spec.Command)
		})
	}
}

func TestDuration(t *testing.T) {
	d, err := duration(0, time.Second, time.Minute, "t")
	require.NoError(t, err)
	assert.Equal(t, time.Second, d)

	d, err = duration(1500*time.Microsecond, time.Second, time.Minute, "t")
	require.NoError(t, err)
	assert.Equal(t, time.Millisecond, d, "durations go to the API in whole milliseconds")

	_, err = duration(-time.Second, time.Second, time.Minute, "t")
	assert.Error(t, err)
	_, err = duration(2*time.Minute, time.Second, time.Minute, "t")
	assert.Error(t, err)
}
