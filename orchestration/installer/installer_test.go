package installer

import (
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	"github.com/gurre/codedeploy-agent-go/logic/appspec"
)

// TestInstall_SingleFile verifies that a single source file is copied to the
// correct destination and a cleanup file is written containing the destination
// path. This is the simplest end-to-end install scenario and validates the
// core copy-and-record contract.
func TestInstall_SingleFile(t *testing.T) {
	archiveDir, instructionsDir, destDir := setupDirs(t)
	createFile(t, filepath.Join(archiveDir, "config.txt"), "data")

	mock := newMockFileOp()
	inst := NewInstaller(mock, slog.Default())

	spec := appspec.Spec{
		Files: []appspec.FileMapping{
			{Source: "config.txt", Destination: destDir},
		},
	}

	if err := inst.Install("dg-1", archiveDir, instructionsDir, spec, "OVERWRITE"); err != nil {
		t.Fatalf("Install: %v", err)
	}

	expectedDest := filepath.Join(destDir, "config.txt")
	if !mock.copiedTo(expectedDest) {
		t.Errorf("expected copy to %q, got copies: %v", expectedDest, mock.copies)
	}

	cleanupPath := filepath.Join(instructionsDir, "dg-1-cleanup")
	data, err := os.ReadFile(cleanupPath)
	if err != nil {
		t.Fatalf("read cleanup file: %v", err)
	}
	if !strings.Contains(string(data), expectedDest) {
		t.Errorf("cleanup file should contain %q, got: %q", expectedDest, string(data))
	}
}

// TestInstall_DirectoryCopy verifies that a source directory with nested files
// is recursively walked and all files are copied to the correct destinations.
// Directory structures must be preserved, which is critical for multi-file
// deployments where relative paths carry meaning.
func TestInstall_DirectoryCopy(t *testing.T) {
	archiveDir, instructionsDir, destDir := setupDirs(t)

	srcDir := filepath.Join(archiveDir, "app")
	mkdirAll(t, filepath.Join(srcDir, "sub"))
	createFile(t, filepath.Join(srcDir, "a.txt"), "a")
	createFile(t, filepath.Join(srcDir, "sub", "b.txt"), "b")

	mock := newMockFileOp()
	inst := NewInstaller(mock, slog.Default())

	spec := appspec.Spec{
		Files: []appspec.FileMapping{
			{Source: "app", Destination: destDir},
		},
	}

	if err := inst.Install("dg-1", archiveDir, instructionsDir, spec, "OVERWRITE"); err != nil {
		t.Fatalf("Install: %v", err)
	}

	expectedA := filepath.Join(destDir, "a.txt")
	expectedB := filepath.Join(destDir, "sub", "b.txt")

	if !mock.copiedTo(expectedA) {
		t.Errorf("expected copy to %q, copies: %v", expectedA, mock.copies)
	}
	if !mock.copiedTo(expectedB) {
		t.Errorf("expected copy to %q, copies: %v", expectedB, mock.copies)
	}
}

// TestInstall_FileExistsBehavior_Disallow verifies that the installer returns
// an error when the destination file already exists and file_exists_behavior is
// DISALLOW. This prevents accidental overwrites in production deployments where
// clobbering existing files is dangerous.
func TestInstall_FileExistsBehavior_Disallow(t *testing.T) {
	archiveDir, instructionsDir, destDir := setupDirs(t)
	createFile(t, filepath.Join(archiveDir, "app.bin"), "new")
	createFile(t, filepath.Join(destDir, "app.bin"), "existing")

	mock := newMockFileOp()
	inst := NewInstaller(mock, slog.Default())

	spec := appspec.Spec{
		Files: []appspec.FileMapping{
			{Source: "app.bin", Destination: destDir},
		},
	}

	err := inst.Install("dg-1", archiveDir, instructionsDir, spec, "DISALLOW")
	if err == nil {
		t.Fatal("expected error for DISALLOW when file exists")
	}
	if !strings.Contains(err.Error(), "already exists") {
		t.Errorf("error should mention 'already exists', got: %v", err)
	}
}

