package filesystem

import (
	"os"
	"path/filepath"
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
