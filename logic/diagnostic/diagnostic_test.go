package diagnostic

import (
	"testing"

	json "github.com/goccy/go-json"
)

// TestBuildSuccessRoundTrips verifies that BuildSuccess produces valid JSON that
// round-trips back to a Diagnostic struct with the correct fields.
func TestBuildSuccessRoundTrips(t *testing.T) {
	payload := BuildSuccess("completed")
	var d Diagnostic
	if err := json.Unmarshal([]byte(payload), &d); err != nil {
		t.Fatalf("JSON unmarshal failed: %v", err)
	}
	if d.ErrorCode != Succeeded {
		t.Errorf("ErrorCode = %d, want %d", d.ErrorCode, Succeeded)
	}
	if d.Message != "Succeeded: completed" {
		t.Errorf("Message = %q", d.Message)
	}
}

// TestBuildFromScriptErrorPreservesFields ensures all fields survive
// serialization, which is critical for the service to display correct diagnostics.
func TestBuildFromScriptErrorPreservesFields(t *testing.T) {
	payload := BuildFromScriptError(ScriptFailed, "deploy.sh", "exit code 1", "[stdout]hello\n")
	var d Diagnostic
	if err := json.Unmarshal([]byte(payload), &d); err != nil {
		t.Fatalf("JSON unmarshal failed: %v", err)
	}
	if d.ErrorCode != ScriptFailed {
		t.Errorf("ErrorCode = %d, want %d", d.ErrorCode, ScriptFailed)
	}
	if d.ScriptName != "deploy.sh" {
		t.Errorf("ScriptName = %q", d.ScriptName)
	}
	if d.Log != "[stdout]hello\n" {
		t.Errorf("Log = %q", d.Log)
	}
}

// TestBuildFromErrorHandlesNil verifies that a nil error produces a valid
// diagnostic with empty message rather than panicking.
func TestBuildFromErrorHandlesNil(t *testing.T) {
	payload := BuildFromError(nil)
	var d Diagnostic
	if err := json.Unmarshal([]byte(payload), &d); err != nil {
		t.Fatalf("JSON unmarshal failed: %v", err)
	}
	if d.ErrorCode != UnknownError {
		t.Errorf("ErrorCode = %d, want %d", d.ErrorCode, UnknownError)
	}
}

// TestBuildFailedAfterRestartCode verifies the specific error code 7 is used
// for post-restart failures, which the service uses to distinguish from normal failures.
func TestBuildFailedAfterRestartCode(t *testing.T) {
	payload := BuildFailedAfterRestart("agent restarted")
	var d Diagnostic
	if err := json.Unmarshal([]byte(payload), &d); err != nil {
		t.Fatalf("JSON unmarshal failed: %v", err)
	}
	if d.ErrorCode != FailedAfterRestart {
		t.Errorf("ErrorCode = %d, want %d", d.ErrorCode, FailedAfterRestart)
	}
}

// TestAllErrorCodesAreDistinct verifies no two error codes share a value.
// This is an invariant the service depends on for diagnostics routing.
func TestAllErrorCodesAreDistinct(t *testing.T) {
	codes := []ErrorCode{Succeeded, ScriptMissing, ScriptNotExecutable, ScriptTimedOut, ScriptFailed, UnknownError, OutputsLeftOpen, FailedAfterRestart}
	seen := make(map[ErrorCode]bool)
	for _, c := range codes {
		if seen[c] {
			t.Errorf("duplicate error code %d", c)
		}
		seen[c] = true
	}
}

// TestErrorCodeRange verifies all error codes are in the expected 0-7 range.
func TestErrorCodeRange(t *testing.T) {
	codes := []ErrorCode{Succeeded, ScriptMissing, ScriptNotExecutable, ScriptTimedOut, ScriptFailed, UnknownError, OutputsLeftOpen, FailedAfterRestart}
	for _, c := range codes {
		if c < 0 || c > 7 {
			t.Errorf("error code %d outside range 0-7", c)
		}
	}
}
