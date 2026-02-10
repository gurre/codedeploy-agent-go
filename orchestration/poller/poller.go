// Package poller implements the main polling loop that retrieves deployment
// commands from the CodeDeploy Commands service and dispatches them for execution.
package poller

import (
	"context"
	"fmt"
	"log/slog"
	"sync"
	"time"

	"github.com/gurre/codedeploy-agent-go/logic/deployspec"
	"github.com/gurre/codedeploy-agent-go/logic/diagnostic"
)

const maxConcurrent = 16

// CommandService communicates with the CodeDeploy Commands service.
type CommandService interface {
	PollHostCommand(ctx context.Context, hostIdentifier string) (*HostCommand, error)
	Acknowledge(ctx context.Context, hostCommandID string, diagnostics *Envelope) (string, error)
	Complete(ctx context.Context, hostCommandID, status string, diagnostics *Envelope) error
	GetDeploymentSpecification(ctx context.Context, executionID, hostID string) (*Envelope, string, error)
}

// HostCommand is a command from PollHostCommand.
type HostCommand struct {
	HostCommandIdentifier string
	HostIdentifier        string
	DeploymentExecutionID string
	CommandName           string
}

// Envelope wraps a format+payload pair used in the protocol.
type Envelope struct {
	Format  string
	Payload string
}

// CommandExecutor dispatches a parsed deployment command.
type CommandExecutor interface {
	Execute(ctx context.Context, commandName string, spec deployspec.Spec) (string, error)
	IsNoop(commandName string, spec deployspec.Spec) bool
}

// SpecParser parses deployment specification envelopes.
type SpecParser interface {
	Parse(env deployspec.Envelope) (deployspec.Spec, error)
}

// DeploymentTracker manages in-progress deployment tracking.
type DeploymentTracker interface {
	Create(deploymentID, hostCommandIdentifier string) error
	Delete(deploymentID string)
	InProgressCommand() string
	CleanAll()
}

// Poller polls the CodeDeploy Commands service for work.
type Poller struct {
	commandService CommandService
	executor       CommandExecutor
	specParser     SpecParser
	tracker        DeploymentTracker
	hostIdentifier string
	pollInterval   time.Duration
	errorBackoff   time.Duration
	shutdownWait   time.Duration
	logger         *slog.Logger

	sem chan struct{} // bounded concurrency
	wg  sync.WaitGroup
}

// NewPoller creates a poller.
//
//	p := poller.NewPoller(svc, exec, parser, tracker, hostID, 30*time.Second, 30*time.Second, 7200*time.Second, logger)
func NewPoller(
	svc CommandService,
	exec CommandExecutor,
	parser SpecParser,
	tracker DeploymentTracker,
	hostIdentifier string,
	pollInterval, errorBackoff, shutdownWait time.Duration,
	logger *slog.Logger,
) *Poller {
	return &Poller{
		commandService: svc,
		executor:       exec,
		specParser:     parser,
		tracker:        tracker,
		hostIdentifier: hostIdentifier,
		pollInterval:   pollInterval,
		errorBackoff:   errorBackoff,
		shutdownWait:   shutdownWait,
		logger:         logger,
		sem:            make(chan struct{}, maxConcurrent),
	}
}

// RecoverFromCrash checks for in-progress deployments from before a crash
// and reports them as failed. Should be called once at startup.
func (p *Poller) RecoverFromCrash(ctx context.Context) {
	hci := p.tracker.InProgressCommand()
	if hci == "" {
		return
	}

	p.logger.Warn("found in-progress deployment after restart, failing it", "hostCommandIdentifier", hci)

	payload := diagnostic.BuildFailedAfterRestart("Failing in-progress lifecycle event after an agent restart.")
	_ = p.commandService.Complete(ctx, hci, "Failed", &Envelope{
		Format:  "JSON",
		Payload: payload,
	})

	p.tracker.CleanAll()
}

// Run starts the polling loop. Blocks until context is cancelled.
// On shutdown, waits up to shutdownWait for in-progress commands to complete.
func (p *Poller) Run(ctx context.Context) error {
	p.logger.Info("starting polling loop", "hostIdentifier", p.hostIdentifier)

	for {
		select {
		case <-ctx.Done():
			return p.shutdown()
		default:
		}

		if err := p.poll(ctx); err != nil {
			p.logger.Error("poll error", "error", err)
			select {
			case <-ctx.Done():
				return p.shutdown()
			case <-time.After(p.errorBackoff):
			}
			continue
		}

		select {
		case <-ctx.Done():
			return p.shutdown()
		case <-time.After(p.pollInterval):
		}
	}
}

