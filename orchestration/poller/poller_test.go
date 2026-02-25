package poller

import (
	"context"
	"fmt"
	"log/slog"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	json "github.com/goccy/go-json"
	"github.com/gurre/codedeploy-agent-go/logic/deployspec"
	"github.com/gurre/codedeploy-agent-go/logic/diagnostic"
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
		time.Millisecond, time.Millisecond, time.Millisecond, time.Second, slog.Default())

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
		time.Millisecond, time.Millisecond, time.Millisecond, time.Second, slog.Default())

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
		time.Millisecond, time.Millisecond, time.Millisecond, time.Second, slog.Default())

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
		time.Millisecond, time.Millisecond, time.Millisecond, time.Second, slog.Default())

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
		time.Millisecond, time.Millisecond, time.Millisecond, time.Second, slog.Default())

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
		time.Millisecond, time.Millisecond, time.Millisecond, time.Second, slog.Default())

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
		time.Millisecond, time.Millisecond, 500*time.Millisecond, time.Second, slog.Default())

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
		time.Millisecond, time.Millisecond, 30*time.Second, time.Second, slog.Default())

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
		time.Millisecond, time.Millisecond, 30*time.Second, time.Second, slog.Default())
	p.consecutiveErrors = 0

	err := fmt.Errorf("some network error")
	delay := p.computeBackoff(err)
	// count=0 with 30s base: should be in [15s, 30s]
	if delay < 15*time.Second || delay > 30*time.Second {
		t.Errorf("non-throttle backoff = %v, want in [15s, 30s]", delay)
	}
}

// TestRun_ActivePollInterval verifies that while a command goroutine is executing,
// the poller uses the shorter activePollInterval. When no commands are in-flight,
// it uses the longer pollInterval. This reduces worst-case latency for picking up
// subsequent lifecycle events during an active deployment.
func TestRun_ActivePollInterval(t *testing.T) {
	// Use durations large enough to distinguish but small enough for a fast test.
	// idleInterval is long enough that if the poller uses it during active commands,
	// the test times out.
	const (
		idleInterval   = 2 * time.Second
		activeInterval = 10 * time.Millisecond
	)

	// executeCh blocks the command goroutine so it stays "active"
	executeCh := make(chan struct{})
	// secondPollCh signals the second poll arrived while the command is still executing
	secondPollCh := make(chan struct{}, 1)

	var pollCount atomic.Int32

	svc := &stubCommandService{
		pollFunc: func(ctx context.Context, _ string) (*HostCommand, error) {
			n := pollCount.Add(1)
			switch n {
			case 1:
				// Return a command that will block in Execute
				return &HostCommand{
					HostCommandIdentifier: "hc-active",
					HostIdentifier:        "i-host",
					DeploymentExecutionID: "exec-active",
					CommandName:           "Install",
				}, nil
			case 2:
				// Second poll arrived while command is still executing
				select {
				case secondPollCh <- struct{}{}:
				default:
				}
				<-ctx.Done()
				return nil, ctx.Err()
			default:
				<-ctx.Done()
				return nil, ctx.Err()
			}
		},
		getSpecFunc: func(_ context.Context, _, _ string) (*Envelope, string, error) {
			return &Envelope{Format: "TEXT/JSON", Payload: `{}`}, "CodeDeploy", nil
		},
		acknowledgeFunc: func(_ context.Context, _ string, _ *Envelope) (string, error) {
			return "InProgress", nil
		},
		completeFunc: func(_ context.Context, _, _ string, _ *Envelope) error {
			return nil
		},
	}

	spec := deployspec.Spec{DeploymentID: "d-ACTIVE", DeploymentGroupID: "dg-1"}
	parser := &stubSpecParser{spec: spec}
	exec := &stubCommandExecutor{
		executeFunc: func(_ context.Context, _ string, _ deployspec.Spec) (string, error) {
			// Block until test releases
			<-executeCh
			return "ok", nil
		},
	}
	tracker := &stubDeploymentTracker{}

	p := NewPoller(svc, exec, parser, tracker, "i-host",
		idleInterval, activeInterval, time.Millisecond, time.Second, slog.Default())

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	go func() { _ = p.Run(ctx) }()

	// The second poll should arrive within activeInterval (~10ms), not idleInterval (2s).
	// If the poller incorrectly uses idleInterval, this times out at 500ms.
	select {
	case <-secondPollCh:
		// Active interval was used — command goroutine is still blocked
	case <-time.After(500 * time.Millisecond):
		t.Fatal("second poll did not arrive quickly — active interval may not be in effect")
	}

	// Release the blocked command
	close(executeCh)
	cancel()
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
		time.Millisecond, time.Millisecond, time.Millisecond, time.Second, slog.Default())

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
		time.Millisecond, time.Millisecond, time.Millisecond, time.Second, slog.Default())

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
		time.Millisecond, time.Millisecond, time.Millisecond, time.Second, slog.Default())

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