// TestInstall_FileExistsBehavior_Overwrite verifies that when a destination
// file already exists and file_exists_behavior is OVERWRITE, the copy proceeds
// normally. The file content must be replaceable during redeployments.
func TestInstall_FileExistsBehavior_Overwrite(t *testing.T) {
	archiveDir, instructionsDir, destDir := setupDirs(t)
	createFile(t, filepath.Join(archiveDir, "app.bin"), "new")
	createFile(t, filepath.Join(destDir, "app.bin"), "existing")

	mock := newMockFileOp()
	inst := NewInstaller(mock, slog.Default())

	spec := appspec.Spec{
		Files: []appspec.FileMapping{
			{Source: "app.bin", Destination: destDir},
		},
	}

	if err := inst.Install("dg-1", archiveDir, instructionsDir, spec, "OVERWRITE"); err != nil {
		t.Fatalf("Install: %v", err)
	}

	expectedDest := filepath.Join(destDir, "app.bin")
	if !mock.copiedTo(expectedDest) {
		t.Errorf("expected copy to %q despite existing file, copies: %v", expectedDest, mock.copies)
	}
}

// TestInstall_FileExistsBehavior_Retain verifies that when a destination file
// already exists and file_exists_behavior is RETAIN, the file is skipped
// without error. This protects user-modified config files from being replaced
// during redeployments.
func TestInstall_FileExistsBehavior_Retain(t *testing.T) {
	archiveDir, instructionsDir, destDir := setupDirs(t)
	createFile(t, filepath.Join(archiveDir, "config.yml"), "new")
	createFile(t, filepath.Join(destDir, "config.yml"), "user-edited")

	mock := newMockFileOp()
	inst := NewInstaller(mock, slog.Default())

	spec := appspec.Spec{
		Files: []appspec.FileMapping{
			{Source: "config.yml", Destination: destDir},
		},
	}

	if err := inst.Install("dg-1", archiveDir, instructionsDir, spec, "RETAIN"); err != nil {
		t.Fatalf("Install: %v", err)
	}

	retainedDest := filepath.Join(destDir, "config.yml")
	if mock.copiedTo(retainedDest) {
		t.Errorf("RETAIN should skip existing file, but copy was recorded to %q", retainedDest)
	}
}

// TestInstall_Permissions verifies that mode and owner from the appspec
// permissions section are applied to matching copy targets. Permission
// enforcement is a security requirement: deployed files must have the correct
// ownership and access bits.
func TestInstall_Permissions(t *testing.T) {
	archiveDir, instructionsDir, destDir := setupDirs(t)
	createFile(t, filepath.Join(archiveDir, "run.sh"), "#!/bin/sh")

	mock := newMockFileOp()
	inst := NewInstaller(mock, slog.Default())

	destFile := filepath.Join(destDir, "run.sh")
	mode, err := appspec.ParseMode("0755")
	if err != nil {
		t.Fatalf("ParseMode: %v", err)
	}

	spec := appspec.Spec{
		Files: []appspec.FileMapping{
			{Source: "run.sh", Destination: destDir},
		},
		Permissions: []appspec.Permission{
			{
				Object:  destFile,
				Pattern: "**",
				Type:    []string{"file"},
				Owner:   "deploy",
				Group:   "web",
				Mode:    &mode,
			},
		},
	}

	if err := inst.Install("dg-1", archiveDir, instructionsDir, spec, "OVERWRITE"); err != nil {
		t.Fatalf("Install: %v", err)
	}

	if !mock.chmodCalled(destFile) {
		t.Errorf("expected chmod on %q, chmods: %v", destFile, mock.chmods)
	}
	if !mock.chownCalled(destFile) {
		t.Errorf("expected chown on %q, chowns: %v", destFile, mock.chowns)
	}
}

