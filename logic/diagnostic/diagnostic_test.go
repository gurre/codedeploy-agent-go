package diagnostic

import (
	"errors"
	"fmt"
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

// TestScriptErrorSatisfiesError verifies that *ScriptError implements the error
// interface and that Error() returns the Message field. This is the contract
// that fmt.Errorf wrapping and errors.As depend on.
func TestScriptErrorSatisfiesError(t *testing.T) {
	se := &ScriptError{
		Code:       ScriptFailed,
		ScriptName: "deploy.sh",
		Message:    "exit code 1",
		Log:        "stdout output",
	}
	var err error = se
	if err.Error() != "exit code 1" {
		t.Errorf("Error() = %q, want %q", err.Error(), "exit code 1")
	}
}

// TestScriptErrorExtractableViaErrorsAs verifies that a *ScriptError wrapped
// with fmt.Errorf is extractable via errors.As. The poller uses this pattern
// to detect script failures and build rich diagnostics.
func TestScriptErrorExtractableViaErrorsAs(t *testing.T) {
	se := &ScriptError{
		Code:       ScriptTimedOut,
		ScriptName: "start.sh",
		Message:    "timed out",
		Log:        "partial output",
	}
	wrapped := fmt.Errorf("executor: %w", se)

	var extracted *ScriptError
	if !errors.As(wrapped, &extracted) {
		t.Fatal("errors.As failed to extract *ScriptError from wrapped error")
	}
	if extracted.Code != ScriptTimedOut {
		t.Errorf("Code = %d, want %d", extracted.Code, ScriptTimedOut)
	}
	if extracted.ScriptName != "start.sh" {
		t.Errorf("ScriptName = %q, want %q", extracted.ScriptName, "start.sh")
	}
}

// TestBuildFromScriptErrRoundTrip ensures all four diagnostic fields survive
// JSON serialization via BuildFromScriptErr. This is the path used when the
// poller detects a ScriptError and sends it to PutHostCommandComplete.
func TestBuildFromScriptErrRoundTrip(t *testing.T) {
	se := &ScriptError{
		Code:       ScriptFailed,
		ScriptName: "deploy.sh",
		Message:    "exit code 1",
		Log:        "[stdout]hello\n",
	}
	payload := BuildFromScriptErr(se)
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
	if d.Message != "exit code 1" {
		t.Errorf("Message = %q", d.Message)
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

// TestBuildFromScriptErrZeroValues verifies that a ScriptError with empty
// string fields and zero Code serializes without panics and produces valid JSON.
// This guards against nil-like edge cases when a ScriptError is constructed
// with minimal fields (e.g. only Code set).
func TestBuildFromScriptErrZeroValues(t *testing.T) {
	se := &ScriptError{Code: Succeeded}
	payload := BuildFromScriptErr(se)
	var d Diagnostic
	if err := json.Unmarshal([]byte(payload), &d); err != nil {
		t.Fatalf("JSON unmarshal failed: %v", err)
	}
	if d.ErrorCode != Succeeded {
		t.Errorf("ErrorCode = %d, want %d", d.ErrorCode, Succeeded)
	}
	if d.ScriptName != "" {
		t.Errorf("ScriptName should be empty, got %q", d.ScriptName)
	}
	if d.Message != "" {
		t.Errorf("Message should be empty, got %q", d.Message)
	}
	if d.Log != "" {
		t.Errorf("Log should be empty, got %q", d.Log)
	}
}

// TestScriptErrorMessageUsedByErrorInterface verifies that Error() returns
// exactly the Message field, not a formatted version. This matters because
// the poller logs err.Error() and separately extracts fields via errors.As —
// if Error() appended the script name, the log line would be redundant.
func TestScriptErrorMessageUsedByErrorInterface(t *testing.T) {
	se := &ScriptError{
		Code:       ScriptFailed,
		ScriptName: "deploy.sh",
		Message:    "exit code 1",
		Log:        "some output",
	}
	if se.Error() != se.Message {
		t.Errorf("Error() = %q, want exactly Message %q", se.Error(), se.Message)
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
