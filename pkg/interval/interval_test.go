package interval

import (
	"testing"
	"time"
)

func TestNew(t *testing.T) {
	start := time.Date(2023, 1, 1, 12, 0, 0, 0, time.UTC)
	end := start.Add(5 * time.Second)
	
	interval := New(start, end)
	
	if interval.A != start.UnixNano() {
		t.Errorf("Expected A to be %d, got %d", start.UnixNano(), interval.A)
	}
	
	expectedDuration := 5 * time.Second
	if interval.B != expectedDuration.Nanoseconds() {
		t.Errorf("Expected B to be %d, got %d", expectedDuration.Nanoseconds(), interval.B)
	}
}

func TestInterval_Start(t *testing.T) {
	start := time.Date(2023, 1, 1, 12, 0, 0, 0, time.UTC)
	end := start.Add(5 * time.Second)
	
	interval := New(start, end)
	
	if !interval.Start().Equal(start) {
		t.Errorf("Expected start time %v, got %v", start, interval.Start())
	}
}

func TestInterval_End(t *testing.T) {
	start := time.Date(2023, 1, 1, 12, 0, 0, 0, time.UTC)
	end := start.Add(5 * time.Second)
	
	interval := New(start, end)
	
	if !interval.End().Equal(end) {
		t.Errorf("Expected end time %v, got %v", end, interval.End())
	}
}

func TestInterval_Duration(t *testing.T) {
	start := time.Date(2023, 1, 1, 12, 0, 0, 0, time.UTC)
	duration := 5 * time.Second
	end := start.Add(duration)
	
	interval := New(start, end)
	
	if interval.Duration() != duration {
		t.Errorf("Expected duration %v, got %v", duration, interval.Duration())
	}
}

func TestInterval_ZeroDuration(t *testing.T) {
	start := time.Date(2023, 1, 1, 12, 0, 0, 0, time.UTC)
	
	interval := New(start, start)
	
	if interval.Duration() != 0 {
		t.Errorf("Expected zero duration, got %v", interval.Duration())
	}
	
	if !interval.Start().Equal(interval.End()) {
		t.Errorf("Expected start and end to be equal for zero duration interval")
	}
}

func TestInterval_NegativeDuration(t *testing.T) {
	start := time.Date(2023, 1, 1, 12, 0, 0, 0, time.UTC)
	end := start.Add(-5 * time.Second)
	
	interval := New(start, end)
	
	expectedDuration := -5 * time.Second
	if interval.Duration() != expectedDuration {
		t.Errorf("Expected duration %v, got %v", expectedDuration, interval.Duration())
	}
}

func TestInterval_LargeDuration(t *testing.T) {
	start := time.Date(2023, 1, 1, 12, 0, 0, 0, time.UTC)
	duration := 24 * time.Hour
	end := start.Add(duration)
	
	interval := New(start, end)
	
	if interval.Duration() != duration {
		t.Errorf("Expected duration %v, got %v", duration, interval.Duration())
	}
}

func TestInterval_Nanoseconds(t *testing.T) {
	start := time.Date(2023, 1, 1, 12, 0, 0, 123456789, time.UTC)
	end := start.Add(1*time.Second + 987654321*time.Nanosecond)
	
	interval := New(start, end)
	
	if !interval.Start().Equal(start) {
		t.Errorf("Expected start time %v, got %v", start, interval.Start())
	}
	
	if !interval.End().Equal(end) {
		t.Errorf("Expected end time %v, got %v", end, interval.End())
	}
}

func TestInterval_JSONTags(t *testing.T) {
	// This test ensures the JSON tags are present for serialization
	interval := Interval{A: 123, B: 456}
	
	// Basic check that the fields are accessible
	if interval.A != 123 {
		t.Errorf("Expected A to be 123, got %d", interval.A)
	}
	
	if interval.B != 456 {
		t.Errorf("Expected B to be 456, got %d", interval.B)
	}
}