func (p *Poller) poll(ctx context.Context) error {
	cmd, err := p.commandService.PollHostCommand(ctx, p.hostIdentifier)
	if err != nil {
		return fmt.Errorf("poller: poll: %w", err)
	}
	if cmd == nil {
		return nil
	}

	p.logger.Info("received command",
		"command", cmd.CommandName,
		"hostCommandIdentifier", cmd.HostCommandIdentifier,
		"deploymentExecutionId", cmd.DeploymentExecutionID)

	if cmd.CommandName == "" {
		return fmt.Errorf("poller: empty command name")
	}

	// Dispatch to goroutine pool
	p.sem <- struct{}{} // acquire
	p.wg.Add(1)
	go func() {
		defer func() {
			<-p.sem // release
			p.wg.Done()
		}()
		p.processCommand(ctx, cmd)
	}()

	return nil
}

func (p *Poller) processCommand(ctx context.Context, cmd *HostCommand) {
	defer func() {
		if r := recover(); r != nil {
			p.logger.Error("panic in command processing", "panic", r, "command", cmd.CommandName)
		}
	}()

	// Get deployment spec
	specEnvelope, deploySystem, err := p.commandService.GetDeploymentSpecification(
		ctx, cmd.DeploymentExecutionID, p.hostIdentifier)
	if err != nil {
		p.reportError(ctx, cmd.HostCommandIdentifier, err)
		return
	}

	if deploySystem != "CodeDeploy" {
		p.reportError(ctx, cmd.HostCommandIdentifier,
			fmt.Errorf("deployment system mismatch: expected CodeDeploy, got %s", deploySystem))
		return
	}

	if specEnvelope == nil {
		p.reportError(ctx, cmd.HostCommandIdentifier, fmt.Errorf("missing deployment specification"))
		return
	}

	// Parse spec
	spec, err := p.specParser.Parse(deployspec.Envelope{
		Format:  specEnvelope.Format,
		Payload: specEnvelope.Payload,
	})
	if err != nil {
		p.reportError(ctx, cmd.HostCommandIdentifier, err)
		return
	}

	// Check noop for acknowledgement
	isNoop := p.executor.IsNoop(cmd.CommandName, spec)

	// Acknowledge
	noopPayload := fmt.Sprintf(`{"IsCommandNoop":%v}`, isNoop)
	ackStatus, err := p.commandService.Acknowledge(ctx, cmd.HostCommandIdentifier, &Envelope{
		Format:  "JSON",
		Payload: noopPayload,
	})
	if err != nil {
		p.reportError(ctx, cmd.HostCommandIdentifier, err)
		return
	}

	if ackStatus == "Succeeded" || ackStatus == "Failed" {
		p.logger.Info("command already terminal", "status", ackStatus, "command", cmd.CommandName)
		return
	}

	// Track deployment
	if err := p.tracker.Create(spec.DeploymentID, cmd.HostCommandIdentifier); err != nil {
		p.logger.Error("failed to create tracking file", "error", err)
	}
	defer p.tracker.Delete(spec.DeploymentID)

	// Execute
	_, err = p.executor.Execute(ctx, cmd.CommandName, spec)
	if err != nil {
		p.reportError(ctx, cmd.HostCommandIdentifier, err)
		return
	}

	// Report success
	payload := diagnostic.BuildSuccess("")
	if completeErr := p.commandService.Complete(ctx, cmd.HostCommandIdentifier, "Succeeded", &Envelope{
		Format:  "JSON",
		Payload: payload,
	}); completeErr != nil {
		p.logger.Error("failed to report success", "error", completeErr)
	}
}

func (p *Poller) reportError(ctx context.Context, hci string, err error) {
	p.logger.Error("command failed", "error", err, "hostCommandIdentifier", hci)
	payload := diagnostic.BuildFromError(err)
	_ = p.commandService.Complete(ctx, hci, "Failed", &Envelope{
		Format:  "JSON",
		Payload: payload,
	})
}

func (p *Poller) shutdown() error {
	p.logger.Info("shutting down, waiting for in-progress commands")

	done := make(chan struct{})
	go func() {
		p.wg.Wait()
		close(done)
	}()

	select {
	case <-done:
		p.logger.Info("all commands completed")
	case <-time.After(p.shutdownWait):
		p.logger.Warn("shutdown timeout exceeded, some commands may be interrupted")
	}
	return nil
}
