package interval

import "time"

// NewInterval creates a new Interval from start and end times.
func New(start, end time.Time) Interval {
	return Interval{
		A: start.UnixNano(),
		B: end.Sub(start).Nanoseconds(),
	}
}

// Interval represents an interval between a start and end time.
//
// In order to minimize space, the start time is represented as UnixNano(), and the duration
// is represented as the number of nanoseconds after the start.
type Interval struct {
	// A represents the start of the interval, taken as the nanoseconds after the unix epoch
	// (eg. via time.Now().UnixNano())
	A int64 `json:"a"`
	// B represents the duration, as nanoseconds.
	B int64 `json:"b"`
}

// Start returns the start time of the interval.
func (i Interval) Start() time.Time {
	return time.Unix(0, i.A)
}

// End returns the end time of the interval.
func (i Interval) End() time.Time {
	return i.Start().Add(time.Nanosecond * time.Duration(i.B))
}

// Duration returns the duration of the interval.
func (i Interval) Duration() time.Duration {
	return time.Duration(i.B)
}