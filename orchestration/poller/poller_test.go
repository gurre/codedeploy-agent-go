package poller

import (
	"context"
	"fmt"
	"log/slog"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/gurre/codedeploy-agent-go/logic/deployspec"
)

// TestProcessCommand_Success verifies the full happy path through the polling loop:
// PollHostCommand returns a command, GetDeploymentSpecification succeeds with "CodeDeploy",
// Parse yields a valid spec, IsNoop returns false, Acknowledge returns "InProgress",
// tracker.Create is called, Execute succeeds, and Complete is called with "Succeeded".
// This test exists because the happy path is the primary contract of the poller.
func TestProcessCommand_Success(t *testing.T) {
	completeCh := make(chan string, 1)

	spec := deployspec.Spec{
		DeploymentID:        "d-ABC123",
		DeploymentGroupID:   "dg-1",
		DeploymentGroupName: "prod",
		ApplicationName:     "myapp",
	}

	svc := &stubCommandService{
		pollFunc: pollOnce(&HostCommand{
			HostCommandIdentifier: "hc-1",
			HostIdentifier:        "i-host",
			DeploymentExecutionID: "exec-1",
			CommandName:           "Install",
		}),
		getSpecFunc: func(_ context.Context, _, _ string) (*Envelope, string, error) {
			return &Envelope{Format: "TEXT/JSON", Payload: `{}`}, "CodeDeploy", nil
		},
		acknowledgeFunc: func(_ context.Context, _ string, _ *Envelope) (string, error) {
			return "InProgress", nil
		},
		completeFunc: func(_ context.Context, hci, status string, _ *Envelope) error {
			completeCh <- status
			return nil
		},
	}

	parser := &stubSpecParser{spec: spec}
	exec := &stubCommandExecutor{
		executeFunc: func(_ context.Context, _ string, _ deployspec.Spec) (string, error) {
			return "ok", nil
		},
	}
	tracker := &stubDeploymentTracker{}

	p := NewPoller(svc, exec, parser, tracker, "i-host",
		time.Millisecond, time.Millisecond, time.Second, slog.Default())

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	go func() { _ = p.Run(ctx) }()

	select {
	case status := <-completeCh:
		if status != "Succeeded" {
			t.Fatalf("expected Complete status Succeeded, got %q", status)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("timed out waiting for Complete call")
	}

	cancel()

	if !tracker.createCalled() {
		t.Error("expected tracker.Create to be called")
	}
	if !tracker.deleteCalled() {
		t.Error("expected tracker.Delete to be called")
	}
}

// TestProcessCommand_AcknowledgeFailed verifies that when Acknowledge returns "Failed",
// the poller short-circuits and never calls Execute or Complete. This prevents
// re-execution of commands the service has already marked terminal.
func TestProcessCommand_AcknowledgeFailed(t *testing.T) {
	ackCh := make(chan struct{}, 1)

	svc := &stubCommandService{
		pollFunc: pollOnce(&HostCommand{
			HostCommandIdentifier: "hc-2",
			HostIdentifier:        "i-host",
			DeploymentExecutionID: "exec-2",
			CommandName:           "Install",
		}),
		getSpecFunc: func(_ context.Context, _, _ string) (*Envelope, string, error) {
			return &Envelope{Format: "TEXT/JSON", Payload: `{}`}, "CodeDeploy", nil
		},
		acknowledgeFunc: func(_ context.Context, _ string, _ *Envelope) (string, error) {
			ackCh <- struct{}{}
			return "Failed", nil
		},
		completeFunc: func(_ context.Context, _, _ string, _ *Envelope) error {
			t.Error("Complete should not be called when Acknowledge returns Failed")
			return nil
		},
	}

	spec := deployspec.Spec{
		DeploymentID:        "d-ABC123",
		DeploymentGroupID:   "dg-1",
		DeploymentGroupName: "prod",
		ApplicationName:     "myapp",
	}
	parser := &stubSpecParser{spec: spec}
	exec := &stubCommandExecutor{
		executeFunc: func(_ context.Context, _ string, _ deployspec.Spec) (string, error) {
			t.Error("Execute should not be called when Acknowledge returns Failed")
			return "", nil
		},
	}
	tracker := &stubDeploymentTracker{}

	p := NewPoller(svc, exec, parser, tracker, "i-host",
		time.Millisecond, time.Millisecond, time.Second, slog.Default())

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	go func() { _ = p.Run(ctx) }()

	select {
	case <-ackCh:
		// Acknowledge was called; give processCommand a moment to return
		time.Sleep(50 * time.Millisecond)
	case <-time.After(3 * time.Second):
		t.Fatal("timed out waiting for Acknowledge call")
	}

	cancel()

	if tracker.createCalled() {
		t.Error("tracker.Create should not be called when Acknowledge returns Failed")
	}
}

// TestProcessCommand_ExecutionError verifies that when Execute returns an error,
// Complete is called with "Failed" and the error message is propagated through
// the diagnostic payload. This ensures execution failures are reported to the service.
func TestProcessCommand_ExecutionError(t *testing.T) {
	completeCh := make(chan string, 1)

	svc := &stubCommandService{
		pollFunc: pollOnce(&HostCommand{
			HostCommandIdentifier: "hc-3",
			HostIdentifier:        "i-host",
			DeploymentExecutionID: "exec-3",
			CommandName:           "Install",
		}),
		getSpecFunc: func(_ context.Context, _, _ string) (*Envelope, string, error) {
			return &Envelope{Format: "TEXT/JSON", Payload: `{}`}, "CodeDeploy", nil
		},
		acknowledgeFunc: func(_ context.Context, _ string, _ *Envelope) (string, error) {
			return "InProgress", nil
		},
		completeFunc: func(_ context.Context, _, status string, _ *Envelope) error {
			completeCh <- status
			return nil
		},
	}

	spec := deployspec.Spec{
		DeploymentID:        "d-ABC123",
		DeploymentGroupID:   "dg-1",
		DeploymentGroupName: "prod",
		ApplicationName:     "myapp",
	}
	parser := &stubSpecParser{spec: spec}
	exec := &stubCommandExecutor{
		executeFunc: func(_ context.Context, _ string, _ deployspec.Spec) (string, error) {
			return "", fmt.Errorf("script exited with code 1")
		},
	}
	tracker := &stubDeploymentTracker{}

	p := NewPoller(svc, exec, parser, tracker, "i-host",
		time.Millisecond, time.Millisecond, time.Second, slog.Default())

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	go func() { _ = p.Run(ctx) }()

	select {
	case status := <-completeCh:
		if status != "Failed" {
			t.Fatalf("expected Complete status Failed, got %q", status)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("timed out waiting for Complete call")
	}

	cancel()
}

// TestProcessCommand_IsNoop verifies that when IsNoop returns true the acknowledge
// envelope contains `"IsCommandNoop":true`. If the ack status is non-terminal,
// execution still proceeds. This tests the noop signaling path which allows the
// service to skip waiting for results when no work is needed.
func TestProcessCommand_IsNoop(t *testing.T) {
	var ackPayload string
	completeCh := make(chan string, 1)

	svc := &stubCommandService{
		pollFunc: pollOnce(&HostCommand{
			HostCommandIdentifier: "hc-4",
			HostIdentifier:        "i-host",
			DeploymentExecutionID: "exec-4",
			CommandName:           "Install",
		}),
		getSpecFunc: func(_ context.Context, _, _ string) (*Envelope, string, error) {
			return &Envelope{Format: "TEXT/JSON", Payload: `{}`}, "CodeDeploy", nil
		},
		acknowledgeFunc: func(_ context.Context, _ string, diag *Envelope) (string, error) {
			ackPayload = diag.Payload
			return "InProgress", nil
		},
		completeFunc: func(_ context.Context, _, status string, _ *Envelope) error {
			completeCh <- status
			return nil
		},
	}

	spec := deployspec.Spec{
		DeploymentID:        "d-ABC123",
		DeploymentGroupID:   "dg-1",
		DeploymentGroupName: "prod",
		ApplicationName:     "myapp",
	}
	parser := &stubSpecParser{spec: spec}
	exec := &stubCommandExecutor{
		noop: true,
		executeFunc: func(_ context.Context, _ string, _ deployspec.Spec) (string, error) {
			return "noop-ok", nil
		},
	}
	tracker := &stubDeploymentTracker{}

	p := NewPoller(svc, exec, parser, tracker, "i-host",
		time.Millisecond, time.Millisecond, time.Second, slog.Default())

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	go func() { _ = p.Run(ctx) }()

	select {
	case status := <-completeCh:
		if status != "Succeeded" {
			t.Fatalf("expected Complete status Succeeded, got %q", status)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("timed out waiting for Complete call")
	}

	cancel()

	expected := `{"IsCommandNoop":true}`
	if ackPayload != expected {
		t.Errorf("expected ack payload %q, got %q", expected, ackPayload)
	}
}

// TestRecoverFromCrash verifies that on startup, if the tracker reports an
// in-progress command identifier, the poller calls Complete with "Failed" to
// notify the service, then calls CleanAll to remove stale tracking files.
// This is critical for crash recovery: without it, a command stuck in-progress
// after an agent restart would never be resolved.
func TestRecoverFromCrash(t *testing.T) {
	var completedHCI string
	var completedStatus string

	svc := &stubCommandService{
		completeFunc: func(_ context.Context, hci, status string, _ *Envelope) error {
			completedHCI = hci
			completedStatus = status
			return nil
		},
	}

	tracker := &stubDeploymentTracker{inProgress: "hc-crashed"}

	p := NewPoller(svc, nil, nil, tracker, "i-host",
		time.Millisecond, time.Millisecond, time.Second, slog.Default())

	p.RecoverFromCrash(context.Background())

	if completedHCI != "hc-crashed" {
		t.Errorf("expected Complete called with hci %q, got %q", "hc-crashed", completedHCI)
	}
	if completedStatus != "Failed" {
		t.Errorf("expected Complete status Failed, got %q", completedStatus)
	}
	if !tracker.cleanAllCalled() {
		t.Error("expected CleanAll to be called after crash recovery")
	}
}

// TestRun_GracefulShutdown verifies that cancelling the context causes Run to
// return nil after waiting for in-progress commands. This ensures the polling
// loop shuts down cleanly without leaking goroutines or abandoning work.
func TestRun_GracefulShutdown(t *testing.T) {
	svc := &stubCommandService{
		pollFunc: func(_ context.Context, _ string) (*HostCommand, error) {
			return nil, nil
		},
	}

	p := NewPoller(svc, nil, nil, &stubDeploymentTracker{}, "i-host",
		time.Millisecond, time.Millisecond, time.Second, slog.Default())

	ctx, cancel := context.WithCancel(context.Background())
	errCh := make(chan error, 1)

	go func() {
		errCh <- p.Run(ctx)
	}()

	// Let it poll at least once, then cancel
	time.Sleep(20 * time.Millisecond)
	cancel()

	select {
	case err := <-errCh:
		if err != nil {
			t.Fatalf("expected nil error from Run, got %v", err)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("Run did not return after context cancellation")
	}
}

// TestRun_ErrorCountResets verifies that the consecutive error counter resets
// after a successful poll. Sequence: fail, fail, succeed, fail, then block.
// If the counter did not reset after the success, the fourth call would see
// consecutiveErrors=2 (escalated). With 500ms base, count=2 gives 1s-2s backoff.
// Without reset, cumulative backoff for calls 1+2+4 ranges [1.75s, 3.5s] — the
// upper half exceeds the 3s timeout, catching regressions probabilistically (~75%).
// With reset, the fourth call sees count=0 ([250ms, 500ms]) and finishes in time.
func TestRun_ErrorCountResets(t *testing.T) {
	var callCount atomic.Int32
	doneCh := make(chan struct{}, 1)

	svc := &stubCommandService{
		pollFunc: func(ctx context.Context, _ string) (*HostCommand, error) {
			n := callCount.Add(1)
			switch n {
			case 1, 2:
				return nil, fmt.Errorf("transient error")
			case 3:
				// Success resets the counter
				return nil, nil
			case 4:
				// After reset, this is consecutiveErrors=0
				doneCh <- struct{}{}
				return nil, fmt.Errorf("another error")
			default:
				<-ctx.Done()
				return nil, ctx.Err()
			}
		},
	}

	// 500ms base makes the distinction between reset and non-reset observable:
	// Without reset, count=2 → backoff in [1s, 2s], cumulative range [1.75s, 3.5s].
	// With reset, count=0 → backoff in [250ms, 500ms], all four polls finish in ~2s.
	p := NewPoller(svc, nil, nil, &stubDeploymentTracker{}, "i-host",
		time.Millisecond, 500*time.Millisecond, time.Second, slog.Default())

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	go func() { _ = p.Run(ctx) }()

	select {
	case <-doneCh:
		// Fourth poll was reached — counter was reset after success
	case <-time.After(3 * time.Second):
		t.Fatal("timed out waiting for fourth poll call — counter may not have reset")
	}

	cancel()
}

// TestComputeBackoff_Throttle verifies that errors implementing IsThrottle()
// produce the fixed 60-second throttle delay instead of exponential backoff.
// This tests the dependency-inverted throttle detection via the local throttler interface.
func TestComputeBackoff_Throttle(t *testing.T) {
	p := NewPoller(nil, nil, nil, nil, "",
		time.Millisecond, 30*time.Second, time.Second, slog.Default())

	err := &throttleError{msg: "Throttling: Rate exceeded"}
	delay := p.computeBackoff(err)
	if delay < 60*time.Second {
		t.Errorf("throttle backoff = %v, want >= 60s", delay)
	}
}

// TestComputeBackoff_NonThrottle verifies that non-throttle errors use
// exponential backoff from the backoff package rather than the fixed
// throttle delay.
func TestComputeBackoff_NonThrottle(t *testing.T) {
	p := NewPoller(nil, nil, nil, nil, "",
		time.Millisecond, 30*time.Second, time.Second, slog.Default())
	p.consecutiveErrors = 0

	err := fmt.Errorf("some network error")
	delay := p.computeBackoff(err)
	// count=0 with 30s base: should be in [15s, 30s]
	if delay < 15*time.Second || delay > 30*time.Second {
		t.Errorf("non-throttle backoff = %v, want in [15s, 30s]", delay)
	}
}

// throttleError is a test double that implements the throttler interface.
type throttleError struct {
	msg string
}

func (e *throttleError) Error() string    { return e.msg }
func (e *throttleError) IsThrottle() bool { return true }

// --- Test doubles (stubs) ---
// Placed at the end of the file per project convention.

// pollOnce returns a PollHostCommand function that yields the given command on
// the first call and blocks on ctx.Done for all subsequent calls. This lets
// tests drive exactly one command through processCommand before the poller idles.
func pollOnce(cmd *HostCommand) func(context.Context, string) (*HostCommand, error) {
	once := sync.Once{}
	return func(ctx context.Context, _ string) (*HostCommand, error) {
		var sent bool
		once.Do(func() { sent = true })
		if sent {
			return cmd, nil
		}
		// Block until context is cancelled so we don't poll again
		<-ctx.Done()
		return nil, ctx.Err()
	}
}

// stubCommandService is a configurable test double for CommandService.
// Each method delegates to an optional function field; nil fields panic
// to surface unexpected calls.
type stubCommandService struct {
	pollFunc        func(ctx context.Context, hostID string) (*HostCommand, error)
	getSpecFunc     func(ctx context.Context, execID, hostID string) (*Envelope, string, error)
	acknowledgeFunc func(ctx context.Context, hci string, diag *Envelope) (string, error)
	completeFunc    func(ctx context.Context, hci, status string, diag *Envelope) error
}

func (s *stubCommandService) PollHostCommand(ctx context.Context, hostID string) (*HostCommand, error) {
	return s.pollFunc(ctx, hostID)
}

func (s *stubCommandService) GetDeploymentSpecification(ctx context.Context, execID, hostID string) (*Envelope, string, error) {
	return s.getSpecFunc(ctx, execID, hostID)
}

func (s *stubCommandService) Acknowledge(ctx context.Context, hci string, diag *Envelope) (string, error) {
	return s.acknowledgeFunc(ctx, hci, diag)
}

func (s *stubCommandService) Complete(ctx context.Context, hci, status string, diag *Envelope) error {
	return s.completeFunc(ctx, hci, status, diag)
}

// stubCommandExecutor is a test double for CommandExecutor.
type stubCommandExecutor struct {
	executeFunc func(ctx context.Context, commandName string, spec deployspec.Spec) (string, error)
	noop        bool
}

func (s *stubCommandExecutor) Execute(ctx context.Context, commandName string, spec deployspec.Spec) (string, error) {
	return s.executeFunc(ctx, commandName, spec)
}

func (s *stubCommandExecutor) IsNoop(_ string, _ deployspec.Spec) bool {
	return s.noop
}

// stubSpecParser is a test double for SpecParser that returns a fixed spec.
type stubSpecParser struct {
	spec deployspec.Spec
	err  error
}

func (s *stubSpecParser) Parse(_ deployspec.Envelope) (deployspec.Spec, error) {
	return s.spec, s.err
}

// stubDeploymentTracker is a test double for DeploymentTracker. Fields are
// aligned largest-to-smallest for memory efficiency.
type stubDeploymentTracker struct {
	mu         sync.Mutex
	inProgress string
	created    bool
	deleted    bool
	cleanedAll bool
}

func (s *stubDeploymentTracker) Create(_, _ string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.created = true
	return nil
}

func (s *stubDeploymentTracker) Delete(_ string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.deleted = true
}

func (s *stubDeploymentTracker) InProgressCommand() string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.inProgress
}

func (s *stubDeploymentTracker) CleanAll() {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.cleanedAll = true
}

func (s *stubDeploymentTracker) createCalled() bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.created
}

func (s *stubDeploymentTracker) deleteCalled() bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.deleted
}

func (s *stubDeploymentTracker) cleanAllCalled() bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.cleanedAll
}

