// Package selfinstall wires the self-installation flow: probes current system
// state, computes the delta via the logic layer, and executes actions through
// the orchestration layer.
package selfinstall

import (
	"context"
	"crypto/sha256"
	"fmt"
	"io"
	"log/slog"
	"os"
	"os/exec"
	"path/filepath"

	"github.com/gurre/codedeploy-agent-go/adaptor/servicefile"
	"github.com/gurre/codedeploy-agent-go/logic/selfinstall"
	orchinstall "github.com/gurre/codedeploy-agent-go/orchestration/selfinstall"
	"github.com/gurre/codedeploy-agent-go/state/config"
	"gopkg.in/yaml.v3"
)

// Options controls the self-installation behavior.
type Options struct {
	// InstallDir is the root installation directory (default: /opt/codedeploy-agent).
	InstallDir string
	// NoStart skips starting the service after installation.
	NoStart bool
}

// DefaultOptions returns options with production defaults.
//
//	opts := selfinstall.DefaultOptions()
//	opts.InstallDir = "/tmp/test-install"
func DefaultOptions() Options {
	return Options{
		InstallDir: "/opt/codedeploy-agent",
	}
}

// Run performs the self-installation: probes the system, computes needed
// actions via declarative reconciliation, and executes them.
//
//	err := selfinstall.Run(ctx, selfinstall.DefaultOptions())
func Run(ctx context.Context, opts Options) error {
	logger := slog.Default()

	// Detect init system.
	_, systemdErr := os.Stat("/run/systemd/system")
	_, initdErr := os.Stat("/etc/init.d")
	initSys := selfinstall.DetectInitSystem(systemdErr == nil, initdErr == nil)

	logger.Info("detected init system", "type", initSys)

	// Select service file content.
	var serviceFileContent []byte
	switch initSys {
	case selfinstall.Systemd:
		serviceFileContent = servicefile.SystemdUnit()
	case selfinstall.SysV:
		serviceFileContent = servicefile.SysVScript()
	}

	// Generate default config content.
	configContent, err := defaultConfigYAML()
	if err != nil {
		return fmt.Errorf("selfinstall: generate config: %w", err)
	}

	// Build manifest.
	m := selfinstall.DefaultManifest(opts.InstallDir, initSys, serviceFileContent, configContent)

	// Probe current state.
	state, err := probeState(m)
	if err != nil {
		return fmt.Errorf("selfinstall: probe state: %w", err)
	}

	// Compute actions.
	steps := selfinstall.Reconcile(m, state)

	// Filter out start if --no-start.
	if opts.NoStart {
		steps = filterOutAction(steps, selfinstall.StartService)
	}

	if len(steps) == 0 {
		logger.Info("system already matches desired state, nothing to do")
		return nil
	}

	logger.Info("reconciliation plan", "steps", len(steps))
	for i, s := range steps {
		logger.Info("step", "index", i, "action", s.Action, "path", s.Path)
	}

	// Resolve self binary path.
	selfBin, err := os.Executable()
	if err != nil {
		return fmt.Errorf("selfinstall: resolve self binary: %w", err)
	}
	selfBin, err = filepath.EvalSymlinks(selfBin)
	if err != nil {
		return fmt.Errorf("selfinstall: resolve self symlinks: %w", err)
	}

	// Build orchestration components.
	files := &osFileInstaller{}
	svc := &osServiceController{initSys: initSys}
	reconciler := orchinstall.NewReconciler(files, svc, selfBin, logger)

	return reconciler.Install(ctx, m, steps)
}

// probeState queries the filesystem and service state to build a State struct
// for the reconciliation logic.
func probeState(m selfinstall.Manifest) (selfinstall.State, error) {
	s := selfinstall.State{
		DirsExist: make(map[string]bool, len(m.Dirs)),
	}

	for _, dir := range m.Dirs {
		_, err := os.Stat(dir)
		s.DirsExist[dir] = err == nil
	}

	info, err := os.Stat(m.BinaryPath)
	s.BinaryExists = err == nil

	if s.BinaryExists && info.Mode().IsRegular() {
		selfBin, err := os.Executable()
		if err == nil {
			selfBin, _ = filepath.EvalSymlinks(selfBin)
			s.BinaryMatchesHash = fileHashesMatch(selfBin, m.BinaryPath)
		}
	}

	_, err = os.Stat(m.ServiceFilePath)
	s.ServiceFileExists = err == nil

	_, err = os.Stat(m.ConfigPath)
	s.ConfigExists = err == nil

	// Probe service status (best effort — systemctl/chkconfig may not exist).
	s.ServiceEnabled = isServiceEnabled(m.ServiceName, m.InitSystem)
	s.ServiceRunning = isServiceRunning(m.ServiceName, m.InitSystem)

	return s, nil
}

