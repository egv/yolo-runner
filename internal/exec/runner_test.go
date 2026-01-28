package exec

import (
	"testing"
	"time"
)

// TestFormatElapsed tests the formatElapsed function with various durations
func TestFormatElapsed(t *testing.T) {
	testCases := []struct {
		name     string
		duration time.Duration
		expected string
	}{
		{
			name:     "less than millisecond",
			duration: 500 * time.Microsecond,
			expected: "0ms",
		},
		{
			name:     "exactly 1 millisecond",
			duration: 1 * time.Millisecond,
			expected: "1ms",
		},
		{
			name:     "10 milliseconds",
			duration: 10 * time.Millisecond,
			expected: "10ms",
		},
		{
			name:     "100 milliseconds",
			duration: 100 * time.Millisecond,
			expected: "100ms",
		},
		{
			name:     "1 second",
			duration: 1 * time.Second,
			expected: "1s",
		},
		{
			name:     "5 seconds",
			duration: 5 * time.Second,
			expected: "5s",
		},
		{
			name:     "1 minute",
			duration: 1 * time.Minute,
			expected: "1m0s",
		},
		{
			name:     "5 minutes 30 seconds",
			duration: 5*time.Minute + 30*time.Second,
			expected: "5m30s",
		},
		{
			name:     "1 hour",
			duration: 1 * time.Hour,
			expected: "1h0m0s",
		},
		{
			name:     "1 hour 30 minutes 15 seconds",
			duration: 1*time.Hour + 30*time.Minute + 15*time.Second,
			expected: "1h30m15s",
		},
		{
			name:     "zero duration",
			duration: 0,
			expected: "0ms",
		},
		{
			name:     "with microseconds",
			duration: 1500 * time.Microsecond,
			expected: "2ms",
		},
		{
			name:     "with nanoseconds",
			duration: 123456789 * time.Nanosecond,
			expected: "123ms",
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			got := formatElapsed(tc.duration)
			if got != tc.expected {
				t.Errorf("formatElapsed(%v) = %q, want %q", tc.duration, got, tc.expected)
			}
		})
	}
}

// TestFormatElapsedPrecision tests that formatElapsed rounds to milliseconds
func TestFormatElapsedPrecision(t *testing.T) {
	testCases := []struct {
		name     string
		duration time.Duration
		expected string
	}{
		{
			name:     "rounds down",
			duration: 1234 * time.Microsecond,
			expected: "1ms",
		},
		{
			name:     "rounds up",
			duration: 1500 * time.Microsecond,
			expected: "2ms",
		},
		{
			name:     "rounds up near boundary",
			duration: 1999 * time.Microsecond,
			expected: "2ms",
		},
		{
			name:     "exact millisecond",
			duration: 2000 * time.Microsecond,
			expected: "2ms",
		},
		{
			name:     "with sub-millisecond precision",
			duration: 1*time.Millisecond + 500*time.Microsecond,
			expected: "2ms",
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			got := formatElapsed(tc.duration)
			if got != tc.expected {
				t.Errorf("formatElapsed(%v) = %q, want %q", tc.duration, got, tc.expected)
			}
		})
	}
}
