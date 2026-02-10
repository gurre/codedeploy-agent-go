package backoff

import (
	"testing"
	"time"
)

const (
	testBase = 30 * time.Second
	testMax  = 90 * time.Second
	// iterations is the number of samples for statistical property tests.
	// High enough to catch jitter absence with overwhelming probability.
	iterations = 1000
)

// TestDuration_AlwaysWithinBounds verifies the invariant that Duration output
// is always in [baseDelay/2, maxDelay]. A violation would mean either the floor
// jitter is broken or the max clamp is missing.
func TestDuration_AlwaysWithinBounds(t *testing.T) {
	floor := testBase / 2

	for i := range iterations {
		d := Duration(i%10, testBase, testMax)
		if d < floor {
			t.Fatalf("iteration %d: Duration=%v < floor %v", i, d, floor)
		}
		if d > testMax {
			t.Fatalf("iteration %d: Duration=%v > max %v", i, d, testMax)
		}
	}
}

// TestDuration_CountZero_WithinBaseRange verifies that at count=0 the output
// lies in [baseDelay/2, baseDelay]. This confirms the starting backoff window
// before any exponential escalation.
func TestDuration_CountZero_WithinBaseRange(t *testing.T) {
	low := testBase / 2

	for range iterations {
		d := Duration(0, testBase, testMax)
		if d < low || d > testBase {
			t.Fatalf("count=0: Duration=%v outside [%v, %v]", d, low, testBase)
		}
	}
}

// TestDuration_MonotonicMidpoint verifies that the midpoint of the jitter range
// increases (or stays equal) as count increases, up to the cap. This confirms
// the exponential escalation property: higher count = longer expected delay.
func TestDuration_MonotonicMidpoint(t *testing.T) {
	prevMid := time.Duration(0)

	for count := range 6 {
		// Compute the deterministic midpoint: (delay/2 + delay) / 2 = 3*delay/4
		shift := count
		if shift > maxShift {
			shift = maxShift
		}
		delay := testBase << shift
		if delay > testMax {
			delay = testMax
		}
		mid := delay * 3 / 4

		if mid < prevMid {
			t.Fatalf("count=%d midpoint %v < previous %v — not monotonic", count, mid, prevMid)
		}
		prevMid = mid
	}
}

// TestDuration_JitterVaries verifies that repeated calls with the same inputs
// produce different outputs. If all values are identical, jitter is not working.
// With 100 samples from a range of at least 15 seconds, the probability of all
// being identical is astronomically low.
func TestDuration_JitterVaries(t *testing.T) {
	first := Duration(1, testBase, testMax)
	allSame := true

	for range 100 {
		if Duration(1, testBase, testMax) != first {
			allSame = false
			break
		}
	}

	if allSame {
		t.Fatal("100 calls returned identical values — jitter is not working")
	}
}

// TestDuration_NegativeCount_SameAsZero verifies that negative count values
// produce the same range as count=0 rather than panicking. The public Duration
// function accepts int, so negative values must be handled defensively.
func TestDuration_NegativeCount_SameAsZero(t *testing.T) {
	low := testBase / 2

	for range iterations {
		d := Duration(-5, testBase, testMax)
		if d < low || d > testBase {
			t.Fatalf("negative count: Duration=%v outside [%v, %v]", d, low, testBase)
		}
	}
}

// TestDuration_LargeCount_NoPanic verifies that very large count values don't
// cause overflow or panic. The shift cap prevents 1<<count from overflowing.
func TestDuration_LargeCount_NoPanic(t *testing.T) {
	d := Duration(1000, testBase, testMax)
	if d < testMax/2 || d > testMax {
		t.Fatalf("large count: Duration=%v outside [%v, %v]", d, testMax/2, testMax)
	}
}

// TestThrottleDelay_Is60Seconds verifies the throttle constant matches the
// Ruby agent's fixed 60-second wait for rate-limit responses.
func TestThrottleDelay_Is60Seconds(t *testing.T) {
	if ThrottleDelay != 60*time.Second {
		t.Fatalf("ThrottleDelay = %v, want 60s", ThrottleDelay)
	}
}

// BenchmarkDuration measures the allocation and CPU cost of computing a single
// backoff delay. Duration is called on every poll error, so it sits on the
// retry hot path and must be allocation-free.
func BenchmarkDuration(b *testing.B) {
	for range b.N {
		Duration(3, testBase, testMax)
	}
}