// fileHashesMatch returns true when both files exist and have identical
// SHA-256 content hashes.
func fileHashesMatch(pathA, pathB string) bool {
	hashA, errA := fileHash(pathA)
	hashB, errB := fileHash(pathB)
	if errA != nil || errB != nil {
		return false
	}
	return hashA == hashB
}

func fileHash(path string) ([sha256.Size]byte, error) {
	f, err := os.Open(path)
	if err != nil {
		return [sha256.Size]byte{}, err
	}
	defer func() { _ = f.Close() }()

	h := sha256.New()
	if _, err := io.Copy(h, f); err != nil {
		return [sha256.Size]byte{}, err
	}
	var sum [sha256.Size]byte
	copy(sum[:], h.Sum(nil))
	return sum, nil
}

func isServiceEnabled(name string, initSys selfinstall.InitSystem) bool {
	switch initSys {
	case selfinstall.Systemd:
		err := exec.Command("systemctl", "is-enabled", "--quiet", name).Run()
		return err == nil
	case selfinstall.SysV:
		err := exec.Command("chkconfig", "--list", name).Run()
		return err == nil
	}
	return false
}

func isServiceRunning(name string, initSys selfinstall.InitSystem) bool {
	switch initSys {
	case selfinstall.Systemd:
		err := exec.Command("systemctl", "is-active", "--quiet", name).Run()
		return err == nil
	case selfinstall.SysV:
		err := exec.Command("service", name, "status").Run()
		return err == nil
	}
	return false
}

func filterOutAction(steps []selfinstall.Step, action selfinstall.Action) []selfinstall.Step {
	filtered := make([]selfinstall.Step, 0, len(steps))
	for _, s := range steps {
		if s.Action != action {
			filtered = append(filtered, s)
		}
	}
	return filtered
}

// configYAML mirrors the subset of config fields written as a default config
// file. Uses plain YAML keys since the Go agent is the primary consumer.
type configYAML struct {
	RootDir                   string `yaml:"root_dir"`
	LogDir                    string `yaml:"log_dir"`
	OngoingDeploymentTracking string `yaml:"ongoing_deployment_tracking"`
	OnPremisesConfigFile      string `yaml:"on_premises_config_file"`
	WaitBetweenRuns           int    `yaml:"wait_between_runs"`
	MaxRevisions              int    `yaml:"max_revisions"`
	EnableDeploymentsLog      bool   `yaml:"enable_deployments_log"`
}

func defaultConfigYAML() ([]byte, error) {
	cfg := config.Default()
	raw := configYAML{
		RootDir:                   cfg.RootDir,
		LogDir:                    cfg.LogDir,
		OngoingDeploymentTracking: cfg.OngoingDeploymentTracking,
		OnPremisesConfigFile:      cfg.OnPremisesConfigFile,
		WaitBetweenRuns:           int(cfg.PollInterval.Seconds()),
		MaxRevisions:              cfg.MaxRevisions,
		EnableDeploymentsLog:      cfg.EnableDeploymentsLog,
	}
	return yaml.Marshal(raw)
}

// osFileInstaller implements FileInstaller using the os package.
type osFileInstaller struct{}

func (o *osFileInstaller) MkdirAll(path string) error {
	return os.MkdirAll(path, 0o755)
}

func (o *osFileInstaller) WriteFile(path string, data []byte, perm os.FileMode) error {
	return os.WriteFile(path, data, perm)
}

func (o *osFileInstaller) CopyFile(src, dst string, perm os.FileMode) error {
	in, err := os.Open(src)
	if err != nil {
		return err
	}
	defer func() { _ = in.Close() }()

	out, err := os.OpenFile(dst, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, perm)
	if err != nil {
		return err
	}
	defer func() { _ = out.Close() }()

	if _, err := io.Copy(out, in); err != nil {
		return err
	}
	return out.Close()
}

func (o *osFileInstaller) Rename(oldpath, newpath string) error {
	return os.Rename(oldpath, newpath)
}

// osServiceController implements ServiceController using system commands.
type osServiceController struct {
	initSys selfinstall.InitSystem
}

func (s *osServiceController) Enable(ctx context.Context, name string) error {
	switch s.initSys {
	case selfinstall.Systemd:
		return exec.CommandContext(ctx, "systemctl", "enable", name).Run()
	case selfinstall.SysV:
		return exec.CommandContext(ctx, "chkconfig", name, "on").Run()
	}
	return fmt.Errorf("unknown init system")
}

func (s *osServiceController) Start(ctx context.Context, name string) error {
	switch s.initSys {
	case selfinstall.Systemd:
		return exec.CommandContext(ctx, "systemctl", "start", name).Run()
	case selfinstall.SysV:
		return exec.CommandContext(ctx, "service", name, "start").Run()
	}
	return fmt.Errorf("unknown init system")
}

func (s *osServiceController) DaemonReload(ctx context.Context) error {
	if s.initSys == selfinstall.Systemd {
		return exec.CommandContext(ctx, "systemctl", "daemon-reload").Run()
	}
	return nil
}