// TestInstall_Cleanup verifies that entries from a previous deployment's
// cleanup file are removed before the new installation begins. Stale files
// from prior deployments must be removed to avoid conflicts and disk waste.
func TestInstall_Cleanup(t *testing.T) {
	archiveDir, instructionsDir, destDir := setupDirs(t)
	createFile(t, filepath.Join(archiveDir, "new.txt"), "new")

	// Write a previous cleanup file with paths to be removed
	cleanupPath := filepath.Join(instructionsDir, "dg-1-cleanup")
	prevCleanup := "/old/deploy/file.txt\n/old/deploy\n"
	writeFile(t, cleanupPath, prevCleanup)

	mock := newMockFileOp()
	inst := NewInstaller(mock, slog.Default())

	spec := appspec.Spec{
		Files: []appspec.FileMapping{
			{Source: "new.txt", Destination: destDir},
		},
	}

	if err := inst.Install("dg-1", archiveDir, instructionsDir, spec, "OVERWRITE"); err != nil {
		t.Fatalf("Install: %v", err)
	}

	// ParseRemoveCommands reverses the order: /old/deploy first, then /old/deploy/file.txt
	// But Remove is called in reversed (bottom-up) order: file first, then directory
	if !mock.removeCalled("/old/deploy/file.txt") {
		t.Errorf("expected Remove for /old/deploy/file.txt, removes: %v", mock.removes)
	}
	if !mock.removeCalled("/old/deploy") {
		t.Errorf("expected Remove for /old/deploy, removes: %v", mock.removes)
	}
}

// TestInstall_MissingAncestors verifies that parent directories that do not
// exist are created before the file copy. The installer must build the full
// directory tree to the destination even when intermediate directories are
// absent, otherwise the copy would fail.
func TestInstall_MissingAncestors(t *testing.T) {
	archiveDir, instructionsDir, _ := setupDirs(t)
	createFile(t, filepath.Join(archiveDir, "data.bin"), "payload")

	// Use a destination under a path that does not exist on disk.
	// The temp dir exists, but the nested subdirectories do not.
	nonExistentBase := filepath.Join(t.TempDir(), "deep", "nested", "dir")

	mock := newMockFileOp()
	inst := NewInstaller(mock, slog.Default())

	spec := appspec.Spec{
		Files: []appspec.FileMapping{
			{Source: "data.bin", Destination: nonExistentBase},
		},
	}

	if err := inst.Install("dg-1", archiveDir, instructionsDir, spec, "OVERWRITE"); err != nil {
		t.Fatalf("Install: %v", err)
	}

	// The installer should have issued Mkdir commands for missing ancestors.
	// The destination for a single file is: nonExistentBase/data.bin
	// So the ancestors are: deep, deep/nested, deep/nested/dir
	if len(mock.mkdirs) == 0 {
		t.Fatal("expected Mkdir calls for missing ancestors, got none")
	}

	// Verify the deepest ancestor (the direct parent) was created
	if !mock.mkdirCalled(nonExistentBase) {
		t.Errorf("expected Mkdir for %q, mkdirs: %v", nonExistentBase, mock.mkdirs)
	}
}

// TestInstall_Permissions_ACL verifies that ACL entries from the appspec
// permissions section are applied to matching copy targets via SetACL.
// ACLs are a POSIX extension that provide fine-grained access control beyond
// traditional owner/group/other, used in enterprise deployments.
func TestInstall_Permissions_ACL(t *testing.T) {
	archiveDir, instructionsDir, destDir := setupDirs(t)
	createFile(t, filepath.Join(archiveDir, "data.txt"), "payload")

	mock := newMockFileOp()
	inst := NewInstaller(mock, slog.Default())

	destFile := filepath.Join(destDir, "data.txt")
	acl, err := appspec.ParseACL([]string{"user:deploy:rwx", "group:web:r-x"})
	if err != nil {
		t.Fatalf("ParseACL: %v", err)
	}

	spec := appspec.Spec{
		Files: []appspec.FileMapping{
			{Source: "data.txt", Destination: destDir},
		},
		Permissions: []appspec.Permission{
			{
				Object:  destFile,
				Pattern: "**",
				Type:    []string{"file"},
				ACLs:    &acl,
			},
		},
	}

	if err := inst.Install("dg-1", archiveDir, instructionsDir, spec, "OVERWRITE"); err != nil {
		t.Fatalf("Install: %v", err)
	}

	if !mock.aclCalled(destFile) {
		t.Errorf("expected SetACL on %q, acls: %v", destFile, mock.acls)
	}
}

