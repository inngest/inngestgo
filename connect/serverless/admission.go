package serverless

import (
	"sync"
	"sync/atomic"
	"time"
)

// drainingBit is set in admission.state once the worker stops taking
// requests.  the low bits keep counting the requests still in flight.
const drainingBit = uint64(1) << 63

// admission counts the requests in flight and decides if a new one may start.
type admission struct {
	// concurrency is the most requests in flight at once.
	concurrency uint64
	// max is the most requests the worker admits in its lifetime.  0 means no
	// limit.
	max uint64
	// deadline is the unix nano time after which the worker admits no
	// request.  0 means no deadline.
	deadline int64

	state atomic.Uint64
	// ctr counts every request admitted so far.
	ctr atomic.Uint64

	// lastRelease is the unix nano time of the most recent release, or of
	// start.  the idle timer measures from it.
	lastRelease atomic.Int64

	// maxAdmitted closes when the worker admits its last allowed request.
	maxAdmitted     chan struct{}
	maxAdmittedOnce sync.Once
}

// newAdmission makes an admission.  maxRequests 0 and a zero deadline mean no
// limit.
func newAdmission(concurrency, maxRequests int, deadline time.Time) *admission {
	a := &admission{
		concurrency: uint64(concurrency),
		max:         uint64(maxRequests),
		maxAdmitted: make(chan struct{}),
	}
	if !deadline.IsZero() {
		a.deadline = deadline.UnixNano()
	}
	a.lastRelease.Store(time.Now().UnixNano())
	return a
}

type admitResult int

const (
	admitted admitResult = iota
	rejectDraining
	rejectAtCapacity
	rejectAtDeadline
	rejectMaxRequests
)

// admit counts one more request in flight.  it fails once the worker drains,
// when concurrency requests are in flight, after max requests, or after the
// deadline.
func (a *admission) admit() admitResult {
	for {
		n := a.state.Load()
		if n&drainingBit != 0 {
			return rejectDraining
		}
		if n >= a.concurrency {
			return rejectAtCapacity
		}
		if a.maxReached() {
			return rejectMaxRequests
		}
		if a.pastDeadline(time.Now()) {
			return rejectAtDeadline
		}
		if !a.state.CompareAndSwap(n, n+1) {
			continue
		}
		// two admits can pass the maxReached check at once.  the counter
		// decides which of them gets the last slot, and the other one gives
		// its in-flight count back.
		if a.max > 0 {
			switch c := a.ctr.Add(1); {
			case c > a.max:
				a.state.Add(^uint64(0))
				return rejectMaxRequests
			case c == a.max:
				a.maxAdmittedOnce.Do(func() { close(a.maxAdmitted) })
			}
		}
		return admitted
	}
}

// maxReached reports whether the worker has admitted max requests.
func (a *admission) maxReached() bool {
	return a.max > 0 && a.ctr.Load() >= a.max
}

// pastDeadline reports whether now is at or after the deadline.
func (a *admission) pastDeadline(now time.Time) bool {
	return a.deadline > 0 && now.UnixNano() >= a.deadline
}

// release counts one request as done.  the idle timer restarts here, before
// the count drops, so an idle check never sees a zero count with a stale time.
func (a *admission) release() {
	a.lastRelease.Store(time.Now().UnixNano())
	a.state.Add(^uint64(0))
}

// tryDrain starts draining only when no request is in flight.
func (a *admission) tryDrain() bool {
	return a.state.CompareAndSwap(0, drainingBit)
}

// drain starts draining with requests still in flight.
func (a *admission) drain() {
	a.state.Or(drainingBit)
}

func (a *admission) inFlight() int {
	return int(a.state.Load() &^ drainingBit)
}

func (a *admission) draining() bool {
	return a.state.Load()&drainingBit != 0
}

// idleFor returns how long the worker has had no request in flight.  it is
// zero while a request runs.
func (a *admission) idleFor(now time.Time) time.Duration {
	if a.inFlight() > 0 {
		return 0
	}
	return now.Sub(time.Unix(0, a.lastRelease.Load()))
}
