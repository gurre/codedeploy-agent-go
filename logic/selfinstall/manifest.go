// Package selfinstall declares the desired installed state of the CodeDeploy
// agent and computes the delta between current and desired state. All functions
// are pure — no I/O, no side effects.
package selfinstall

import "path/filepath"

// InitSystem identifies the init system in use.
type InitSystem int

const (
	Systemd InitSystem = iota
	SysV
)

// String returns the human-readable name of the init system.
func (s InitSystem) String() string {
	switch s {
	case Systemd:
		return "systemd"
	case SysV:
		return "sysv"
	default:
		return "unknown"
	}
}

// Action describes a single installation step to bring the system from
// current state to the desired state declared in a Manifest.
type Action int

const (
	CreateDir     Action = iota // Create a directory (with parents)
	CopyBinary                  // Copy the running binary to the install path
	WriteFile                   // Write a file (service unit, config, etc.)
	EnableService               // Enable the service in the init system
	StartService                // Start the service
)

// String returns the human-readable name of the action.
func (a Action) String() string {
	switch a {
	case CreateDir:
		return "create-dir"
	case CopyBinary:
		return "copy-binary"
	case WriteFile:
		return "write-file"
	case EnableService:
		return "enable-service"
	case StartService:
		return "start-service"
	default:
		return "unknown"
	}
}

// Step pairs an Action with a target path and optional content.
// Content is only set for WriteFile actions.
type Step struct {
	Action  Action
	Path    string
	Content []byte
	Mode    uint32 // file mode bits (e.g. 0o755)
}

// Manifest declares the desired installed state of the agent.
// Fields are aligned from largest to smallest for memory efficiency.
type Manifest struct {
	// ServiceFilePath is the destination for the init/systemd file.
	ServiceFilePath string
	// ServiceFileContent is the rendered service file bytes.
	ServiceFileContent []byte
	// BinaryPath is the full path to the installed binary.
	BinaryPath string
	// ConfigPath is the path to the YAML config file.
	ConfigPath string
	// ConfigContent is the default YAML config to write (only if absent).
	ConfigContent []byte
	// ServiceName is the service unit/init script name.
	ServiceName string
	// Dirs lists directories that must exist.
	Dirs []string
	// InitSystem is the detected init system.
	InitSystem InitSystem
}

// DefaultManifest returns the standard manifest for a given install directory
// and init system. The serviceFile and configContent are provided by the
// caller (adaptor layer embeds the actual file bytes).
//
//	m := selfinstall.DefaultManifest("/opt/codedeploy-agent", selfinstall.Systemd, unitBytes, cfgBytes)
func DefaultManifest(installDir string, initSys InitSystem, serviceFile, configContent []byte) Manifest {
	binPath := filepath.Join(installDir, "bin", "codedeploy-agent")
	configDir := "/etc/codedeploy-agent/conf"
	configPath := filepath.Join(configDir, "codedeployagent.yml")
	logDir := "/var/log/aws/codedeploy-agent"
	deployRoot := filepath.Join(installDir, "deployment-root")

	var serviceFilePath string
	switch initSys {
	case Systemd:
		serviceFilePath = "/etc/systemd/system/codedeploy-agent.service"
	case SysV:
		serviceFilePath = "/etc/init.d/codedeploy-agent"
	}

	return Manifest{
		ServiceFilePath:    serviceFilePath,
		ServiceFileContent: serviceFile,
		BinaryPath:         binPath,
		ConfigPath:         configPath,
		ConfigContent:      configContent,
		ServiceName:        "codedeploy-agent",
		Dirs: []string{
			filepath.Join(installDir, "bin"),
			deployRoot,
			logDir,
			configDir,
		},
		InitSystem: initSys,
	}
}

// DetectInitSystem returns the init system based on filesystem probes.
// Both arguments are the result of os.Stat-like existence checks performed
// by the caller — this function is pure.
//
//	initSys := selfinstall.DetectInitSystem(systemdRunExists, initdDirExists)
func DetectInitSystem(systemdRunExists, initdDirExists bool) InitSystem {
	if systemdRunExists {
		return Systemd
	}
	if initdDirExists {
		return SysV
	}
	// Default to systemd when neither signal is present (modern Linux).
	return Systemd
}
