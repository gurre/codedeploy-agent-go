// Package backoff provides jittered exponential backoff computation for retry loops.
//
// The package implements the "equal jitter" strategy from the AWS Architecture Blog
// (see: "Exponential Backoff and Jitter"). This approach splits the computed delay
// in half: one half is deterministic floor, the other half is uniformly random.
// Compared to full jitter (uniform over [0, delay]), equal jitter guarantees forward
// progress — the minimum wait is always 50% of the computed delay — while still
// decorrelating concurrent retriers to prevent thundering herd.
//
// Design constraints:
//   - Pure computation, no IO or side effects (logic layer).
//   - Zero allocations on the hot path (called on every poll error).
//   - Negative and overflow-prone inputs are handled defensively since Duration is public.
//
// Used by orchestration/poller to determine retry delays after poll failures.
// Throttle detection is handled separately by the caller; this package only provides
// the ThrottleDelay constant for that case.
package backoff

import (
	"math/rand/v2"
	"time"
)

// ThrottleDelay is the fixed wait duration when a throttle response is detected.
// Matches the Ruby CodeDeploy agent's 60-second throttle wait. A fixed delay is
// used instead of exponential backoff because throttle responses already signal
// the server's desired pacing — escalating further would over-penalize the client.
//
//	if svcErr.IsThrottle() {
//	    time.Sleep(backoff.ThrottleDelay)
//	}
const ThrottleDelay = 60 * time.Second

// maxShift caps the bit-shift exponent to prevent overflow. With a 30s base,
// shift=10 yields 30s * 1024 = 30720s (~8.5h), well beyond any practical maxDelay.
// The clamp to maxDelay brings this back into range. Without maxShift, a count of
// 63+ would overflow time.Duration (int64 nanoseconds).
const maxShift = 10

// Duration computes a jittered exponential backoff delay.
//
// The formula proceeds in three steps:
//  1. Exponential: delay = baseDelay << min(count, maxShift)
//  2. Clamp:       delay = min(delay, maxDelay)
//  3. Equal jitter: return uniform sample from [delay/2, delay]
//
// The equal jitter guarantee means the caller always waits at least half the
// computed delay. This prevents the pathological case in full jitter where a
// retrier rolls near-zero and immediately retries, negating the backoff.
//
// Negative count values are treated as zero (defensive guard for public API).
// Count values above maxShift are clamped to prevent bit-shift overflow.
//
//	backoff.Duration(0, 30*time.Second, 90*time.Second) // [15s, 30s]
//	backoff.Duration(1, 30*time.Second, 90*time.Second) // [30s, 60s]
//	backoff.Duration(2, 30*time.Second, 90*time.Second) // [45s, 90s] (clamped)
func Duration(count int, baseDelay, maxDelay time.Duration) time.Duration {
	// Guard: public API accepts int, so negative values are possible.
	shift := count
	if shift < 0 {
		shift = 0
	}
	if shift > maxShift {
		shift = maxShift
	}

	// Exponential growth via bit shift: baseDelay * 2^shift.
	delay := baseDelay << shift
	if delay > maxDelay {
		delay = maxDelay
	}

	// Equal jitter: deterministic floor (delay/2) + uniform random in [0, delay/2].
	// Int64N(half+1) makes the upper bound inclusive so the full delay is reachable.
	half := delay / 2
	jitter := time.Duration(rand.Int64N(int64(half + 1)))
	return half + jitter
}
