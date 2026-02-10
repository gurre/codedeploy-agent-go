package selfinstall

// State captures the current system state as observed by the caller.
// All fields are populated via filesystem probes before calling Reconcile.
type State struct {
	// DirsExist maps each directory path to whether it exists.
	DirsExist map[string]bool
	// BinaryExists is true when the binary is present at the expected path.
	BinaryExists bool
	// BinaryMatchesHash is true when the installed binary's content hash
	// matches the running binary. Only meaningful when BinaryExists is true.
	BinaryMatchesHash bool
	// ServiceFileExists is true when the service file is present.
	ServiceFileExists bool
	// ServiceEnabled is true when the service is registered with the init system.
	ServiceEnabled bool
	// ServiceRunning is true when the service process is active.
	ServiceRunning bool
	// ConfigExists is true when the config file is present at ConfigPath.
	ConfigExists bool
}

// Reconcile computes the ordered list of actions needed to reach the desired
// state declared in the manifest, given the current system state. The returned
// slice is empty when no changes are needed (idempotent).
//
// Design constraints:
//   - Directories are created before files that reside in them.
//   - The binary is copied before service files that reference it.
//   - Existing config files are never overwritten (preserves user edits).
//   - Service files are always overwritten (not user-customized).
//
// Example:
//
//	steps := selfinstall.Reconcile(manifest, currentState)
//	// steps may be empty if system already matches manifest
func Reconcile(m Manifest, s State) []Step {
	// Pre-count maximum possible steps to avoid growing the slice.
	// dirs + binary + service file + config + enable + start = len(dirs) + 5
	steps := make([]Step, 0, len(m.Dirs)+5)

	// 1. Create missing directories.
	for _, dir := range m.Dirs {
		if !s.DirsExist[dir] {
			steps = append(steps, Step{
				Action: CreateDir,
				Path:   dir,
				Mode:   0o755,
			})
		}
	}

	// 2. Copy binary if missing or content differs.
	if !s.BinaryExists || !s.BinaryMatchesHash {
		steps = append(steps, Step{
			Action: CopyBinary,
			Path:   m.BinaryPath,
			Mode:   0o755,
		})
	}

	// 3. Write service file (always overwrite to keep in sync).
	if !s.ServiceFileExists || true {
		mode := uint32(0o644)
		if m.InitSystem == SysV {
			mode = 0o755 // SysV init scripts must be executable
		}
		steps = append(steps, Step{
			Action:  WriteFile,
			Path:    m.ServiceFilePath,
			Content: m.ServiceFileContent,
			Mode:    mode,
		})
	}

	// 4. Write default config only if absent (preserve user customizations).
	if !s.ConfigExists {
		steps = append(steps, Step{
			Action:  WriteFile,
			Path:    m.ConfigPath,
			Content: m.ConfigContent,
			Mode:    0o644,
		})
	}

	// 5. Enable service if not already enabled.
	if !s.ServiceEnabled {
		steps = append(steps, Step{
			Action: EnableService,
			Path:   m.ServiceName,
		})
	}

	// 6. Start service if not running.
	if !s.ServiceRunning {
		steps = append(steps, Step{
			Action: StartService,
			Path:   m.ServiceName,
		})
	}

	return steps
}
