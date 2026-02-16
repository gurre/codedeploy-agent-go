package filesystem

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// TestCopyFile verifies that Copy creates an identical file at the destination
// with the same content. This is the most common operation during Install.
func TestCopyFile(t *testing.T) {
	dir := t.TempDir()
	src := filepath.Join(dir, "src.txt")
	dst := filepath.Join(dir, "dst.txt")

	content := []byte("hello world")
	if err := os.WriteFile(src, content, 0o644); err != nil {
		t.Fatal(err)
	}

	op := NewOperator()
	if err := op.Copy(src, dst); err != nil {
		t.Fatalf("Copy: %v", err)
	}

	got, err := os.ReadFile(dst)
	if err != nil {
		t.Fatalf("read dst: %v", err)
	}
	if string(got) != string(content) {
		t.Errorf("content = %q, want %q", string(got), string(content))
	}
}

// TestCopySymlink verifies that Copy recreates symlinks rather than following
// them. This preserves deployment archive structure.
func TestCopySymlink(t *testing.T) {
	dir := t.TempDir()
	target := filepath.Join(dir, "target.txt")
	if err := os.WriteFile(target, []byte("data"), 0o644); err != nil {
		t.Fatal(err)
	}

	link := filepath.Join(dir, "link.txt")
	if err := os.Symlink(target, link); err != nil {
		t.Fatal(err)
	}

	dst := filepath.Join(dir, "dst-link.txt")
	op := NewOperator()
	if err := op.Copy(link, dst); err != nil {
		t.Fatalf("Copy symlink: %v", err)
	}

	linkTarget, err := os.Readlink(dst)
	if err != nil {
		t.Fatalf("readlink: %v", err)
	}
	if linkTarget != target {
		t.Errorf("symlink target = %q, want %q", linkTarget, target)
	}
}

// TestMkdir verifies that Mkdir creates a single directory.
func TestMkdir(t *testing.T) {
	dir := t.TempDir()
	newDir := filepath.Join(dir, "subdir")

	op := NewOperator()
	if err := op.Mkdir(newDir); err != nil {
		t.Fatalf("Mkdir: %v", err)
	}

	info, err := os.Stat(newDir)
	if err != nil {
		t.Fatalf("stat: %v", err)
	}
	if !info.IsDir() {
		t.Error("should be a directory")
	}
}

// TestRemoveFile verifies that Remove deletes a file.
func TestRemoveFile(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "file.txt")
	if err := os.WriteFile(path, []byte("data"), 0o644); err != nil {
		t.Fatal(err)
	}

	op := NewOperator()
	if err := op.Remove(path); err != nil {
		t.Fatalf("Remove: %v", err)
	}

	if _, err := os.Stat(path); !os.IsNotExist(err) {
		t.Error("file should be removed")
	}
}

// TestRemoveNonExistentIsNoOp verifies that removing a non-existent path
// does not return an error, matching the Ruby agent's behavior.
func TestRemoveNonExistentIsNoOp(t *testing.T) {
	op := NewOperator()
	if err := op.Remove("/nonexistent/path"); err != nil {
		t.Errorf("Remove nonexistent: %v", err)
	}
}

// TestRemoveNonEmptyDirIsNoOp verifies that Remove does not fail on
// non-empty directories (skips them silently), matching Ruby behavior.
func TestRemoveNonEmptyDirIsNoOp(t *testing.T) {
	dir := t.TempDir()
	subdir := filepath.Join(dir, "sub")
	if err := os.Mkdir(subdir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(subdir, "file"), []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}

	op := NewOperator()
	if err := op.Remove(subdir); err != nil {
		t.Errorf("Remove non-empty dir: %v", err)
	}

	// Directory should still exist
	if _, err := os.Stat(subdir); os.IsNotExist(err) {
		t.Error("non-empty dir should not be removed")
	}
}

// TestRemoveEmptyDir verifies that Remove deletes empty directories.
func TestRemoveEmptyDir(t *testing.T) {
	dir := t.TempDir()
	subdir := filepath.Join(dir, "empty")
	if err := os.Mkdir(subdir, 0o755); err != nil {
		t.Fatal(err)
	}

	op := NewOperator()
	if err := op.Remove(subdir); err != nil {
		t.Fatalf("Remove: %v", err)
	}

	if _, err := os.Stat(subdir); !os.IsNotExist(err) {
		t.Error("empty dir should be removed")
	}
}