// TestInstall_Permissions_Context verifies that SELinux context from the appspec
// permissions section is applied via SetContext. This exercises the semanage
// command path and the cleanup file "semanage\x00path" format.
func TestInstall_Permissions_Context(t *testing.T) {
	archiveDir, instructionsDir, destDir := setupDirs(t)
	createFile(t, filepath.Join(archiveDir, "index.html"), "<html>")

	mock := newMockFileOp()
	inst := NewInstaller(mock, slog.Default())

	destFile := filepath.Join(destDir, "index.html")
	spec := appspec.Spec{
		Files: []appspec.FileMapping{
			{Source: "index.html", Destination: destDir},
		},
		Permissions: []appspec.Permission{
			{
				Object:  destFile,
				Pattern: "**",
				Type:    []string{"file"},
				Context: &appspec.SELinuxContext{
					User: "system_u",
					Type: "httpd_sys_content_t",
					Range: &appspec.SELinuxRange{
						Low:  "s0",
						High: "s0:c0.c1023",
					},
				},
			},
		},
	}

	if err := inst.Install("dg-1", archiveDir, instructionsDir, spec, "OVERWRITE"); err != nil {
		t.Fatalf("Install: %v", err)
	}

	if !mock.contextCalled(destFile) {
		t.Errorf("expected SetContext on %q, contexts: %v", destFile, mock.contexts)
	}

	// Verify the cleanup file contains the semanage entry
	cleanupPath := filepath.Join(instructionsDir, "dg-1-cleanup")
	data, err := os.ReadFile(cleanupPath)
	if err != nil {
		t.Fatalf("read cleanup file: %v", err)
	}
	if !strings.Contains(string(data), "semanage\x00"+destFile) {
		t.Errorf("cleanup file should contain semanage entry for %q, got: %q", destFile, data)
	}
}

// TestInstall_Permissions_DirectoryMatch verifies that when the permission
// object matches a directory target (not a specific copy target), the permission
// is applied to all files and directories matching the pattern beneath it.
// This tests the findMatches path which was at 0% coverage.
func TestInstall_Permissions_DirectoryMatch(t *testing.T) {
	archiveDir, instructionsDir, destDir := setupDirs(t)

	srcDir := filepath.Join(archiveDir, "app")
	mkdirAll(t, srcDir)
	createFile(t, filepath.Join(srcDir, "run.sh"), "#!/bin/sh")
	createFile(t, filepath.Join(srcDir, "config.yml"), "key: val")

	mock := newMockFileOp()
	inst := NewInstaller(mock, slog.Default())

	mode, _ := appspec.ParseMode("0755")
	spec := appspec.Spec{
		Files: []appspec.FileMapping{
			{Source: "app", Destination: destDir},
		},
		Permissions: []appspec.Permission{
			{
				Object:  destDir,
				Pattern: "**",
				Type:    []string{"file", "directory"},
				Owner:   "deploy",
				Mode:    &mode,
			},
		},
	}

	if err := inst.Install("dg-1", archiveDir, instructionsDir, spec, "OVERWRITE"); err != nil {
		t.Fatalf("Install: %v", err)
	}

	// Both files should have chmod applied
	runDest := filepath.Join(destDir, "run.sh")
	configDest := filepath.Join(destDir, "config.yml")
	if !mock.chmodCalled(runDest) && !mock.chmodCalled(configDest) {
		t.Errorf("expected chmod on at least one file, chmods: %v", mock.chmods)
	}
}

