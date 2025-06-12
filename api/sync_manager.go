package api

import (
	"context"
	"encoding/json"
	"sync"

	"github.com/inngest/inngest/pkg/enums"
	"github.com/inngest/inngestgo/experimental"
	"github.com/inngest/inngestgo/internal/fn"
	"github.com/inngest/inngestgo/internal/middleware"
	"github.com/inngest/inngestgo/internal/sdkrequest"
)

type apiContextKey string

const (
	// APIFunctionKey marks a context as being within an API function
	APIFunctionKey = apiContextKey("api-function")
)

// syncInvocationManager implements InvocationManager for synchronous API execution
type syncInvocationManager struct {
	runID      string
	apiManager APIManager
	signingKey string
	err        error
	ops        []sdkrequest.GeneratorOpcode
	l          *sync.RWMutex
	mw         *middleware.MiddlewareManager
	fn         fn.ServableFunction
	cancelled  bool
}

// NewRequestManager creates a new InvocationManager for API functions
func NewRequestManager(
	runID string,
	apiManager APIManager,
	signingKey string,
	mw *middleware.MiddlewareManager,
	fn fn.ServableFunction,
) sdkrequest.InvocationManager {
	return &syncInvocationManager{
		runID:      runID,
		apiManager: apiManager,
		signingKey: signingKey,
		l:          &sync.RWMutex{},
		mw:         mw,
		fn:         fn,
	}
}

func (s *syncInvocationManager) Cancel() {
	// No-op for sync execution - API functions should continue execution
	// rather than being cancelled after each step
}

func (s *syncInvocationManager) Request() *sdkrequest.Request {
	// For API functions, we don't have a traditional request with steps
	// This is a new execution, so return empty request
	return &sdkrequest.Request{
		Steps: make(map[string]json.RawMessage),
		CallCtx: sdkrequest.CallCtx{
			RunID: s.runID,
			Env:   "prod", // TODO: Make configurable
		},
	}
}

func (s *syncInvocationManager) Err() error {
	return s.err
}

func (s *syncInvocationManager) SetErr(err error) {
	s.err = err
}

func (s *syncInvocationManager) AppendOp(op sdkrequest.GeneratorOpcode) {
	s.l.Lock()
	defer s.l.Unlock()

	if s.ops == nil {
		s.ops = []sdkrequest.GeneratorOpcode{op}
	} else {
		s.ops = append(s.ops, op)
	}

	// Checkpoint the step in the background
	if s.apiManager != nil {
		ctx := context.Background()
		_ = s.apiManager.CheckpointStep(ctx, s.runID, op)
	}
}

func (s *syncInvocationManager) Ops() []sdkrequest.GeneratorOpcode {
	s.l.RLock()
	defer s.l.RUnlock()
	return s.ops
}

func (s *syncInvocationManager) Step(ctx context.Context, op sdkrequest.UnhashedOp) (json.RawMessage, bool) {
	// For API functions, we don't have pre-existing step data
	// Always return false to indicate the step should run fresh
	return nil, false
}

func (s *syncInvocationManager) ReplayedStep(hashedID string) bool {
	// For API functions, we never replay steps in the same request
	return false
}

func (s *syncInvocationManager) NewOp(op enums.Opcode, id string, opts map[string]any) sdkrequest.UnhashedOp {
	return sdkrequest.UnhashedOp{
		ID:   id,
		Op:   op,
		Opts: opts,
		Pos:  0, // No positioning needed for API functions
	}
}

func (s *syncInvocationManager) SigningKey() string {
	return s.signingKey
}

func (s *syncInvocationManager) CallContext() middleware.CallContext {
	opts := fn.FunctionOpts{}
	if s.fn != nil {
		opts = s.fn.Config()
	}

	return experimental.CallContext{
		FunctionOpts: opts,
		Env:          "prod", // TODO: Make configurable
		RunID:        s.runID,
		Attempt:      0, // API functions don't retry
	}
}

func (s *syncInvocationManager) StepMode() sdkrequest.StepMode {
	// API functions continue execution after checkpointing
	return sdkrequest.StepModeContinue
}

