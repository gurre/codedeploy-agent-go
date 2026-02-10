// Package agent wires configuration, adaptors, and orchestration together
// to run the CodeDeploy agent daemon.
package agent

import (
	"context"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/url"
	"os"
	"os/signal"
	"syscall"
	"time"

	awsconfig "github.com/aws/aws-sdk-go-v2/config"
	"github.com/aws/aws-sdk-go-v2/credentials"

	"github.com/gurre/codedeploy-agent-go/adaptor/archive"
	"github.com/gurre/codedeploy-agent-go/adaptor/codedeployctl"
	"github.com/gurre/codedeploy-agent-go/adaptor/configloader"
	"github.com/gurre/codedeploy-agent-go/adaptor/filesystem"
	"github.com/gurre/codedeploy-agent-go/adaptor/githubdownload"
	"github.com/gurre/codedeploy-agent-go/adaptor/imds"
	"github.com/gurre/codedeploy-agent-go/adaptor/logfile"
	"github.com/gurre/codedeploy-agent-go/adaptor/pkcs7"
	"github.com/gurre/codedeploy-agent-go/adaptor/s3download"
	"github.com/gurre/codedeploy-agent-go/adaptor/scriptrunner"
	"github.com/gurre/codedeploy-agent-go/logic/appspec"
	"github.com/gurre/codedeploy-agent-go/logic/deployspec"
	"github.com/gurre/codedeploy-agent-go/logic/lifecycle"
	"github.com/gurre/codedeploy-agent-go/orchestration/executor"
	"github.com/gurre/codedeploy-agent-go/orchestration/hookrunner"
	"github.com/gurre/codedeploy-agent-go/orchestration/installer"
	"github.com/gurre/codedeploy-agent-go/orchestration/poller"
	"github.com/gurre/codedeploy-agent-go/orchestration/tracker"
	"github.com/gurre/codedeploy-agent-go/state/config"
)