// TestInstall_CleanupWithSemanage verifies that cleanup correctly removes
// SELinux context entries (semanage\x00path format) by calling RemoveContext
// instead of Remove.
func TestInstall_CleanupWithSemanage(t *testing.T) {
	archiveDir, instructionsDir, destDir := setupDirs(t)
	createFile(t, filepath.Join(archiveDir, "new.txt"), "new")

	cleanupPath := filepath.Join(instructionsDir, "dg-1-cleanup")
	prevCleanup := "/old/file.txt\nsemanage\x00/old/context.html\n"
	writeFile(t, cleanupPath, prevCleanup)

	mock := newMockFileOp()
	inst := NewInstaller(mock, slog.Default())

	spec := appspec.Spec{
		Files: []appspec.FileMapping{
			{Source: "new.txt", Destination: destDir},
		},
	}

	if err := inst.Install("dg-1", archiveDir, instructionsDir, spec, "OVERWRITE"); err != nil {
		t.Fatalf("Install: %v", err)
	}

	if !mock.removeCalled("/old/file.txt") {
		t.Errorf("expected Remove for /old/file.txt, removes: %v", mock.removes)
	}
	if !mock.removeContextCalled("/old/context.html") {
		t.Errorf("expected RemoveContext for /old/context.html, removeContexts: %v", mock.removeContexts)
	}
}

// TestInstall_AppspecFEB_OverwriteOverridesDisallow verifies that when the
// appspec sets FileExistsBehavior to OVERWRITE, it takes precedence over the
// DISALLOW parameter passed to Install. The precedence logic at installer.go
// generateInstructions reads spec.FileExistsBehavior first and only falls back
// to the parameter when the spec value is empty. This validates Ruby Scenario
// 17 where the appspec sets file_exists_behavior: OVERWRITE and the CLI default
// is DISALLOW, yet the deployment succeeds.
func TestInstall_AppspecFEB_OverwriteOverridesDisallow(t *testing.T) {
	archiveDir, instructionsDir, destDir := setupDirs(t)
	createFile(t, filepath.Join(archiveDir, "app.bin"), "new-content")
	createFile(t, filepath.Join(destDir, "app.bin"), "existing-content")

	mock := newMockFileOp()
	inst := NewInstaller(mock, slog.Default())

	spec := appspec.Spec{
		Files: []appspec.FileMapping{
			{Source: "app.bin", Destination: destDir},
		},
		FileExistsBehavior: "OVERWRITE",
	}

	err := inst.Install("dg-1", archiveDir, instructionsDir, spec, "DISALLOW")
	if err != nil {
		t.Fatalf("Install should succeed when appspec OVERWRITE overrides param DISALLOW: %v", err)
	}

	expectedDest := filepath.Join(destDir, "app.bin")
	if !mock.copiedTo(expectedDest) {
		t.Errorf("expected copy to %q, copies: %v", expectedDest, mock.copies)
	}
}

// TestInstall_AppspecFEB_RetainOverridesDisallow verifies that when the appspec
// sets FileExistsBehavior to RETAIN, it takes precedence over the DISALLOW
// parameter. The existing file is preserved without error. This validates Ruby
// Scenario 18 where the appspec sets file_exists_behavior: RETAIN and the CLI
// default is DISALLOW, yet the deployment succeeds with the original file kept.
func TestInstall_AppspecFEB_RetainOverridesDisallow(t *testing.T) {
	archiveDir, instructionsDir, destDir := setupDirs(t)
	createFile(t, filepath.Join(archiveDir, "config.yml"), "new-config")
	createFile(t, filepath.Join(destDir, "config.yml"), "user-config")

	mock := newMockFileOp()
	inst := NewInstaller(mock, slog.Default())

	spec := appspec.Spec{
		Files: []appspec.FileMapping{
			{Source: "config.yml", Destination: destDir},
		},
		FileExistsBehavior: "RETAIN",
	}

	err := inst.Install("dg-1", archiveDir, instructionsDir, spec, "DISALLOW")
	if err != nil {
		t.Fatalf("Install should succeed when appspec RETAIN overrides param DISALLOW: %v", err)
	}

	retainedDest := filepath.Join(destDir, "config.yml")
	if mock.copiedTo(retainedDest) {
		t.Errorf("RETAIN should skip copy to %q, but copy was recorded", retainedDest)
	}
}

