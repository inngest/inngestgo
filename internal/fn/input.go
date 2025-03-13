package fn

// Input is the input data passed to your function.  It is comprised of the triggering event
// and call context.
type Input[T any] struct {
	Event    T        `json:"event"`
	Events   []T      `json:"events"`
	InputCtx InputCtx `json:"ctx"`
}

type InputCtx struct {
	Env        string `json:"env"`
	FunctionID string `json:"fn_id"`
	RunID      string `json:"run_id"`
	StepID     string `json:"step_id"`
	Attempt    int    `json:"attempt"`
}
