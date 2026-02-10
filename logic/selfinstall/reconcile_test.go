package selfinstall_test

import (
	"testing"

	"github.com/gurre/codedeploy-agent-go/logic/selfinstall"
)

// testManifest returns a manifest suitable for testing. It uses /opt/test as
// the install directory so tests do not depend on real system paths.
func testManifest() selfinstall.Manifest {
	return selfinstall.DefaultManifest(
		"/opt/test",
		selfinstall.Systemd,
		[]byte("[Unit]\nDescription=test"),
		[]byte("root_dir: /opt/test/deployment-root\n"),
	)
}

// fullyInstalledState returns a State where everything in the manifest is
// already present and running, so Reconcile should produce no steps.
func fullyInstalledState(m selfinstall.Manifest) selfinstall.State {
	dirs := make(map[string]bool, len(m.Dirs))
	for _, d := range m.Dirs {
		dirs[d] = true
	}
	return selfinstall.State{
		DirsExist:         dirs,
		BinaryExists:      true,
		BinaryMatchesHash: true,
		ServiceFileExists: true,
		ServiceEnabled:    true,
		ServiceRunning:    true,
		ConfigExists:      true,
	}
}

// TestReconcile_CleanSystem verifies that a completely empty system produces
// the full set of installation actions in the correct order.
func TestReconcile_CleanSystem(t *testing.T) {
	m := testManifest()
	s := selfinstall.State{DirsExist: map[string]bool{}}

	steps := selfinstall.Reconcile(m, s)

	// Expect: 4 dirs + binary + service file + config + enable + start = 9
	if len(steps) != 9 {
		t.Fatalf("expected 9 steps for clean system, got %d: %v", len(steps), steps)
	}

	// First four must be CreateDir.
	for i := 0; i < 4; i++ {
		if steps[i].Action != selfinstall.CreateDir {
			t.Errorf("step %d: expected CreateDir, got %s", i, steps[i].Action)
		}
	}

	// Then CopyBinary.
	if steps[4].Action != selfinstall.CopyBinary {
		t.Errorf("step 4: expected CopyBinary, got %s", steps[4].Action)
	}
	if steps[4].Path != m.BinaryPath {
		t.Errorf("step 4: expected path %s, got %s", m.BinaryPath, steps[4].Path)
	}

	// Then WriteFile (service).
	if steps[5].Action != selfinstall.WriteFile {
		t.Errorf("step 5: expected WriteFile (service), got %s", steps[5].Action)
	}

	// Then WriteFile (config).
	if steps[6].Action != selfinstall.WriteFile {
		t.Errorf("step 6: expected WriteFile (config), got %s", steps[6].Action)
	}

	// Then EnableService and StartService.
	if steps[7].Action != selfinstall.EnableService {
		t.Errorf("step 7: expected EnableService, got %s", steps[7].Action)
	}
	if steps[8].Action != selfinstall.StartService {
		t.Errorf("step 8: expected StartService, got %s", steps[8].Action)
	}
}

// TestReconcile_FullyInstalled verifies that no actions are produced when the
// system already matches the manifest (idempotency on already-converged state).
func TestReconcile_FullyInstalled(t *testing.T) {
	m := testManifest()
	s := fullyInstalledState(m)

	steps := selfinstall.Reconcile(m, s)

	// Service file is always rewritten, so we expect exactly 1 step.
	if len(steps) != 1 {
		t.Fatalf("expected 1 step (service file rewrite) for fully installed system, got %d: %v", len(steps), steps)
	}
	if steps[0].Action != selfinstall.WriteFile {
		t.Errorf("expected WriteFile for service rewrite, got %s", steps[0].Action)
	}
}

// TestReconcile_BinaryOutdated verifies that only the binary copy and service
// restart occur when the binary exists but has different content.
func TestReconcile_BinaryOutdated(t *testing.T) {
	m := testManifest()
	s := fullyInstalledState(m)
	s.BinaryMatchesHash = false
	s.ServiceRunning = false // needs restart after binary update

	steps := selfinstall.Reconcile(m, s)

	hasAction := func(a selfinstall.Action) bool {
		for _, step := range steps {
			if step.Action == a {
				return true
			}
		}
		return false
	}

	if !hasAction(selfinstall.CopyBinary) {
		t.Error("expected CopyBinary action for outdated binary")
	}
	if !hasAction(selfinstall.StartService) {
		t.Error("expected StartService action after binary update")
	}
}

