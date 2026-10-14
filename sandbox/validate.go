package sandbox

import (
	"encoding/json"
	"regexp"
	"strings"
	"time"
	"unicode"
	"unicode/utf8"
)

// these limits match the sandbox API.  input that breaks them fails here, so
// the SDK never sends a request or records a step that the API would reject.
const (
	maxRunningTimeout   = 5 * time.Minute
	defaultRunningWait  = 120 * time.Second
	maxCommandTimeout   = 5 * time.Minute
	defaultCommandWait  = 30 * time.Second
	maxLifecycleTimeout = 5 * time.Minute
	maxTailBytes        = 512 * 1024

	maxArgvCount   = 128
	maxArgvBytes   = 32 * 1024
	maxEnvCount    = 256
	maxEnvBytes    = 64 * 1024
	maxCwdBytes    = 4096
	maxSpecBytes   = 96 * 1024
	maxNameRunes   = 255
	maxSecretBytes = 256
	maxListLimit   = 250
	defaultLimit   = 50
)

var uuidRegexp = regexp.MustCompile(`^[0-9a-f]{8}(?:-[0-9a-f]{4}){3}-[0-9a-f]{12}$`)

func validateUUID(id, field string) error {
	if !uuidRegexp.MatchString(id) || id == "00000000-0000-0000-0000-000000000000" {
		return validationErrorf("%s must be a canonical lowercase UUID, got %q", field, id)
	}
	return nil
}

func validateName(name, field string) error {
	switch {
	case name == "":
		return validationErrorf("%s must not be empty", field)
	case !utf8.ValidString(name):
		return validationErrorf("%s must be valid UTF-8", field)
	case utf8.RuneCountInString(name) > maxNameRunes:
		return validationErrorf("%s must not exceed %d characters", field, maxNameRunes)
	case hasEdgeSpace(name):
		return validationErrorf("%s must not have leading or trailing whitespace", field)
	case strings.IndexFunc(name, unicode.IsControl) >= 0:
		return validationErrorf("%s must not contain control characters", field)
	}
	return nil
}

func validateSecretName(name string) error {
	if name == "" || len(name) > maxSecretBytes || hasEdgeSpace(name) || strings.ContainsAny(name, "\r\n\x00") || !utf8.ValidString(name) {
		return validationErrorf("secret %q must be a nonempty workspace secret name of at most %d bytes without leading or trailing whitespace, CR, LF, or NUL", name, maxSecretBytes)
	}
	return nil
}

func hasEdgeSpace(s string) bool {
	first, _ := utf8.DecodeRuneInString(s)
	last, _ := utf8.DecodeLastRuneInString(s)
	return unicode.IsSpace(first) || unicode.IsSpace(last)
}

func validateText(value, field string) error {
	if strings.ContainsRune(value, 0) {
		return validationErrorf("%s must not contain NUL", field)
	}
	if !utf8.ValidString(value) {
		return validationErrorf("%s must be valid UTF-8", field)
	}
	return nil
}

func validateEnvironment(env map[string]string) error {
	if len(env) > maxEnvCount {
		return validationErrorf("environment must not contain more than %d entries", maxEnvCount)
	}
	size := 0
	for k, v := range env {
		if k == "" || strings.Contains(k, "=") {
			return validationErrorf("environment keys must be nonempty and must not contain '='")
		}
		if err := validateText(k, "environment key "+k); err != nil {
			return err
		}
		if err := validateText(v, "environment."+k); err != nil {
			return err
		}
		size += len(k) + 1 + len(v)
	}
	if size > maxEnvBytes {
		return validationErrorf("environment must not exceed %d bytes", maxEnvBytes)
	}
	return nil
}

// duration converts d to whole milliseconds.  zero selects def.
func duration(d, def, max time.Duration, field string) (time.Duration, error) {
	if d == 0 {
		d = def
	}
	if d < time.Millisecond {
		return 0, validationErrorf("%s must be at least 1ms", field)
	}
	if d > max {
		return 0, validationErrorf("%s must not exceed %s", field, max)
	}
	return d.Truncate(time.Millisecond), nil
}

func msString(d time.Duration) string {
	return formatInt(d.Milliseconds()) + "ms"
}

// createBody is the request body of POST /v2/sandboxes.
type createBody struct {
	Name        string            `json:"name"`
	VCPU        int               `json:"vcpu,omitempty"`
	MemoryMB    int               `json:"memoryMb,omitempty"`
	Environment map[string]string `json:"environment,omitempty"`
	Secrets     []string          `json:"secrets,omitempty"`
	SnapshotID  string            `json:"snapshotId,omitempty"`
}