// TestProcessCommand_ScriptErrorDiagnostic verifies that when Execute returns
// a *diagnostic.ScriptError (wrapped or direct), reportError extracts the rich
// diagnostic fields — error_code, script_name, message, log — into the Complete
// payload. Without this, the console only shows "UnknownError" with no log output.
func TestProcessCommand_ScriptErrorDiagnostic(t *testing.T) {
	completeCh := make(chan *Envelope, 1)

	svc := &stubCommandService{
		pollFunc: pollOnce(&HostCommand{
			HostCommandIdentifier: "hc-script-err",
			HostIdentifier:        "i-host",
			DeploymentExecutionID: "exec-script-err",
			CommandName:           "Install",
		}),
		getSpecFunc: func(_ context.Context, _, _ string) (*Envelope, string, error) {
			return &Envelope{Format: "TEXT/JSON", Payload: `{}`}, "CodeDeploy", nil
		},
		acknowledgeFunc: func(_ context.Context, _ string, _ *Envelope) (string, error) {
			return "InProgress", nil
		},
		completeFunc: func(_ context.Context, _, _ string, diag *Envelope) error {
			completeCh <- diag
			return nil
		},
	}

	spec := deployspec.Spec{
		DeploymentID:        "d-DIAG",
		DeploymentGroupID:   "dg-1",
		DeploymentGroupName: "prod",
		ApplicationName:     "myapp",
	}
	parser := &stubSpecParser{spec: spec}
	exec := &stubCommandExecutor{
		executeFunc: func(_ context.Context, _ string, _ deployspec.Spec) (string, error) {
			return "", &diagnostic.ScriptError{
				Code:       diagnostic.ScriptFailed,
				ScriptName: "scripts/start.sh",
				Message:    "script at scripts/start.sh failed with exit code 1",
				Log:        "Script - scripts/start.sh\nError: port in use\n",
			}
		},
	}
	tracker := &stubDeploymentTracker{}

	p := NewPoller(svc, exec, parser, tracker, "i-host",
		time.Millisecond, time.Millisecond, time.Millisecond, time.Second, slog.Default())

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	go func() { _ = p.Run(ctx) }()

	select {
	case diag := <-completeCh:
		var d diagnostic.Diagnostic
		if err := json.Unmarshal([]byte(diag.Payload), &d); err != nil {
			t.Fatalf("failed to unmarshal diagnostic payload: %v", err)
		}
		if d.ErrorCode != diagnostic.ScriptFailed {
			t.Errorf("error_code = %d, want %d (ScriptFailed)", d.ErrorCode, diagnostic.ScriptFailed)
		}
		if d.ScriptName != "scripts/start.sh" {
			t.Errorf("script_name = %q, want %q", d.ScriptName, "scripts/start.sh")
		}
		if d.Log == "" {
			t.Error("log field should contain captured script output")
		}
	case <-time.After(3 * time.Second):
		t.Fatal("timed out waiting for Complete call")
	}

	cancel()
}