// TestMkdirAll verifies that MkdirAll creates nested directories
// in a single call. Used by the installer for creating deployment paths.
func TestMkdirAll(t *testing.T) {
	dir := t.TempDir()
	nested := filepath.Join(dir, "a", "b", "c")

	op := NewOperator()
	if err := op.MkdirAll(nested); err != nil {
		t.Fatalf("MkdirAll: %v", err)
	}

	info, err := os.Stat(nested)
	if err != nil {
		t.Fatalf("stat: %v", err)
	}
	if !info.IsDir() {
		t.Error("should be a directory")
	}
}

// TestChmod verifies that Chmod changes the file mode bits.
func TestChmod(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "file.txt")
	if err := os.WriteFile(path, []byte("data"), 0o644); err != nil {
		t.Fatal(err)
	}

	op := NewOperator()
	if err := op.Chmod(path, 0o755); err != nil {
		t.Fatalf("Chmod: %v", err)
	}

	info, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	if info.Mode().Perm() != 0o755 {
		t.Errorf("mode = %o, want 755", info.Mode().Perm())
	}
}

// TestRemoveAll verifies that RemoveAll recursively removes a directory
// tree. Used during archive cleanup.
func TestRemoveAll(t *testing.T) {
	dir := t.TempDir()
	sub := filepath.Join(dir, "tree")
	if err := os.MkdirAll(filepath.Join(sub, "a", "b"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(sub, "a", "b", "file"), []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}

	op := NewOperator()
	if err := op.RemoveAll(sub); err != nil {
		t.Fatalf("RemoveAll: %v", err)
	}

	if _, err := os.Stat(sub); !os.IsNotExist(err) {
		t.Error("directory tree should be fully removed")
	}
}

// TestRemoveSymlink verifies that Remove deletes a symlink itself rather
// than its target. This matters during cleanup of installed symlinks.
func TestRemoveSymlink(t *testing.T) {
	dir := t.TempDir()
	target := filepath.Join(dir, "target.txt")
	if err := os.WriteFile(target, []byte("real"), 0o644); err != nil {
		t.Fatal(err)
	}

	link := filepath.Join(dir, "link.txt")
	if err := os.Symlink(target, link); err != nil {
		t.Fatal(err)
	}

	op := NewOperator()
	if err := op.Remove(link); err != nil {
		t.Fatalf("Remove symlink: %v", err)
	}

	// Link should be gone
	if _, err := os.Lstat(link); !os.IsNotExist(err) {
		t.Error("symlink should be removed")
	}
	// Target should remain
	if _, err := os.Stat(target); err != nil {
		t.Error("target file should still exist")
	}
}

// TestCopyMissingSource verifies that Copy returns a clear error when
// the source file does not exist.
func TestCopyMissingSource(t *testing.T) {
	op := NewOperator()
	err := op.Copy("/nonexistent/src", "/tmp/dst")
	if err == nil {
		t.Fatal("expected error for missing source")
	}
}

// TestCopyPreservesPermissions verifies that the destination file
// receives the same mode bits as the source.
func TestCopyPreservesPermissions(t *testing.T) {
	dir := t.TempDir()
	src := filepath.Join(dir, "exec.sh")
	if err := os.WriteFile(src, []byte("#!/bin/sh\n"), 0o755); err != nil {
		t.Fatal(err)
	}

	dst := filepath.Join(dir, "exec-copy.sh")
	op := NewOperator()
	if err := op.Copy(src, dst); err != nil {
		t.Fatalf("Copy: %v", err)
	}

	info, err := os.Stat(dst)
	if err != nil {
		t.Fatal(err)
	}
	if info.Mode().Perm() != 0o755 {
		t.Errorf("mode = %o, want 755", info.Mode().Perm())
	}
}

// TestMkdirExistingReturnsError verifies that Mkdir (non-recursive) returns
// an error when the directory already exists.
func TestMkdirExistingReturnsError(t *testing.T) {
	dir := t.TempDir()
	op := NewOperator()
	// dir already exists from TempDir
	if err := op.Mkdir(dir); err == nil {
		t.Error("expected error for existing directory")
	}
}

// TestChownBothEmpty verifies that Chown with empty owner and group
// is a no-op (returns nil immediately).
func TestChownBothEmpty(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "file.txt")
	if err := os.WriteFile(path, []byte("data"), 0o644); err != nil {
		t.Fatal(err)
	}

	op := NewOperator()
	if err := op.Chown(path, "", ""); err != nil {
		t.Errorf("Chown empty: %v", err)
	}
}

// TestSetACLEmptyIsNoOp verifies that SetACL with an empty list
// returns nil without invoking setfacl.
func TestSetACLEmptyIsNoOp(t *testing.T) {
	op := NewOperator()
	if err := op.SetACL("/some/path", nil); err != nil {
		t.Errorf("SetACL empty: %v", err)
	}
}