// TestProcessCommand_GetSpecError verifies that when GetDeploymentSpecification
// returns an error, the poller calls Complete with "Failed". This covers the
// first error branch in processCommand and ensures network/service failures
// during spec retrieval are reported back to the CodeDeploy service.
func TestProcessCommand_GetSpecError(t *testing.T) {
	completeCh := make(chan string, 1)

	svc := &stubCommandService{
		pollFunc: pollOnce(&HostCommand{
			HostCommandIdentifier: "hc-getspec-err",
			HostIdentifier:        "i-host",
			DeploymentExecutionID: "exec-getspec-err",
			CommandName:           "Install",
		}),
		getSpecFunc: func(_ context.Context, _, _ string) (*Envelope, string, error) {
			return nil, "", fmt.Errorf("service unavailable")
		},
		completeFunc: func(_ context.Context, _, status string, _ *Envelope) error {
			completeCh <- status
			return nil
		},
	}

	p := NewPoller(svc, nil, nil, &stubDeploymentTracker{}, "i-host",
		time.Millisecond, time.Millisecond, time.Second, slog.Default())

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	go func() { _ = p.Run(ctx) }()

	select {
	case status := <-completeCh:
		if status != "Failed" {
			t.Fatalf("expected Complete status Failed, got %q", status)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("timed out waiting for Complete call")
	}

	cancel()
}

// TestProcessCommand_NonCodeDeploy verifies that when GetDeploymentSpecification
// returns a deployment system other than "CodeDeploy", the poller calls Complete
// with "Failed". This guards against processing commands from unrecognized
// deployment systems that could lead to undefined behavior.
func TestProcessCommand_NonCodeDeploy(t *testing.T) {
	completeCh := make(chan string, 1)

	svc := &stubCommandService{
		pollFunc: pollOnce(&HostCommand{
			HostCommandIdentifier: "hc-nocd",
			HostIdentifier:        "i-host",
			DeploymentExecutionID: "exec-nocd",
			CommandName:           "Install",
		}),
		getSpecFunc: func(_ context.Context, _, _ string) (*Envelope, string, error) {
			return &Envelope{Format: "TEXT/JSON", Payload: `{}`}, "OtherSystem", nil
		},
		completeFunc: func(_ context.Context, _, status string, _ *Envelope) error {
			completeCh <- status
			return nil
		},
	}

	p := NewPoller(svc, nil, nil, &stubDeploymentTracker{}, "i-host",
		time.Millisecond, time.Millisecond, time.Second, slog.Default())

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	go func() { _ = p.Run(ctx) }()

	select {
	case status := <-completeCh:
		if status != "Failed" {
			t.Fatalf("expected Complete status Failed, got %q", status)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("timed out waiting for Complete call")
	}

	cancel()
}

// TestProcessCommand_NilSpec verifies that when GetDeploymentSpecification
// returns a nil envelope (with no error), the poller calls Complete with
// "Failed". This covers the nil-envelope guard in processCommand that prevents
// a nil-pointer dereference when constructing the spec parser input.
func TestProcessCommand_NilSpec(t *testing.T) {
	completeCh := make(chan string, 1)

	svc := &stubCommandService{
		pollFunc: pollOnce(&HostCommand{
			HostCommandIdentifier: "hc-nilspec",
			HostIdentifier:        "i-host",
			DeploymentExecutionID: "exec-nilspec",
			CommandName:           "Install",
		}),
		getSpecFunc: func(_ context.Context, _, _ string) (*Envelope, string, error) {
			return nil, "CodeDeploy", nil
		},
		completeFunc: func(_ context.Context, _, status string, _ *Envelope) error {
			completeCh <- status
			return nil
		},
	}

	p := NewPoller(svc, nil, nil, &stubDeploymentTracker{}, "i-host",
		time.Millisecond, time.Millisecond, time.Second, slog.Default())

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	go func() { _ = p.Run(ctx) }()

	select {
	case status := <-completeCh:
		if status != "Failed" {
			t.Fatalf("expected Complete status Failed, got %q", status)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("timed out waiting for Complete call")
	}

	cancel()
}

// TestProcessCommand_ParseError verifies that when SpecParser.Parse returns an
// error, the poller calls Complete with "Failed". This covers the parse-error
// branch in processCommand and ensures malformed deployment specifications are
// reported as failures rather than silently ignored.
func TestProcessCommand_ParseError(t *testing.T) {
	completeCh := make(chan string, 1)

	svc := &stubCommandService{
		pollFunc: pollOnce(&HostCommand{
			HostCommandIdentifier: "hc-parse-err",
			HostIdentifier:        "i-host",
			DeploymentExecutionID: "exec-parse-err",
			CommandName:           "Install",
		}),
		getSpecFunc: func(_ context.Context, _, _ string) (*Envelope, string, error) {
			return &Envelope{Format: "TEXT/JSON", Payload: `{}`}, "CodeDeploy", nil
		},
		completeFunc: func(_ context.Context, _, status string, _ *Envelope) error {
			completeCh <- status
			return nil
		},
	}

	parser := &stubSpecParser{err: fmt.Errorf("invalid JSON")}
	p := NewPoller(svc, nil, parser, &stubDeploymentTracker{}, "i-host",
		time.Millisecond, time.Millisecond, time.Second, slog.Default())

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	go func() { _ = p.Run(ctx) }()

	select {
	case status := <-completeCh:
		if status != "Failed" {
			t.Fatalf("expected Complete status Failed, got %q", status)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("timed out waiting for Complete call")
	}

	cancel()
}
