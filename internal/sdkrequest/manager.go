package sdkrequest

import (
	"context"
	"crypto/sha1"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"sync"

	"github.com/inngest/inngest/pkg/enums"
	"github.com/inngest/inngest/pkg/execution/state"
)

type requestCtxKeyType struct{}

var requestCtxKey = requestCtxKeyType{}

// InvocationManager is responsible for the lifecycle of a function invocation.
type InvocationManager interface {
	// Cancel indicates that a step has ran and cancels future steps from processing.
	Cancel()
	// Request returns the incoming executor request.
	Request() *Request
	// Err returns the error generated by step code, if a step errored.
	Err() error
	// SetErr sets the invocation's error.
	SetErr(err error)
	// AppendOp pushes a new generator op to the stack for future execution.
	AppendOp(op state.GeneratorOpcode)
	// Ops returns all pushed generator ops to the stack for future execution.
	Ops() []state.GeneratorOpcode
	// Step returns step data for the given unhashed operation, if present in the
	// incoming request data.
	Step(op UnhashedOp) (json.RawMessage, bool)
	// NewOp generates a new unhashed op for creating a state.GeneratorOpcode.  This
	// is required for future execution of a step.
	NewOp(op enums.Opcode, name string, opts map[string]any) UnhashedOp
}

// NewManager returns an InvocationManager to manage the incoming executor request.  This
// is required for step tooling to process.
func NewManager(cancel context.CancelFunc, request *Request) InvocationManager {
	return &requestCtxManager{
		cancel:  cancel,
		request: request,
		indexes: map[string]int{},
		l:       &sync.RWMutex{},
	}
}

func SetManager(ctx context.Context, r InvocationManager) context.Context {
	return context.WithValue(ctx, requestCtxKey, r)
}

func Manager(ctx context.Context) (InvocationManager, bool) {
	mgr, ok := ctx.Value(requestCtxKey).(InvocationManager)
	return mgr, ok
}

type requestCtxManager struct {
	// cancel ends the context and prevents any other tools from running.
	cancel func()
	// err stores the error from any step ran.
	err error
	// Ops holds a list of buffered generator opcodes to send to the executor
	// after this invocation.
	ops []state.GeneratorOpcode
	// request represents the incoming request.
	request *Request
	// Indexes represents a map of indexes for each unhashed op.
	indexes map[string]int
	l       *sync.RWMutex
}

func (r *requestCtxManager) Cancel() {
	r.cancel()
}

func (r *requestCtxManager) SetRequest(req *Request) {
	r.request = req
}

func (r *requestCtxManager) Request() *Request {
	return r.request
}

func (r *requestCtxManager) SetErr(err error) {
	r.err = err
}

func (r *requestCtxManager) Err() error {
	return r.err
}

func (r *requestCtxManager) AppendOp(op state.GeneratorOpcode) {
	r.l.Lock()
	defer r.l.Unlock()

	if r.ops == nil {
		r.ops = []state.GeneratorOpcode{op}
		return
	}

	r.ops = append(r.ops, op)
}

func (r *requestCtxManager) Ops() []state.GeneratorOpcode {
	return r.ops
}

func (r *requestCtxManager) Step(op UnhashedOp) (json.RawMessage, bool) {
	r.l.RLock()
	defer r.l.RUnlock()
	val, ok := r.request.Steps[op.MustHash()]
	return val, ok
}

func (r *requestCtxManager) NewOp(op enums.Opcode, id string, opts map[string]any) UnhashedOp {
	r.l.Lock()
	defer r.l.Unlock()

	// NOTE: Indexes are 1-indexed, ie. start at 1.  We always add 1 to the
	// existing index to ensure that we're incrementing correctly.  This is due
	// to the original spec and publicly released hashing.
	n := r.indexes[id] + 1

	// Update indexes for each particualar key.
	r.indexes[id] = n

	return UnhashedOp{
		ID:   id,
		Op:   op,
		Opts: opts,
		Pos:  uint(n),
	}
}

type UnhashedOp struct {
	ID     string         `json:"id"`
	Op     enums.Opcode   `json:"op"`
	Opts   map[string]any `json:"opts"`
	Pos    uint           `json:"pos"`
	Parent *string        `json:"parent,omitempty"`
}

func (u UnhashedOp) Hash() (string, error) {
	input := fmt.Sprintf("%s:%d", u.ID, u.Pos)
	sum := sha1.Sum([]byte(input))
	return hex.EncodeToString(sum[:]), nil
}

func (u UnhashedOp) MustHash() string {
	h, err := u.Hash()
	if err != nil {
		panic(fmt.Errorf("error hashing op: %w", err))
	}
	return h
}