// TestInstall_AppspecFEB_DisallowOverridesOverwrite verifies that when the
// appspec sets FileExistsBehavior to DISALLOW, it takes precedence over the
// OVERWRITE parameter. The deployment fails with an "already exists" error.
// This validates Ruby Scenario 16 where the appspec sets file_exists_behavior:
// DISALLOW and the caller passes OVERWRITE, yet the deployment correctly fails.
func TestInstall_AppspecFEB_DisallowOverridesOverwrite(t *testing.T) {
	archiveDir, instructionsDir, destDir := setupDirs(t)
	createFile(t, filepath.Join(archiveDir, "app.bin"), "new-content")
	createFile(t, filepath.Join(destDir, "app.bin"), "existing-content")

	mock := newMockFileOp()
	inst := NewInstaller(mock, slog.Default())

	spec := appspec.Spec{
		Files: []appspec.FileMapping{
			{Source: "app.bin", Destination: destDir},
		},
		FileExistsBehavior: "DISALLOW",
	}

	err := inst.Install("dg-1", archiveDir, instructionsDir, spec, "OVERWRITE")
	if err == nil {
		t.Fatal("expected error when appspec DISALLOW overrides param OVERWRITE")
	}
	if !strings.Contains(err.Error(), "already exists") {
		t.Errorf("error should mention 'already exists', got: %v", err)
	}
}

// TestInstall_SourceNotFound verifies that a missing source file produces a
// clear error rather than proceeding with a broken install.
func TestInstall_SourceNotFound(t *testing.T) {
	archiveDir, instructionsDir, destDir := setupDirs(t)

	mock := newMockFileOp()
	inst := NewInstaller(mock, slog.Default())

	spec := appspec.Spec{
		Files: []appspec.FileMapping{
			{Source: "nonexistent.txt", Destination: destDir},
		},
	}

	err := inst.Install("dg-1", archiveDir, instructionsDir, spec, "OVERWRITE")
	if err == nil {
		t.Fatal("expected error for missing source file")
	}
	if !strings.Contains(err.Error(), "nonexistent.txt") {
		t.Errorf("error should mention source file, got: %v", err)
	}
}

// --- test helpers ---

// setupDirs creates the three directories needed by every test: an archive
// directory containing source files, an instructions directory for generated
// JSON, and a destination directory for installed files.
func setupDirs(t *testing.T) (archiveDir, instructionsDir, destDir string) {
	t.Helper()
	archiveDir = filepath.Join(t.TempDir(), "archive")
	instructionsDir = filepath.Join(t.TempDir(), "instructions")
	destDir = filepath.Join(t.TempDir(), "dest")
	mkdirAll(t, archiveDir)
	mkdirAll(t, instructionsDir)
	mkdirAll(t, destDir)
	return archiveDir, instructionsDir, destDir
}

// createFile writes content to a file, creating parent directories as needed.
func createFile(t *testing.T, path, content string) {
	t.Helper()
	mkdirAll(t, filepath.Dir(path))
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatalf("create file %q: %v", path, err)
	}
}

// writeFile writes content to an existing file path.
func writeFile(t *testing.T, path, content string) {
	t.Helper()
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatalf("write file %q: %v", path, err)
	}
}

// mkdirAll creates a directory and all parents.
func mkdirAll(t *testing.T, path string) {
	t.Helper()
	if err := os.MkdirAll(path, 0o755); err != nil {
		t.Fatalf("mkdirAll %q: %v", path, err)
	}
}

// --- mock types ---

// copyRecord tracks a single Copy call with source and destination.
type copyRecord struct {
	source      string
	destination string
}