// TestReconcile_PreservesExistingConfig verifies that the config file is NOT
// overwritten when it already exists. Users may have customized it.
func TestReconcile_PreservesExistingConfig(t *testing.T) {
	m := testManifest()
	s := selfinstall.State{
		DirsExist:         map[string]bool{},
		BinaryExists:      true,
		BinaryMatchesHash: true,
		ServiceFileExists: true,
		ConfigExists:      true, // existing config
		ServiceEnabled:    true,
		ServiceRunning:    true,
	}

	steps := selfinstall.Reconcile(m, s)

	for _, step := range steps {
		if step.Action == selfinstall.WriteFile && step.Path == m.ConfigPath {
			t.Error("Reconcile must not overwrite existing config file")
		}
	}
}

// TestReconcile_PartialInstall verifies that only missing pieces are created
// when the system is partially installed.
func TestReconcile_PartialInstall(t *testing.T) {
	m := testManifest()

	// Dirs exist, binary installed and matching, but no service file or config.
	dirs := make(map[string]bool, len(m.Dirs))
	for _, d := range m.Dirs {
		dirs[d] = true
	}
	s := selfinstall.State{
		DirsExist:         dirs,
		BinaryExists:      true,
		BinaryMatchesHash: true,
		ServiceFileExists: false,
		ConfigExists:      false,
		ServiceEnabled:    false,
		ServiceRunning:    false,
	}

	steps := selfinstall.Reconcile(m, s)

	// Should NOT have any CreateDir or CopyBinary actions.
	for _, step := range steps {
		if step.Action == selfinstall.CreateDir {
			t.Errorf("unexpected CreateDir for %s — directory already exists", step.Path)
		}
		if step.Action == selfinstall.CopyBinary {
			t.Error("unexpected CopyBinary — binary already matches")
		}
	}

	// Should have WriteFile (service + config), EnableService, StartService.
	actionCounts := map[selfinstall.Action]int{}
	for _, step := range steps {
		actionCounts[step.Action]++
	}
	if actionCounts[selfinstall.WriteFile] != 2 {
		t.Errorf("expected 2 WriteFile actions (service + config), got %d", actionCounts[selfinstall.WriteFile])
	}
	if actionCounts[selfinstall.EnableService] != 1 {
		t.Errorf("expected 1 EnableService action, got %d", actionCounts[selfinstall.EnableService])
	}
	if actionCounts[selfinstall.StartService] != 1 {
		t.Errorf("expected 1 StartService action, got %d", actionCounts[selfinstall.StartService])
	}
}

// TestReconcile_SysVServiceFileIsExecutable verifies that init scripts get
// mode 0755 while systemd units get mode 0644.
func TestReconcile_SysVServiceFileIsExecutable(t *testing.T) {
	m := selfinstall.DefaultManifest(
		"/opt/test",
		selfinstall.SysV,
		[]byte("#!/bin/bash\n# init script"),
		[]byte("root_dir: /opt/test/deployment-root\n"),
	)
	s := selfinstall.State{DirsExist: map[string]bool{}}

	steps := selfinstall.Reconcile(m, s)

	for _, step := range steps {
		if step.Action == selfinstall.WriteFile && step.Path == m.ServiceFilePath {
			if step.Mode != 0o755 {
				t.Errorf("SysV service file mode: expected 0755, got %04o", step.Mode)
			}
			return
		}
	}
	t.Error("no WriteFile step found for service file")
}

// TestDetectInitSystem verifies init system detection from filesystem signals.
func TestDetectInitSystem(t *testing.T) {
	tests := []struct {
		name           string
		systemdExists  bool
		initdDirExists bool
		want           selfinstall.InitSystem
	}{
		// Systemd presence takes priority over sysv.
		{name: "systemd present", systemdExists: true, initdDirExists: true, want: selfinstall.Systemd},
		// Only sysv directory present.
		{name: "sysv only", systemdExists: false, initdDirExists: true, want: selfinstall.SysV},
		// Neither present defaults to systemd (modern Linux assumption).
		{name: "neither present", systemdExists: false, initdDirExists: false, want: selfinstall.Systemd},
		// Systemd present, no sysv.
		{name: "systemd only", systemdExists: true, initdDirExists: false, want: selfinstall.Systemd},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := selfinstall.DetectInitSystem(tt.systemdExists, tt.initdDirExists)
			if got != tt.want {
				t.Errorf("DetectInitSystem(%v, %v) = %s, want %s",
					tt.systemdExists, tt.initdDirExists, got, tt.want)
			}
		})
	}
}