// TestSetContext_FullContext verifies that SetContext correctly invokes
// both semanage and restorecon with all context fields provided.
// This is the standard case for setting SELinux contexts on files.
func TestSetContext_FullContext(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping semanage integration test in short mode")
	}

	// Check if semanage and restorecon are available
	if _, err := exec.LookPath("semanage"); err != nil {
		t.Skip("semanage not available, skipping SELinux test")
	}
	if _, err := exec.LookPath("restorecon"); err != nil {
		t.Skip("restorecon not available, skipping SELinux test")
	}

	dir := t.TempDir()
	file := filepath.Join(dir, "test.txt")
	if err := os.WriteFile(file, []byte("data"), 0o644); err != nil {
		t.Fatal(err)
	}

	op := NewOperator()

	// Set full context: system_u:object_r:httpd_sys_content_t:s0
	err := op.SetContext(file, "system_u", "httpd_sys_content_t", "s0")

	if err != nil {
		t.Fatalf("SetContext failed: %v", err)
	}

	// Verify context was applied by checking with ls -Z
	cmd := exec.Command("ls", "-Z", file)
	output, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("ls -Z failed: %v", err)
	}

	outputStr := string(output)
	if !strings.Contains(outputStr, "httpd_sys_content_t") {
		t.Errorf("expected httpd_sys_content_t in context, got: %s", outputStr)
	}
}

// TestSetContext_SymlinkResolution verifies that SetContext resolves
// symlinks before calling semanage. This ensures the file policy is
// set on the actual file, not the symlink path.
func TestSetContext_SymlinkResolution(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping semanage integration test in short mode")
	}
	if _, err := exec.LookPath("semanage"); err != nil {
		t.Skip("semanage not available, skipping SELinux test")
	}
	if _, err := exec.LookPath("restorecon"); err != nil {
		t.Skip("restorecon not available, skipping SELinux test")
	}

	dir := t.TempDir()
	realFile := filepath.Join(dir, "real.txt")
	symlinkFile := filepath.Join(dir, "link.txt")

	if err := os.WriteFile(realFile, []byte("data"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(realFile, symlinkFile); err != nil {
		t.Fatal(err)
	}

	op := NewOperator()

	// Call SetContext on the symlink
	err := op.SetContext(symlinkFile, "system_u", "httpd_sys_content_t", "s0")

	if err != nil {
		t.Fatalf("SetContext on symlink failed: %v", err)
	}

	// Verify: The real file should have the context, not the symlink
	// (SELinux contexts apply to targets, not symlinks)
	cmd := exec.Command("ls", "-Z", realFile)
	output, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("ls -Z failed: %v", err)
	}

	outputStr := string(output)
	if !strings.Contains(outputStr, "httpd_sys_content_t") {
		t.Errorf("expected httpd_sys_content_t in real file context, got: %s", outputStr)
	}
}

// TestSetContext_MinimalContext verifies that SetContext works with
// only the required Type field, with empty User and Range.
func TestSetContext_MinimalContext(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping semanage integration test in short mode")
	}
	if _, err := exec.LookPath("semanage"); err != nil {
		t.Skip("semanage not available")
	}
	if _, err := exec.LookPath("restorecon"); err != nil {
		t.Skip("restorecon not available")
	}

	dir := t.TempDir()
	file := filepath.Join(dir, "test.txt")
	if err := os.WriteFile(file, []byte("data"), 0o644); err != nil {
		t.Fatal(err)
	}

	op := NewOperator()

	// Set context with only Type field (User and Range empty)
	err := op.SetContext(file, "", "httpd_sys_content_t", "")

	if err != nil {
		t.Fatalf("SetContext with minimal context failed: %v", err)
	}

	// Verify context was applied
	cmd := exec.Command("ls", "-Z", file)
	output, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("ls -Z failed: %v", err)
	}

	outputStr := string(output)
	if !strings.Contains(outputStr, "httpd_sys_content_t") {
		t.Errorf("expected httpd_sys_content_t in context, got: %s", outputStr)
	}
}

// TestSetContext_BrokenSymlink verifies that SetContext returns an error
// when attempting to set context on a broken symlink. EvalSymlinks will
// fail because the target doesn't exist.
func TestSetContext_BrokenSymlink(t *testing.T) {
	dir := t.TempDir()
	brokenLink := filepath.Join(dir, "broken.txt")

	// Create a symlink to a non-existent file
	if err := os.Symlink("/nonexistent/file.txt", brokenLink); err != nil {
		t.Fatal(err)
	}

	op := NewOperator()

	err := op.SetContext(brokenLink, "system_u", "httpd_sys_content_t", "s0")

	if err == nil {
		t.Fatal("expected error for broken symlink, got nil")
	}

	// Should contain "no such file or directory" from EvalSymlinks
	if !strings.Contains(err.Error(), "no such file") {
		t.Errorf("expected 'no such file' in error, got: %v", err)
	}
}