// Run starts the CodeDeploy agent with the given config file path.
// It blocks until SIGTERM/SIGINT is received or the context is cancelled.
//
//	err := agent.Run(ctx, "/etc/codedeploy-agent/conf/codedeployagent.yml")
func Run(ctx context.Context, configPath string) error {
	// Load config
	cfg, err := configloader.LoadAgent(configPath)
	if err != nil {
		return fmt.Errorf("agent: load config: %w", err)
	}

	// Set up log rotation: write to both stderr (journald) and rotating file
	logWriter := logfile.NewRotatingWriter(cfg.LogDir, cfg.ProgramName+".log", 64*1024*1024, 8)
	if err := logWriter.Open(); err != nil {
		return fmt.Errorf("agent: open log file: %w", err)
	}
	defer func() { _ = logWriter.Close() }()

	logger := slog.New(slog.NewTextHandler(io.MultiWriter(os.Stderr, logWriter), nil))
	slog.SetDefault(logger)

	// Signal handling
	ctx, cancel := signal.NotifyContext(ctx, syscall.SIGTERM, syscall.SIGINT)
	defer cancel()

	// Build proxy-aware transport when ProxyURI is configured.
	// Each adaptor wraps this transport in its own *http.Client with
	// adaptor-specific timeouts, so we only share the transport layer.
	var proxyTransport http.RoundTripper
	if cfg.ProxyURI != "" {
		proxyURL, parseErr := url.Parse(cfg.ProxyURI)
		if parseErr != nil {
			return fmt.Errorf("agent: parse proxy URI: %w", parseErr)
		}
		proxyTransport = &http.Transport{Proxy: http.ProxyURL(proxyURL)}
		logger.Info("proxy configured", "uri", cfg.ProxyURI)
	}

	// Resolve identity and on-premises credentials
	identity, err := resolveIdentity(ctx, cfg, proxyTransport, logger)
	if err != nil {
		return fmt.Errorf("agent: resolve identity: %w", err)
	}

	logger.Info("agent starting",
		"region", identity.Region,
		"hostIdentifier", identity.HostID,
		"rootDir", cfg.RootDir)

	// Build AWS config with optional on-premises credentials
	awsOpts := []func(*awsconfig.LoadOptions) error{
		awsconfig.WithRegion(identity.Region),
	}
	if proxyTransport != nil {
		awsOpts = append(awsOpts, awsconfig.WithHTTPClient(&http.Client{
			Transport: proxyTransport,
			Timeout:   cfg.HTTPReadTimeout,
		}))
	}
	if identity.StaticAccessKey != "" && identity.StaticSecretKey != "" {
		awsOpts = append(awsOpts, awsconfig.WithCredentialsProvider(
			credentials.NewStaticCredentialsProvider(identity.StaticAccessKey, identity.StaticSecretKey, ""),
		))
	}
	if identity.CredentialsFile != "" {
		awsOpts = append(awsOpts, awsconfig.WithSharedCredentialsFiles([]string{identity.CredentialsFile}))
	}
	awsCfg, err := awsconfig.LoadDefaultConfig(ctx, awsOpts...)
	if err != nil {
		return fmt.Errorf("agent: load AWS config: %w", err)
	}

	// Build PKCS7 verifier for signed deployment specs
	verifier, err := pkcs7.NewVerifier()
	if err != nil {
		return fmt.Errorf("agent: create PKCS7 verifier: %w", err)
	}

	// Build adaptors — each receives the shared proxy transport and applies
	// its own timeout internally, except s3download which needs *http.Client
	// for the AWS SDK.
	commandClient := codedeployctl.NewClient(
		awsCfg.Credentials,
		identity.Region,
		cfg.DeployControlEndpoint,
		proxyTransport,
		logger,
	)

	var s3ProxyClient *http.Client
	if proxyTransport != nil {
		s3ProxyClient = &http.Client{
			Transport: proxyTransport,
			Timeout:   cfg.HTTPReadTimeout,
		}
	}
	s3dl := s3download.NewDownloader(
		awsCfg,
		identity.Region,
		cfg.S3EndpointOverride,
		cfg.UseFIPSMode,
		s3ProxyClient,
		logger,
	)

	ghDl := githubdownload.NewDownloader(proxyTransport, logger)
	unpacker := archive.NewUnpacker()
	fileOp := filesystem.NewOperator()
	sr := scriptrunner.NewRunner(logger)

	// Build orchestration components
	ft := tracker.NewFileTracker(cfg.RootDir, cfg.OngoingDeploymentTracking, logger)
	hookMapping := lifecycle.DefaultHookMapping()

	// Wire adaptor implementations to orchestration interfaces
	dl := &downloaderBridge{s3: s3dl, gh: ghDl}
	fileOpBridge := &fileOperatorBridge{op: fileOp}
	hookBridge := &hookRunnerBridge{runner: hookrunner.NewRunner(&scriptRunnerBridge{sr: sr}, logger)}
	instBridge := &installerBridge{inst: installer.NewInstaller(&fileOperatorInstallerBridge{op: fileOp}, logger)}

	exec := executor.NewExecutor(
		dl, unpacker, hookBridge, instBridge, fileOpBridge,
		cfg.RootDir, hookMapping, cfg.MaxRevisions, logger,
	)

	svcBridge := &commandServiceBridge{client: commandClient}
	parserBridge := &specParserBridge{verifier: verifier}

	p := poller.NewPoller(
		svcBridge, exec, parserBridge, ft,
		identity.HostID,
		cfg.PollInterval, cfg.ErrorBackoff, cfg.KillAgentMaxWait,
		logger,
	)

	// Crash recovery: fail any in-progress deployments from before restart
	p.RecoverFromCrash(ctx)

	return p.Run(ctx)
}

// agentIdentity holds the resolved identity and credential information
// for the agent. On EC2 instances, only Region and HostID are set. On-premises
// instances additionally carry static credentials or a credentials file path.
type agentIdentity struct {
	Region          string
	HostID          string
	StaticAccessKey string
	StaticSecretKey string
	CredentialsFile string
}

func resolveIdentity(ctx context.Context, cfg config.Agent, transport http.RoundTripper, logger *slog.Logger) (agentIdentity, error) {
	// Check environment overrides first
	region := os.Getenv("AWS_REGION")
	hostID := os.Getenv("AWS_HOST_IDENTIFIER")

	if region != "" && hostID != "" {
		return agentIdentity{Region: region, HostID: hostID}, nil
	}

	// Try on-premises config
	if _, err := os.Stat(cfg.OnPremisesConfigFile); err == nil {
		onprem, err := configloader.LoadOnPremises(cfg.OnPremisesConfigFile)
		if err != nil {
			return agentIdentity{}, err
		}

		// IAM User ARN: use static credentials from the config file
		if onprem.Region != "" && onprem.IAMUserARN != "" {
			id := agentIdentity{
				Region:          orEnvDefault(region, onprem.Region),
				HostID:          orEnvDefault(hostID, onprem.IAMUserARN),
				StaticAccessKey: onprem.AWSAccessKeyID,
				StaticSecretKey: onprem.AWSSecretAccessKey,
			}
			return id, nil
		}

		// IAM Session ARN: use a rotating credentials file
		if onprem.Region != "" && onprem.IAMSessionARN != "" {
			id := agentIdentity{
				Region:          orEnvDefault(region, onprem.Region),
				HostID:          orEnvDefault(hostID, onprem.IAMSessionARN),
				CredentialsFile: onprem.CredentialsFile,
			}
			return id, nil
		}
	}

	// Fall back to IMDS with a single retry.
	// On freshly launched EC2 instances IMDS may be momentarily unavailable
	// while the network stack initializes.
	id, err := resolveIMDS(ctx, cfg, transport, region, hostID, logger)
	if err != nil {
		logger.Warn("IMDS identity resolution failed, retrying after 5s", "error", err)
		select {
		case <-ctx.Done():
			return agentIdentity{}, ctx.Err()
		case <-time.After(5 * time.Second):
		}
		id, err = resolveIMDS(ctx, cfg, transport, region, hostID, logger)
		if err != nil {
			return agentIdentity{}, err
		}
	}
	return id, nil
}