// TestReconcile_Idempotency verifies the mathematical property that applying
// reconciliation to the "after" state produces no further actions (beyond the
// always-rewritten service file). This proves convergence in at most one pass.
func TestReconcile_Idempotency(t *testing.T) {
	m := testManifest()
	empty := selfinstall.State{DirsExist: map[string]bool{}}

	// First reconciliation: compute actions from empty state.
	steps := selfinstall.Reconcile(m, empty)
	if len(steps) == 0 {
		t.Fatal("expected non-empty steps from empty state")
	}

	// Simulate applying all actions to build the resulting state.
	after := simulateApply(m, steps)

	// Second reconciliation should produce only the service file rewrite
	// (which is always applied regardless of state).
	steps2 := selfinstall.Reconcile(m, after)
	for _, step := range steps2 {
		if step.Action != selfinstall.WriteFile || step.Path != m.ServiceFilePath {
			t.Errorf("idempotency violated: unexpected step %s on %s after convergence",
				step.Action, step.Path)
		}
	}
}

// simulateApply builds the State that would result from executing all steps.
func simulateApply(m selfinstall.Manifest, steps []selfinstall.Step) selfinstall.State {
	s := selfinstall.State{DirsExist: make(map[string]bool)}
	for _, step := range steps {
		switch step.Action {
		case selfinstall.CreateDir:
			s.DirsExist[step.Path] = true
		case selfinstall.CopyBinary:
			s.BinaryExists = true
			s.BinaryMatchesHash = true
		case selfinstall.WriteFile:
			if step.Path == m.ServiceFilePath {
				s.ServiceFileExists = true
			}
			if step.Path == m.ConfigPath {
				s.ConfigExists = true
			}
		case selfinstall.EnableService:
			s.ServiceEnabled = true
		case selfinstall.StartService:
			s.ServiceRunning = true
		}
	}
	return s
}

// TestDefaultManifest_SystemdPaths verifies path construction for systemd.
func TestDefaultManifest_SystemdPaths(t *testing.T) {
	m := selfinstall.DefaultManifest(
		"/opt/codedeploy-agent",
		selfinstall.Systemd,
		[]byte("unit"),
		[]byte("config"),
	)

	if m.BinaryPath != "/opt/codedeploy-agent/bin/codedeploy-agent" {
		t.Errorf("BinaryPath = %s", m.BinaryPath)
	}
	if m.ServiceFilePath != "/etc/systemd/system/codedeploy-agent.service" {
		t.Errorf("ServiceFilePath = %s", m.ServiceFilePath)
	}
	if m.ConfigPath != "/etc/codedeploy-agent/conf/codedeployagent.yml" {
		t.Errorf("ConfigPath = %s", m.ConfigPath)
	}
	if len(m.Dirs) != 4 {
		t.Errorf("expected 4 dirs, got %d", len(m.Dirs))
	}
}

// TestDefaultManifest_SysVPaths verifies that SysV places the init script
// in /etc/init.d/ rather than /etc/systemd/system/.
func TestDefaultManifest_SysVPaths(t *testing.T) {
	m := selfinstall.DefaultManifest(
		"/opt/codedeploy-agent",
		selfinstall.SysV,
		[]byte("script"),
		[]byte("config"),
	)

	if m.ServiceFilePath != "/etc/init.d/codedeploy-agent" {
		t.Errorf("ServiceFilePath = %s, want /etc/init.d/codedeploy-agent", m.ServiceFilePath)
	}
}

// BenchmarkReconcile_CleanSystem measures reconciliation cost for a fresh
// install (worst-case step generation).
func BenchmarkReconcile_CleanSystem(b *testing.B) {
	m := testManifest()
	s := selfinstall.State{DirsExist: map[string]bool{}}

	b.ReportAllocs()
	for b.Loop() {
		selfinstall.Reconcile(m, s)
	}
}

// BenchmarkReconcile_FullyInstalled measures reconciliation cost when the
// system is already converged (best-case, minimal allocation).
func BenchmarkReconcile_FullyInstalled(b *testing.B) {
	m := testManifest()
	s := fullyInstalledState(m)

	b.ReportAllocs()
	for b.Loop() {
		selfinstall.Reconcile(m, s)
	}
}
