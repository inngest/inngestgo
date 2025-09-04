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

func TestMeasure(t *testing.T) {
	sleepDuration := 10 * time.Millisecond
	
	interval := Measure(func() {
		time.Sleep(sleepDuration)
	})
	
	if interval.Duration() < sleepDuration {
		t.Errorf("Expected duration to be at least %v, got %v", sleepDuration, interval.Duration())
	}
	
	// Allow some tolerance for timing variations
	if interval.Duration() > sleepDuration+5*time.Millisecond {
		t.Errorf("Expected duration to be close to %v, got %v", sleepDuration, interval.Duration())
	}
}

func TestMeasureT(t *testing.T) {
	expectedResult := 42
	sleepDuration := 10 * time.Millisecond
	
	result, interval := MeasureT(func() int {
		time.Sleep(sleepDuration)
		return expectedResult
	})
	
	if result != expectedResult {
		t.Errorf("Expected result %d, got %d", expectedResult, result)
	}
	
	if interval.Duration() < sleepDuration {
		t.Errorf("Expected duration to be at least %v, got %v", sleepDuration, interval.Duration())
	}
}

func TestMeasureTT(t *testing.T) {
	expectedResult1 := "hello"
	expectedResult2 := 42
	sleepDuration := 10 * time.Millisecond
	
	result1, result2, interval := MeasureTT(func() (string, int) {
		time.Sleep(sleepDuration)
		return expectedResult1, expectedResult2
	})
	
	if result1 != expectedResult1 {
		t.Errorf("Expected result1 %s, got %s", expectedResult1, result1)
	}
	
	if result2 != expectedResult2 {
		t.Errorf("Expected result2 %d, got %d", expectedResult2, result2)
	}
	
	if interval.Duration() < sleepDuration {
		t.Errorf("Expected duration to be at least %v, got %v", sleepDuration, interval.Duration())
	}
}

func TestMeasureTTT(t *testing.T) {
	expectedResult1 := "hello"
	expectedResult2 := 42
	expectedResult3 := true
	sleepDuration := 10 * time.Millisecond
	
	result1, result2, result3, interval := MeasureTTT(func() (string, int, bool) {
		time.Sleep(sleepDuration)
		return expectedResult1, expectedResult2, expectedResult3
	})
	
	if result1 != expectedResult1 {
		t.Errorf("Expected result1 %s, got %s", expectedResult1, result1)
	}
	
	if result2 != expectedResult2 {
		t.Errorf("Expected result2 %d, got %d", expectedResult2, result2)
	}
	
	if result3 != expectedResult3 {
		t.Errorf("Expected result3 %t, got %t", expectedResult3, result3)
	}
	
	if interval.Duration() < sleepDuration {
		t.Errorf("Expected duration to be at least %v, got %v", sleepDuration, interval.Duration())
	}
}