// Package filesystem provides file system operations for deployment installation:
// copy, mkdir, chmod, chown, setfacl, semanage, and removal.
package filesystem

import (
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
)

// Operator performs file system operations during deployment installation.
type Operator struct{}

// NewOperator creates a new file system operator.
//
//	op := filesystem.NewOperator()
//	err := op.Copy("/src/file.txt", "/dst/file.txt")
func NewOperator() *Operator {
	return &Operator{}
}

// Copy copies a file from source to destination, preserving permissions.
// Symlinks are recreated rather than following the link.
func (o *Operator) Copy(source, destination string) error {
	info, err := os.Lstat(source)
	if err != nil {
		return fmt.Errorf("filesystem: stat %s: %w", source, err)
	}

	if info.Mode()&os.ModeSymlink != 0 {
		link, err := os.Readlink(source)
		if err != nil {
			return fmt.Errorf("filesystem: readlink %s: %w", source, err)
		}
		return os.Symlink(link, destination)
	}

	src, err := os.Open(source)
	if err != nil {
		return fmt.Errorf("filesystem: open %s: %w", source, err)
	}
	defer func() { _ = src.Close() }()

	dst, err := os.OpenFile(destination, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, info.Mode())
	if err != nil {
		return fmt.Errorf("filesystem: create %s: %w", destination, err)
	}
	defer func() { _ = dst.Close() }()

	if _, err := io.Copy(dst, src); err != nil {
		return fmt.Errorf("filesystem: copy %s -> %s: %w", source, destination, err)
	}
	return nil
}

// Mkdir creates a single directory (not recursive).
func (o *Operator) Mkdir(path string) error {
	if err := os.Mkdir(path, 0o755); err != nil {
		return fmt.Errorf("filesystem: mkdir %s: %w", path, err)
	}
	return nil
}

// MkdirAll creates a directory and all parents.
func (o *Operator) MkdirAll(path string) error {
	return os.MkdirAll(path, 0o755)
}

// Chmod changes the file mode.
func (o *Operator) Chmod(path string, mode os.FileMode) error {
	return os.Chmod(path, mode)
}

// Chown changes the file owner and group by name.
// Uses OS lookup to resolve names to uid/gid.
func (o *Operator) Chown(path, owner, group string) error {
	// Build chown argument
	var ownerGroup string
	switch {
	case owner != "" && group != "":
		ownerGroup = owner + ":" + group
	case owner != "":
		ownerGroup = owner
	case group != "":
		ownerGroup = ":" + group
	default:
		return nil
	}

	cmd := exec.Command("chown", ownerGroup, path)
	if output, err := cmd.CombinedOutput(); err != nil {
		return fmt.Errorf("filesystem: chown %s %s: %s: %w", ownerGroup, path, string(output), err)
	}
	return nil
}

// SetACL applies POSIX ACL entries to a file or directory via setfacl.
func (o *Operator) SetACL(path string, acl []string) error {
	if len(acl) == 0 {
		return nil
	}
	aclStr := ""
	for i, a := range acl {
		if i > 0 {
			aclStr += ","
		}
		aclStr += a
	}
	cmd := exec.Command("setfacl", "--set", aclStr, path)
	if output, err := cmd.CombinedOutput(); err != nil {
		return fmt.Errorf("filesystem: setfacl %s: %s: %w", path, string(output), err)
	}
	return nil
}

// SetContext applies SELinux context to a file via semanage and restorecon.
func (o *Operator) SetContext(path string, seUser, seType, seRange string) error {
	realPath, err := filepath.EvalSymlinks(path)
	if err != nil {
		return fmt.Errorf("filesystem: resolve %s: %w", path, err)
	}

	args := []string{"fcontext", "-a"}
	if seUser != "" {
		args = append(args, "-s", seUser)
	}
	args = append(args, "-t", seType)
	if seRange != "" {
		args = append(args, "-r", seRange)
	}
	args = append(args, realPath)

	cmd := exec.Command("semanage", args...)
	if output, err := cmd.CombinedOutput(); err != nil {
		return fmt.Errorf("filesystem: semanage %s: %s: %w", realPath, string(output), err)
	}

	cmd = exec.Command("restorecon", "-v", realPath)
	if output, err := cmd.CombinedOutput(); err != nil {
		return fmt.Errorf("filesystem: restorecon %s: %s: %w", realPath, string(output), err)
	}
	return nil
}

// RemoveContext removes an SELinux context from a file via semanage.
func (o *Operator) RemoveContext(path string) error {
	cmd := exec.Command("semanage", "fcontext", "-d", path)
	_ = cmd.Run() // Best effort, ignore errors (matches Ruby behavior)
	return nil
}

// Remove removes a file or empty directory. Non-existent paths are ignored.
// Non-empty directories are skipped (not an error), matching Ruby behavior.
func (o *Operator) Remove(path string) error {
	info, err := os.Lstat(path)
	if os.IsNotExist(err) {
		return nil
	}
	if err != nil {
		return err
	}

	if info.Mode()&os.ModeSymlink != 0 {
		return os.Remove(path)
	}
	if info.IsDir() {
		// Only remove empty directories; ignore ENOTEMPTY
		err := os.Remove(path)
		if err != nil && !os.IsNotExist(err) {
			// Silently ignore non-empty directory errors
			return nil
		}
		return nil
	}
	return os.Remove(path)
}

// RemoveAll removes a path and all its contents.
func (o *Operator) RemoveAll(path string) error {
	return os.RemoveAll(path)
}