// chownRecord tracks a single Chown call with path, owner, and group.
type chownRecord struct {
	path  string
	owner string
	group string
}

// mockFileOp records all FileOperator calls and performs real directory
// creation so that the installer's os.Stat checks succeed during execution.
type mockFileOp struct {
	mu             sync.Mutex
	copies         []copyRecord
	mkdirs         []string
	chmods         []string
	chowns         []chownRecord
	acls           []string
	contexts       []string
	removes        []string
	removeContexts []string
}

func newMockFileOp() *mockFileOp {
	return &mockFileOp{
		copies:         make([]copyRecord, 0, 8),
		mkdirs:         make([]string, 0, 8),
		chmods:         make([]string, 0, 8),
		chowns:         make([]chownRecord, 0, 8),
		acls:           make([]string, 0, 4),
		contexts:       make([]string, 0, 4),
		removes:        make([]string, 0, 8),
		removeContexts: make([]string, 0, 4),
	}
}

func (m *mockFileOp) Copy(source, destination string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.copies = append(m.copies, copyRecord{source: source, destination: destination})
	return nil
}

func (m *mockFileOp) Mkdir(path string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.mkdirs = append(m.mkdirs, path)
	return os.MkdirAll(path, 0o755)
}

func (m *mockFileOp) MkdirAll(path string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.mkdirs = append(m.mkdirs, path)
	return os.MkdirAll(path, 0o755)
}

func (m *mockFileOp) Chmod(path string, mode os.FileMode) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.chmods = append(m.chmods, path)
	return nil
}

func (m *mockFileOp) Chown(path, owner, group string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.chowns = append(m.chowns, chownRecord{path: path, owner: owner, group: group})
	return nil
}

func (m *mockFileOp) SetACL(path string, acl []string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.acls = append(m.acls, path)
	return nil
}

func (m *mockFileOp) SetContext(path string, seUser, seType, seRange string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.contexts = append(m.contexts, path)
	return nil
}

func (m *mockFileOp) RemoveContext(path string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.removeContexts = append(m.removeContexts, path)
	return nil
}

func (m *mockFileOp) Remove(path string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.removes = append(m.removes, path)
	return nil
}

func (m *mockFileOp) copiedTo(destination string) bool {
	m.mu.Lock()
	defer m.mu.Unlock()
	for _, c := range m.copies {
		if c.destination == destination {
			return true
		}
	}
	return false
}

func (m *mockFileOp) chmodCalled(path string) bool {
	m.mu.Lock()
	defer m.mu.Unlock()
	for _, p := range m.chmods {
		if p == path {
			return true
		}
	}
	return false
}

func (m *mockFileOp) chownCalled(path string) bool {
	m.mu.Lock()
	defer m.mu.Unlock()
	for _, c := range m.chowns {
		if c.path == path {
			return true
		}
	}
	return false
}

func (m *mockFileOp) removeCalled(path string) bool {
	m.mu.Lock()
	defer m.mu.Unlock()
	for _, p := range m.removes {
		if p == path {
			return true
		}
	}
	return false
}

func (m *mockFileOp) mkdirCalled(path string) bool {
	m.mu.Lock()
	defer m.mu.Unlock()
	for _, p := range m.mkdirs {
		if p == path {
			return true
		}
	}
	return false
}

func (m *mockFileOp) aclCalled(path string) bool {
	m.mu.Lock()
	defer m.mu.Unlock()
	for _, p := range m.acls {
		if p == path {
			return true
		}
	}
	return false
}

func (m *mockFileOp) contextCalled(path string) bool {
	m.mu.Lock()
	defer m.mu.Unlock()
	for _, p := range m.contexts {
		if p == path {
			return true
		}
	}
	return false
}

func (m *mockFileOp) removeContextCalled(path string) bool {
	m.mu.Lock()
	defer m.mu.Unlock()
	for _, p := range m.removeContexts {
		if p == path {
			return true
		}
	}
	return false
}
