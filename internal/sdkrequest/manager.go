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
	"github.com/inngest/inngestgo/experimental"
	"github.com/inngest/inngestgo/internal/fn"
	"github.com/inngest/inngestgo/internal/middleware"
	"github.com/inngest/inngestgo/internal/types"
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
	// These represent new steps that have not been previously memoized.
	Ops() []state.GeneratorOpcode
	// Step returns step data for the given unhashed operation, if present in the
	// incoming request data.
	Step(ctx context.Context, op UnhashedOp) (json.RawMessage, bool)
	// ReplayedStep returns whether we've replayed the given hashed step ID yet.
	ReplayedStep(hashedID string) bool
	// NewOp generates a new unhashed op for creating a state.GeneratorOpcode.  This
	// is required for future execution of a step.
	NewOp(op enums.Opcode, id string, opts map[string]any) UnhashedOp
	// SigningKey returns the signing key used for this request.  This lets us
	// retrieve creds for eg. publishing or API alls.
	SigningKey() string
	// MiddlewareCallCtx exposes the call context for middleware calls.
	MiddlewareCallCtx() experimental.CallContext
}

// NewManager returns an InvocationManager to manage the incoming executor request.  This
// is required for step tooling to process.
func NewManager(
	fn fn.ServableFunction,
	mw *middleware.MiddlewareManager,
	cancel context.CancelFunc,
	request *Request,
	signingKey string,
) InvocationManager {
	unseen := types.Set[string]{}
	for k := range request.Steps {
		unseen.Add(k)
	}

	return &requestCtxManager{
		fn:         fn,
		cancel:     cancel,
		request:    request,
		indexes:    map[string]int{},
		l:          &sync.RWMutex{},
		signingKey: signingKey,
		seen:       map[string]struct{}{},
		seenLock:   &sync.RWMutex{},
		unseen:     &unseen,
		mw:         mw,
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
	fn fn.ServableFunction
	// key is the signing key
	signingKey string
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

	// seen represents all ops seen in this request, by calling Step(ctx)
	// to retrieve step data.
	seen     map[string]struct{}
	seenLock *sync.RWMutex

	unseen *types.Set[string]

	mw *middleware.MiddlewareManager
}

func (r *requestCtxManager) SigningKey() string {
	return r.signingKey
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

func (r *requestCtxManager) MiddlewareCallCtx() middleware.CallContext {
	return middleware.CallContext{
		FunctionOpts: r.fn.Config(),
		Env:          r.request.CallCtx.Env,
		RunID:        r.request.CallCtx.RunID,
		Attempt:      r.request.CallCtx.Attempt,
	}
}

func (r *requestCtxManager) Step(ctx context.Context, op UnhashedOp) (json.RawMessage, bool) {
	hash := op.MustHash()
	r.l.RLock()
	defer r.l.RUnlock()

	r.unseen.Remove(hash)
	if r.unseen.Len() == 0 {
		// We exhausted all memoized steps, so we're about to run "new code"
		// after a memoized step.
		r.mw.BeforeExecution(ctx, r.MiddlewareCallCtx())
	}

	val, ok := r.request.Steps[hash]
	if ok {
		r.seenLock.Lock()
		r.seen[hash] = struct{}{}
		r.seenLock.Unlock()
	}
	return val, ok
}

func (r *requestCtxManager) ReplayedStep(hashedID string) bool {
	r.seenLock.RLock()
	_, ok := r.seen[hashedID]
	r.seenLock.RUnlock()
	return ok
}

func (r *requestCtxManager) NewOp(op enums.Opcode, id string, opts map[string]any) UnhashedOp {
	r.l.Lock()
	defer r.l.Unlock()

	n, ok := r.indexes[id]
	if ok {
		// We have an index already, so increase the counter as we're
		// adding to this key.
		n += 1
	}

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
	Op   enums.Opcode   `json:"op"`
	ID   string         `json:"id"`
	Name string         `json:"name"`
	Opts map[string]any `json:"opts"`
	Pos  uint           `json:"-"`
}

func (u UnhashedOp) Hash() (string, error) {
	input := u.ID
	if u.Pos > 0 {
		// We only suffix the counter if there's > 1 operation with the same ID.
		input = fmt.Sprintf("%s:%d", u.ID, u.Pos)
	}
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
