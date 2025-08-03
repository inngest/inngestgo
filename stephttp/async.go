package stephttp

import "github.com/oklog/ulid/v2"

type AsyncResponse struct {
	RunID ulid.ULID `json:"run_id"`
	Mode  string    `json:"mode"`
	Token string    `json:"token"`
	URL   string    `json:"url"`
}