// normalizeCreate validates o.  wait is zero when o.NoWait is set.
func normalizeCreate(o CreateOpts) (body createBody, wait time.Duration, err error) {
	if err := validateName(o.Name, "name"); err != nil {
		return body, 0, err
	}
	body.Name = o.Name

	if o.SnapshotID != "" {
		if o.VCPU != 0 || o.MemoryMB != 0 || len(o.Environment) > 0 || len(o.Secrets) > 0 {
			return body, 0, validationErrorf("a sandbox created from a snapshot must not set VCPU, MemoryMB, Environment, or Secrets")
		}
		if err := validateUUID(o.SnapshotID, "snapshot ID"); err != nil {
			return body, 0, err
		}
		body.SnapshotID = o.SnapshotID
	} else {
		if o.VCPU <= 0 || o.MemoryMB <= 0 {
			return body, 0, validationErrorf("VCPU and MemoryMB must be positive, or SnapshotID must be set")
		}
		if uint64(o.VCPU) > 0xffffffff || uint64(o.MemoryMB) > 0xffffffff {
			return body, 0, validationErrorf("VCPU and MemoryMB must not exceed 4294967295")
		}
		body.VCPU, body.MemoryMB = o.VCPU, o.MemoryMB

		// secrets share the environment namespace in the sandbox, so they
		// count toward the environment limits.
		merged := make(map[string]string, len(o.Environment)+len(o.Secrets))
		for k, v := range o.Environment {
			merged[k] = v
		}
		for _, name := range o.Secrets {
			if err := validateSecretName(name); err != nil {
				return body, 0, err
			}
			if _, ok := o.Environment[name]; ok {
				return body, 0, validationErrorf("secrets must not overlap with environment")
			}
			if _, ok := merged[name]; ok {
				return body, 0, validationErrorf("secrets must not contain duplicate names")
			}
			merged[name] = ""
		}
		if err := validateEnvironment(merged); err != nil {
			return body, 0, err
		}
		body.Environment = o.Environment
		body.Secrets = o.Secrets
	}

	if o.NoWait {
		return body, 0, nil
	}
	wait, err = duration(o.RunningTimeout, defaultRunningWait, maxRunningTimeout, "RunningTimeout")
	return body, wait, err
}

// processSpec is the command part of exec and process start requests.
type processSpec struct {
	Command     Command           `json:"command"`
	Environment map[string]string `json:"environment,omitempty"`
	Cwd         string            `json:"cwd,omitempty"`
}

func normalizeSpec(cmd Command, env map[string]string, cwd string) (processSpec, error) {
	if len(cmd) == 0 {
		return processSpec{}, validationErrorf("command must not be empty")
	}
	if len(cmd) > maxArgvCount {
		return processSpec{}, validationErrorf("command must not contain more than %d arguments", maxArgvCount)
	}
	if cmd[0] == "" {
		return processSpec{}, validationErrorf("command[0] must not be empty")
	}
	size := 0
	for i, arg := range cmd {
		if err := validateText(arg, "command["+formatInt(int64(i))+"]"); err != nil {
			return processSpec{}, err
		}
		size += len(arg)
	}
	if size > maxArgvBytes {
		return processSpec{}, validationErrorf("command must not exceed %d bytes", maxArgvBytes)
	}
	if err := validateEnvironment(env); err != nil {
		return processSpec{}, err
	}
	if err := validateText(cwd, "cwd"); err != nil {
		return processSpec{}, err
	}
	if len(cwd) > maxCwdBytes {
		return processSpec{}, validationErrorf("cwd must not exceed %d bytes", maxCwdBytes)
	}

	// the API measures the encoded spec with encoding/json, under these keys.
	encoded, _ := json.Marshal(struct {
		Argv []string          `json:"argv"`
		Env  map[string]string `json:"env,omitempty"`
		Cwd  string            `json:"cwd,omitempty"`
	}{cmd, env, cwd})
	if len(encoded) > maxSpecBytes {
		return processSpec{}, validationErrorf("command, environment, and cwd must not exceed %d encoded bytes", maxSpecBytes)
	}

	if len(env) == 0 {
		env = nil
	}
	return processSpec{Command: append(Command(nil), cmd...), Environment: env, Cwd: cwd}, nil
}

func normalizeList(cursor string, limit int) (string, int, error) {
	if limit == 0 {
		limit = defaultLimit
	}
	if limit < 1 || limit > maxListLimit {
		return "", 0, validationErrorf("limit must be between 1 and %d", maxListLimit)
	}
	return cursor, limit, nil
}

func normalizeSignal(o SignalOpts) error {
	if o.Signal < 1 || o.Signal > 64 {
		return validationErrorf("signal must be between 1 and 64")
	}
	return nil
}

func normalizeTail(o OutputOpts) error {
	if o.TailBytes < 0 || o.TailBytes > maxTailBytes {
		return validationErrorf("TailBytes must be between 0 and %d", maxTailBytes)
	}
	return nil
}

func validateFilePath(path string) error {
	if err := validateText(path, "path"); err != nil {
		return err
	}
	if !strings.HasPrefix(path, "/") {
		return validationErrorf("path must be absolute")
	}
	if len(path) > maxCwdBytes {
		return validationErrorf("path must not exceed %d bytes", maxCwdBytes)
	}
	return nil
}