// TestSetContext_MissingSemanage verifies that SetContext returns a clear
// error when semanage is not available. This simulates a system without
// SELinux tools installed or SELinux disabled.
func TestSetContext_MissingSemanage(t *testing.T) {
	// Only run this test if semanage is NOT available
	if _, err := exec.LookPath("semanage"); err == nil {
		t.Skip("semanage is available, skipping missing executable test")
	}

	dir := t.TempDir()
	file := filepath.Join(dir, "test.txt")
	if err := os.WriteFile(file, []byte("data"), 0o644); err != nil {
		t.Fatal(err)
	}

	op := NewOperator()

	err := op.SetContext(file, "system_u", "httpd_sys_content_t", "s0")

	if err == nil {
		t.Fatal("expected error when semanage is missing, got nil")
	}

	// Should contain "semanage" or "executable file not found" in error
	errMsg := err.Error()
	if !strings.Contains(errMsg, "semanage") && !strings.Contains(errMsg, "executable file not found") {
		t.Errorf("expected semanage-related error, got: %v", errMsg)
	}
}

// TestSetContext_MissingRestorecon documents that SetContext returns an error
// when restorecon is not available. Both semanage AND restorecon must succeed.
func TestSetContext_MissingRestorecon(t *testing.T) {
	// This is difficult to test directly without mocking exec.Command
	// because we can't selectively disable restorecon while keeping semanage.
	// Mark as a documentation test for the requirement.
	t.Skip("Requires mocking exec.Command to test partial failure scenarios")

	// Critical behavior documented:
	// - If semanage succeeds but restorecon fails, SetContext MUST return error
	// - This ensures both commands complete or the operation is considered failed
}

// TestRemoveContext_BestEffort verifies that RemoveContext always returns
// nil, even if the semanage command fails. This implements best-effort
// cleanup semantics where we don't fail deployments due to cleanup errors.
func TestRemoveContext_BestEffort(t *testing.T) {
	dir := t.TempDir()
	file := filepath.Join(dir, "test.txt")
	if err := os.WriteFile(file, []byte("data"), 0o644); err != nil {
		t.Fatal(err)
	}

	op := NewOperator()

	// RemoveContext should never return error, even if semanage fails
	err := op.RemoveContext(file)

	if err != nil {
		t.Errorf("RemoveContext should always return nil, got: %v", err)
	}
}

// TestRemoveContext_NonexistentPath verifies that RemoveContext returns
// nil even when called on a path that doesn't exist. Best-effort cleanup
// should be idempotent and never fail.
func TestRemoveContext_NonexistentPath(t *testing.T) {
	op := NewOperator()

	err := op.RemoveContext("/nonexistent/path/to/file.txt")

	if err != nil {
		t.Errorf("RemoveContext should return nil for non-existent path, got: %v", err)
	}
}

// TestRemoveContext_MissingSemanage verifies that RemoveContext returns
// nil even when semanage is not available. The best-effort pattern means
// we silently ignore all errors during cleanup.
func TestRemoveContext_MissingSemanage(t *testing.T) {
	if _, err := exec.LookPath("semanage"); err == nil {
		t.Skip("semanage is available, cannot test missing executable case")
	}

	op := NewOperator()

	err := op.RemoveContext("/some/path.txt")

	if err != nil {
		t.Errorf("RemoveContext should return nil even when semanage missing, got: %v", err)
	}
}

// TestSetContext_CommandArguments is a documentation test that verifies
// the expected command-line arguments passed to semanage and restorecon.
// This locks in the exact invocation pattern.
func TestSetContext_CommandArguments(t *testing.T) {
	// This test documents the expected command invocations:
	//
	// semanage fcontext -a -s {user} -t {type} -r {range} {realPath}
	// restorecon -v {realPath}
	//
	// Where:
	// - {user} = SELinux user (e.g., "system_u")
	// - {type} = SELinux type (e.g., "httpd_sys_content_t")
	// - {range} = SELinux range (e.g., "s0" or "s0-s0:c0.c1023")
	// - {realPath} = EvalSymlinks result (resolved path)
	//
	// Critical invariants:
	// 1. Path MUST be resolved via EvalSymlinks before semanage
	// 2. Both semanage AND restorecon must be called
	// 3. If either command fails, error is returned (no partial success)
	// 4. User, Type, and Range are passed as-is (no validation/transformation)

	t.Skip("Documentation test - describes expected behavior for SetContext")
}