// TestProcessCommand_WrappedScriptError verifies that reportError detects a
// *diagnostic.ScriptError even when wrapped by an intermediate layer (e.g.
// fmt.Errorf with %w). The executor does not currently wrap, but this guards
// against future refactors that add wrapping in the error chain.
func TestProcessCommand_WrappedScriptError(t *testing.T) {
	completeCh := make(chan *Envelope, 1)

	svc := &stubCommandService{
		pollFunc: pollOnce(&HostCommand{
			HostCommandIdentifier: "hc-wrapped",
			HostIdentifier:        "i-host",
			DeploymentExecutionID: "exec-wrapped",
			CommandName:           "Install",
		}),
		getSpecFunc: func(_ context.Context, _, _ string) (*Envelope, string, error) {
			return &Envelope{Format: "TEXT/JSON", Payload: `{}`}, "CodeDeploy", nil
		},
		acknowledgeFunc: func(_ context.Context, _ string, _ *Envelope) (string, error) {
			return "InProgress", nil
		},
		completeFunc: func(_ context.Context, _, _ string, diag *Envelope) error {
			completeCh <- diag
			return nil
		},
	}

	spec := deployspec.Spec{
		DeploymentID:        "d-WRAP",
		DeploymentGroupID:   "dg-1",
		DeploymentGroupName: "prod",
		ApplicationName:     "myapp",
	}
	parser := &stubSpecParser{spec: spec}
	exec := &stubCommandExecutor{
		executeFunc: func(_ context.Context, _ string, _ deployspec.Spec) (string, error) {
			inner := &diagnostic.ScriptError{
				Code:       diagnostic.ScriptTimedOut,
				ScriptName: "scripts/slow.sh",
				Message:    "script at scripts/slow.sh timed out after 30 seconds",
				Log:        "Script - scripts/slow.sh\nstill running...\n",
			}
			// Simulate an intermediate layer wrapping the error
			return "", fmt.Errorf("executor: hooks: %w", inner)
		},
	}
	tracker := &stubDeploymentTracker{}

	p := NewPoller(svc, exec, parser, tracker, "i-host",
		time.Millisecond, time.Millisecond, time.Millisecond, time.Second, slog.Default())

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	go func() { _ = p.Run(ctx) }()

	select {
	case diag := <-completeCh:
		var d diagnostic.Diagnostic
		if err := json.Unmarshal([]byte(diag.Payload), &d); err != nil {
			t.Fatalf("failed to unmarshal diagnostic payload: %v", err)
		}
		if d.ErrorCode != diagnostic.ScriptTimedOut {
			t.Errorf("error_code = %d, want %d (ScriptTimedOut)", d.ErrorCode, diagnostic.ScriptTimedOut)
		}
		if d.ScriptName != "scripts/slow.sh" {
			t.Errorf("script_name = %q, want %q", d.ScriptName, "scripts/slow.sh")
		}
		if d.Log == "" {
			t.Error("log field should contain captured script output")
		}
	case <-time.After(3 * time.Second):
		t.Fatal("timed out waiting for Complete call")
	}

	cancel()
}

