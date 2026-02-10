// Package diagnostic defines error codes and JSON diagnostic payloads
// for reporting deployment lifecycle event results to the CodeDeploy service.
package diagnostic

import (
	json "github.com/goccy/go-json"
)

// ErrorCode represents the outcome of a lifecycle event script execution.
type ErrorCode int

const (
	Succeeded           ErrorCode = 0
	ScriptMissing       ErrorCode = 1
	ScriptNotExecutable ErrorCode = 2
	ScriptTimedOut      ErrorCode = 3
	ScriptFailed        ErrorCode = 4
	UnknownError        ErrorCode = 5
	OutputsLeftOpen     ErrorCode = 6
	FailedAfterRestart  ErrorCode = 7
)

// Diagnostic holds the result of a lifecycle event for reporting to CodeDeploy.
type Diagnostic struct {
	ErrorCode  ErrorCode `json:"error_code"`
	ScriptName string    `json:"script_name"`
	Message    string    `json:"message"`
	Log        string    `json:"log"`
}

// Build creates a Diagnostic and marshals it to JSON. This is the format
// expected by PutHostCommandComplete's diagnostics envelope payload.
//
//	payload := diagnostic.Build(diagnostic.Succeeded, "", "Succeeded", "")
func Build(code ErrorCode, scriptName, message, log string) string {
	d := Diagnostic{
		ErrorCode:  code,
		ScriptName: scriptName,
		Message:    message,
		Log:        log,
	}
	data, err := json.Marshal(d)
	if err != nil {
		// Fallback to minimal JSON on marshal failure (should not happen)
		return `{"error_code":5,"script_name":"","message":"marshal error","log":""}`
	}
	return string(data)
}

// BuildSuccess creates a success diagnostic payload.
//
//	payload := diagnostic.BuildSuccess("Succeeded: ")
func BuildSuccess(message string) string {
	return Build(Succeeded, "", "Succeeded: "+message, "")
}

// BuildFromError creates an error diagnostic from a generic error.
//
//	payload := diagnostic.BuildFromError(err)
func BuildFromError(err error) string {
	msg := ""
	if err != nil {
		msg = err.Error()
	}
	return Build(UnknownError, "", msg, "")
}

// BuildFromScriptError creates a diagnostic from a script execution error,
// preserving the error code, script name, and captured log output.
//
//	payload := diagnostic.BuildFromScriptError(ScriptFailed, "deploy.sh", "exit 1", logOutput)
func BuildFromScriptError(code ErrorCode, scriptName, message, log string) string {
	return Build(code, scriptName, message, log)
}

// BuildFailedAfterRestart creates a diagnostic for lifecycle events that were
// in-progress when the agent restarted. The event is failed with code 7.
//
//	payload := diagnostic.BuildFailedAfterRestart("Agent restarted during execution")
func BuildFailedAfterRestart(message string) string {
	return Build(FailedAfterRestart, "", "Failed: "+message, "")
}