// resolveIMDS performs a single IMDS identity resolution attempt.
func resolveIMDS(ctx context.Context, cfg config.Agent, transport http.RoundTripper, region, hostID string, logger *slog.Logger) (agentIdentity, error) {
	imdsClient := imds.NewClient(cfg.DisableIMDSv1, transport, logger)

	if region == "" {
		r, err := imdsClient.Region(ctx)
		if err != nil {
			return agentIdentity{}, fmt.Errorf("cannot determine region: %w", err)
		}
		region = r
	}

	if hostID == "" {
		h, err := imdsClient.HostIdentifier(ctx)
		if err != nil {
			return agentIdentity{}, fmt.Errorf("cannot determine host identifier: %w", err)
		}
		hostID = h
	}

	return agentIdentity{Region: region, HostID: hostID}, nil
}

func orEnvDefault(envVal, configVal string) string {
	if envVal != "" {
		return envVal
	}
	return configVal
}

// Bridge types adapt adaptor implementations to orchestration interfaces.

// downloaderBridge adapts S3 and GitHub downloaders to executor.BundleDownloader.
type downloaderBridge struct {
	s3 *s3download.Downloader
	gh *githubdownload.Downloader
}

func (d *downloaderBridge) DownloadS3(ctx context.Context, bucket, key, version, etag, dest string) error {
	return d.s3.Download(ctx, bucket, key, version, etag, dest)
}

func (d *downloaderBridge) DownloadGitHub(ctx context.Context, account, repo, commit, bundleType, token, dest string) error {
	return d.gh.Download(ctx, account, repo, commit, bundleType, token, dest)
}

// fileOperatorBridge adapts filesystem.Operator to executor.FileOperator.
type fileOperatorBridge struct {
	op *filesystem.Operator
}

func (f *fileOperatorBridge) MkdirAll(path string) error  { return f.op.MkdirAll(path) }
func (f *fileOperatorBridge) RemoveAll(path string) error { return f.op.RemoveAll(path) }

// scriptRunnerBridge adapts scriptrunner.Runner to hookrunner.ScriptRunner.
type scriptRunnerBridge struct {
	sr *scriptrunner.Runner
}

func (s *scriptRunnerBridge) Run(ctx context.Context, scriptPath string, env map[string]string, timeoutSeconds int) (hookrunner.ScriptResult, error) {
	result, err := s.sr.Run(ctx, scriptPath, env, timeoutSeconds)
	if err != nil {
		return hookrunner.ScriptResult{}, err
	}
	return hookrunner.ScriptResult{
		ExitCode: result.ExitCode,
		Stdout:   result.Stdout,
		Stderr:   result.Stderr,
		TimedOut: result.TimedOut,
	}, nil
}

// hookRunnerBridge adapts hookrunner.Runner to executor.HookRunner.
type hookRunnerBridge struct {
	runner *hookrunner.Runner
}

func (h *hookRunnerBridge) Run(ctx context.Context, args executor.HookRunArgs) (executor.HookResult, error) {
	result, err := h.runner.Run(ctx, hookrunner.RunArgs{
		LifecycleEvent:      args.LifecycleEvent,
		DeploymentID:        args.DeploymentID,
		ApplicationName:     args.ApplicationName,
		DeploymentGroupName: args.DeploymentGroupName,
		DeploymentGroupID:   args.DeploymentGroupID,
		DeploymentCreator:   args.DeploymentCreator,
		DeploymentType:      args.DeploymentType,
		AppSpecPath:         args.AppSpecPath,
		DeploymentRootDir:   args.DeploymentRootDir,
		LastSuccessfulDir:   args.LastSuccessfulDir,
		MostRecentDir:       args.MostRecentDir,
		RevisionEnvs:        args.RevisionEnvs,
	})
	if err != nil {
		return executor.HookResult{Log: result.Log}, err
	}
	return executor.HookResult{
		IsNoop: result.IsNoop,
		Log:    result.Log,
	}, nil
}