// TestProcessCommand_NonScriptErrorPayload verifies that when Execute returns a
// plain error (not *diagnostic.ScriptError), reportError falls back to
// BuildFromError which uses error_code 5 (UnknownError) and an empty log field.
// This ensures non-script failures (network errors, parse errors) don't
// accidentally get script-specific error codes.
func TestProcessCommand_NonScriptErrorPayload(t *testing.T) {
	completeCh := make(chan *Envelope, 1)

	svc := &stubCommandService{
		pollFunc: pollOnce(&HostCommand{
			HostCommandIdentifier: "hc-plain-err",
			HostIdentifier:        "i-host",
			DeploymentExecutionID: "exec-plain-err",
			CommandName:           "Install",
		}),
		getSpecFunc: func(_ context.Context, _, _ string) (*Envelope, string, error) {
			return &Envelope{Format: "TEXT/JSON", Payload: `{}`}, "CodeDeploy", nil
		},
		acknowledgeFunc: func(_ context.Context, _ string, _ *Envelope) (string, error) {
			return "InProgress", nil
		},
		completeFunc: func(_ context.Context, _, _ string, diag *Envelope) error {
			completeCh <- diag
			return nil
		},
	}

	spec := deployspec.Spec{
		DeploymentID:        "d-PLAIN",
		DeploymentGroupID:   "dg-1",
		DeploymentGroupName: "prod",
		ApplicationName:     "myapp",
	}
	parser := &stubSpecParser{spec: spec}
	exec := &stubCommandExecutor{
		executeFunc: func(_ context.Context, _ string, _ deployspec.Spec) (string, error) {
			return "", fmt.Errorf("network timeout fetching bundle")
		},
	}
	tracker := &stubDeploymentTracker{}

	p := NewPoller(svc, exec, parser, tracker, "i-host",
		time.Millisecond, time.Millisecond, time.Millisecond, time.Second, slog.Default())

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	go func() { _ = p.Run(ctx) }()

	select {
	case diag := <-completeCh:
		var d diagnostic.Diagnostic
		if err := json.Unmarshal([]byte(diag.Payload), &d); err != nil {
			t.Fatalf("failed to unmarshal diagnostic payload: %v", err)
		}
		if d.ErrorCode != diagnostic.UnknownError {
			t.Errorf("error_code = %d, want %d (UnknownError)", d.ErrorCode, diagnostic.UnknownError)
		}
		if d.Log != "" {
			t.Errorf("log should be empty for non-script errors, got %q", d.Log)
		}
		if d.ScriptName != "" {
			t.Errorf("script_name should be empty for non-script errors, got %q", d.ScriptName)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("timed out waiting for Complete call")
	}

	cancel()
}

// captureSlogHandler is a test double that records slog.Records for assertion.
// It implements slog.Handler by storing every emitted record.
type captureSlogHandler struct {
	mu      sync.Mutex
	records []slog.Record
}

func (h *captureSlogHandler) Enabled(_ context.Context, _ slog.Level) bool { return true }
func (h *captureSlogHandler) WithAttrs(_ []slog.Attr) slog.Handler         { return h }
func (h *captureSlogHandler) WithGroup(_ string) slog.Handler              { return h }
func (h *captureSlogHandler) Handle(_ context.Context, r slog.Record) error {
	h.mu.Lock()
	defer h.mu.Unlock()
	h.records = append(h.records, r)
	return nil
}

// findRecord returns the first record whose message matches msg, or nil.
func (h *captureSlogHandler) findRecord(msg string) *slog.Record {
	h.mu.Lock()
	defer h.mu.Unlock()
	for i := range h.records {
		if h.records[i].Message == msg {
			return &h.records[i]
		}
	}
	return nil
}

// attrValue returns the string value of the first attribute with the given key
// in the record, or "" if not found.
func attrValue(r *slog.Record, key string) string {
	var val string
	r.Attrs(func(a slog.Attr) bool {
		if a.Key == key {
			val = a.Value.String()
			return false
		}
		return true
	})
	return val
}

// TestReportError_ScriptErrorLogsOutput verifies that when the executor returns
// a *diagnostic.ScriptError, the structured log record contains msg="script failed"
// along with script, error, and output attributes. This ensures operators can see
// script failure details in journalctl without digging through log files.
func TestReportError_ScriptErrorLogsOutput(t *testing.T) {
	handler := &captureSlogHandler{}
	logger := slog.New(handler)

	svc := &stubCommandService{
		completeFunc: func(_ context.Context, _, _ string, _ *Envelope) error {
			return nil
		},
	}

	p := NewPoller(svc, nil, nil, &stubDeploymentTracker{}, "i-host",
		time.Millisecond, time.Millisecond, time.Millisecond, time.Second, logger)

	se := &diagnostic.ScriptError{
		Code:       diagnostic.ScriptFailed,
		ScriptName: "scripts/start.sh",
		Message:    "script at scripts/start.sh failed with exit code 1",
		Log:        "Script - scripts/start.sh\nError: port 8080 already in use\n",
	}

	p.reportError(context.Background(), "hc-test", se)

	rec := handler.findRecord("script failed")
	if rec == nil {
		t.Fatal("expected log record with msg=\"script failed\", not found")
	}
	if got := attrValue(rec, "script"); got != "scripts/start.sh" {
		t.Errorf("script attr = %q, want %q", got, "scripts/start.sh")
	}
	if got := attrValue(rec, "output"); got == "" {
		t.Error("output attr should be non-empty")
	}
}

// TestReportError_PlainErrorLogsCommandFailed verifies that when the executor
// returns a plain error (not *diagnostic.ScriptError), the log record uses
// msg="command failed". This is a regression guard ensuring the generic branch
// was not accidentally removed when adding the script-specific branch.
func TestReportError_PlainErrorLogsCommandFailed(t *testing.T) {
	handler := &captureSlogHandler{}
	logger := slog.New(handler)

	svc := &stubCommandService{
		completeFunc: func(_ context.Context, _, _ string, _ *Envelope) error {
			return nil
		},
	}

	p := NewPoller(svc, nil, nil, &stubDeploymentTracker{}, "i-host",
		time.Millisecond, time.Millisecond, time.Millisecond, time.Second, logger)

	p.reportError(context.Background(), "hc-test", fmt.Errorf("network timeout"))

	rec := handler.findRecord("command failed")
	if rec == nil {
		t.Fatal("expected log record with msg=\"command failed\", not found")
	}
	if got := attrValue(rec, "error"); got == "" {
		t.Error("error attr should be non-empty")
	}
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
		time.Millisecond, time.Millisecond, time.Millisecond, time.Second, slog.Default())

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
