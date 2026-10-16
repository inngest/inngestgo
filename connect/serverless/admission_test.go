package serverless

import (
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

func TestAdmission(t *testing.T) {
	tests := []struct {
		name string
		// run changes adm and returns the result of the last admit.
		run          func(adm *admission) admitResult
		wantResult   admitResult
		wantInFlight int
		wantDraining bool
	}{
		{
			name:         "admits below max",
			run:          func(adm *admission) admitResult { return adm.admit() },
			wantResult:   admitted,
			wantInFlight: 1,
		},
		{
			name: "rejects at max",
			run: func(adm *admission) admitResult {
				adm.admit()
				adm.admit()
				return adm.admit()
			},
			wantResult:   rejectAtCapacity,
			wantInFlight: 2,
		},
		{
			name: "rejects after drain with requests in flight",
			run: func(adm *admission) admitResult {
				adm.admit()
				adm.drain()
				return adm.admit()
			},
			wantResult:   rejectDraining,
			wantInFlight: 1,
			wantDraining: true,
		},
		{
			name: "tryDrain fails with a request in flight",
			run: func(adm *admission) admitResult {
				adm.admit()
				require.False(t, adm.tryDrain())
				return adm.admit()
			},
			wantResult:   admitted,
			wantInFlight: 2,
		},
		{
			name: "tryDrain succeeds when idle",
			run: func(adm *admission) admitResult {
				adm.admit()
				adm.release()
				require.True(t, adm.tryDrain())
				return adm.admit()
			},
			wantResult:   rejectDraining,
			wantDraining: true,
		},
		{
			name: "release after drain keeps draining",
			run: func(adm *admission) admitResult {
				adm.admit()
				adm.drain()
				adm.release()
				return adm.admit()
			},
			wantResult:   rejectDraining,
			wantDraining: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			r := require.New(t)
			adm := newAdmission(2, 0, time.Time{})
			r.Equal(tt.wantResult, tt.run(adm))
			r.Equal(tt.wantInFlight, adm.inFlight())
			r.Equal(tt.wantDraining, adm.draining())
		})
	}
}

func TestAdmissionIdleFor(t *testing.T) {
	r := require.New(t)
	adm := newAdmission(1, 0, time.Time{})

	r.Equal(admitted, adm.admit())
	r.Zero(adm.idleFor(time.Now().Add(time.Hour)))

	adm.release()
	released := time.Now()
	r.InDelta(time.Minute, adm.idleFor(released.Add(time.Minute)), float64(time.Second))
}

func TestAdmissionLimits(t *testing.T) {
	tests := []struct {
		name        string
		maxRequests int
		deadline    time.Time
		// admits is how many requests to admit and release before the check.
		admits     int
		wantResult admitResult
	}{
		{name: "no limits", admits: 10, wantResult: admitted},
		{name: "below max requests", maxRequests: 3, admits: 2, wantResult: admitted},
		{name: "at max requests", maxRequests: 3, admits: 3, wantResult: rejectMaxRequests},
		{name: "before the deadline", deadline: time.Now().Add(time.Hour), wantResult: admitted},
		{name: "after the deadline", deadline: time.Now().Add(-time.Second), wantResult: rejectAtDeadline},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			r := require.New(t)
			adm := newAdmission(1, tt.maxRequests, tt.deadline)
			for i := 0; i < tt.admits; i++ {
				r.Equal(admitted, adm.admit(), "admit %d", i)
				adm.release()
			}
			r.Equal(tt.wantResult, adm.admit())
		})
	}
}

func TestAdmissionMaxRequestsUnderContention(t *testing.T) {
	r := require.New(t)
	adm := newAdmission(maxMultiplex, 5, time.Time{})

	var admittedCount atomic.Int64
	var wg sync.WaitGroup
	for i := 0; i < 100; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			if adm.admit() == admitted {
				admittedCount.Add(1)
			}
		}()
	}
	wg.Wait()

	r.EqualValues(5, admittedCount.Load())
	r.Equal(5, adm.inFlight())
	select {
	case <-adm.maxAdmitted:
	default:
		t.Fatal("maxAdmitted did not close")
	}
}