func (h *hookRunnerBridge) IsNoop(args executor.HookRunArgs) (bool, error) {
	return h.runner.IsNoop(hookrunner.RunArgs{
		LifecycleEvent:      args.LifecycleEvent,
		DeploymentID:        args.DeploymentID,
		ApplicationName:     args.ApplicationName,
		DeploymentGroupName: args.DeploymentGroupName,
		DeploymentGroupID:   args.DeploymentGroupID,
		DeploymentCreator:   args.DeploymentCreator,
		DeploymentType:      args.DeploymentType,
		AppSpecPath:         args.AppSpecPath,
		DeploymentRootDir:   args.DeploymentRootDir,
		LastSuccessfulDir:   args.LastSuccessfulDir,
		MostRecentDir:       args.MostRecentDir,
		RevisionEnvs:        args.RevisionEnvs,
	})
}

// fileOperatorInstallerBridge adapts filesystem.Operator to installer.FileOperator.
type fileOperatorInstallerBridge struct {
	op *filesystem.Operator
}

func (f *fileOperatorInstallerBridge) Copy(source, destination string) error {
	return f.op.Copy(source, destination)
}
func (f *fileOperatorInstallerBridge) Mkdir(path string) error    { return f.op.Mkdir(path) }
func (f *fileOperatorInstallerBridge) MkdirAll(path string) error { return f.op.MkdirAll(path) }
func (f *fileOperatorInstallerBridge) Chmod(path string, mode os.FileMode) error {
	return f.op.Chmod(path, mode)
}
func (f *fileOperatorInstallerBridge) Chown(path, owner, group string) error {
	return f.op.Chown(path, owner, group)
}
func (f *fileOperatorInstallerBridge) SetACL(path string, acl []string) error {
	return f.op.SetACL(path, acl)
}
func (f *fileOperatorInstallerBridge) SetContext(path string, seUser, seType, seRange string) error {
	return f.op.SetContext(path, seUser, seType, seRange)
}
func (f *fileOperatorInstallerBridge) RemoveContext(path string) error {
	return f.op.RemoveContext(path)
}
func (f *fileOperatorInstallerBridge) Remove(path string) error { return f.op.Remove(path) }

// installerBridge adapts installer.Installer to executor.Installer.
type installerBridge struct {
	inst *installer.Installer
}

func (i *installerBridge) Install(deploymentGroupID, archiveDir, instructionsDir string, spec appspec.Spec, fileExistsBehavior string) error {
	return i.inst.Install(deploymentGroupID, archiveDir, instructionsDir, spec, fileExistsBehavior)
}

// commandServiceBridge adapts codedeployctl.Client to poller.CommandService.
type commandServiceBridge struct {
	client *codedeployctl.Client
}

func (s *commandServiceBridge) PollHostCommand(ctx context.Context, hostID string) (*poller.HostCommand, error) {
	cmd, err := s.client.PollHostCommand(ctx, hostID)
	if err != nil {
		return nil, err
	}
	if cmd == nil {
		return nil, nil
	}
	return &poller.HostCommand{
		HostCommandIdentifier: cmd.HostCommandIdentifier,
		HostIdentifier:        cmd.HostIdentifier,
		DeploymentExecutionID: cmd.DeploymentExecutionID,
		CommandName:           cmd.CommandName,
	}, nil
}

func (s *commandServiceBridge) Acknowledge(ctx context.Context, hci string, diag *poller.Envelope) (string, error) {
	var env *codedeployctl.Envelope
	if diag != nil {
		env = &codedeployctl.Envelope{Format: diag.Format, Payload: diag.Payload}
	}
	return s.client.Acknowledge(ctx, hci, env)
}

func (s *commandServiceBridge) Complete(ctx context.Context, hci, status string, diag *poller.Envelope) error {
	var env *codedeployctl.Envelope
	if diag != nil {
		env = &codedeployctl.Envelope{Format: diag.Format, Payload: diag.Payload}
	}
	return s.client.Complete(ctx, hci, status, env)
}

func (s *commandServiceBridge) GetDeploymentSpecification(ctx context.Context, execID, hostID string) (*poller.Envelope, string, error) {
	spec, system, err := s.client.GetDeploymentSpecification(ctx, execID, hostID)
	if err != nil {
		return nil, "", err
	}
	if spec == nil || spec.GenericEnvelope == nil {
		return nil, system, nil
	}
	return &poller.Envelope{
		Format:  spec.GenericEnvelope.Format,
		Payload: spec.GenericEnvelope.Payload,
	}, system, nil
}

// specParserBridge adapts deployspec.Parse to poller.SpecParser.
type specParserBridge struct {
	verifier *pkcs7.Verifier
}

func (p *specParserBridge) Parse(env deployspec.Envelope) (deployspec.Spec, error) {
	return deployspec.Parse(env, p.verifier, false)
}
