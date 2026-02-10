// builder.go provides the CommandBuilder which accumulates install instructions
// from the appspec files and permissions sections. It tracks which files are
// being copied and directories being created to detect conflicts and apply
// permissions correctly.
package instruction

import (
	"fmt"
	"path/filepath"
)

// Builder accumulates install commands and tracks copy/mkdir targets for
// conflict detection and permission matching.
type Builder struct {
	commands          []Command
	copyTargets       map[string]string // destination -> source
	mkdirTargets      map[string]bool
	permissionTargets map[string]bool
}

// NewBuilder creates a fresh instruction builder.
//
//	b := instruction.NewBuilder()
//	b.Copy("/src/file.txt", "/dst/file.txt")
//	b.Mkdir("/dst/subdir")
func NewBuilder() *Builder {
	return &Builder{
		commands:          make([]Command, 0, 64),
		copyTargets:       make(map[string]string),
		mkdirTargets:      make(map[string]bool),
		permissionTargets: make(map[string]bool),
	}
}

// Copy adds a file copy instruction. Returns an error if the destination
// conflicts with an existing copy or mkdir target.
func (b *Builder) Copy(source, destination string) error {
	destination = filepath.Clean(destination)

	if prev, ok := b.copyTargets[destination]; ok {
		return fmt.Errorf("duplicate copy destination %q from %q and %q", destination, source, prev)
	}
	if b.mkdirTargets[destination] {
		return fmt.Errorf("copy destination %q conflicts with mkdir target", destination)
	}

	b.commands = append(b.commands, Command{
		Type:        TypeCopy,
		Source:      source,
		Destination: destination,
	})
	b.copyTargets[destination] = source
	return nil
}

// Mkdir adds a directory creation instruction. Skips if directory is already
// tracked as a mkdir target (idempotent for recursive directory creation).
func (b *Builder) Mkdir(directory string) error {
	directory = filepath.Clean(directory)

	if _, ok := b.copyTargets[directory]; ok {
		return fmt.Errorf("mkdir target %q conflicts with copy target", directory)
	}
	if b.mkdirTargets[directory] {
		return nil // already tracked, skip duplicate
	}

	b.commands = append(b.commands, Command{
		Type:      TypeMkdir,
		Directory: directory,
	})
	b.mkdirTargets[directory] = true
	return nil
}

// SetMode adds a chmod instruction for a file or directory.
func (b *Builder) SetMode(path, mode string) {
	b.commands = append(b.commands, Command{
		Type: TypeChmod,
		File: path,
		Mode: mode,
	})
}

// SetOwner adds a chown instruction for a file or directory.
func (b *Builder) SetOwner(path, owner, group string) {
	b.commands = append(b.commands, Command{
		Type:  TypeChown,
		File:  path,
		Owner: owner,
		Group: group,
	})
}

// SetACL adds a setfacl instruction for a file or directory.
func (b *Builder) SetACL(path string, acl []string) {
	b.commands = append(b.commands, Command{
		Type: TypeSetfacl,
		File: path,
		ACL:  acl,
	})
}

// SetContext adds a semanage instruction for a file or directory.
func (b *Builder) SetContext(path string, ctx ContextCmd) {
	b.commands = append(b.commands, Command{
		Type:    TypeSemanage,
		File:    path,
		Context: &ctx,
	})
}

// MarkPermission records that permissions have been set for an object.
// Returns an error if permissions were already applied to this path.
func (b *Builder) MarkPermission(path string) error {
	path = filepath.Clean(path)
	if b.permissionTargets[path] {
		return fmt.Errorf("permissions already set for %q", path)
	}
	b.permissionTargets[path] = true
	return nil
}

// IsCopyTarget returns whether a path is a tracked copy destination.
func (b *Builder) IsCopyTarget(path string) bool {
	_, ok := b.copyTargets[filepath.Clean(path)]
	return ok
}

// IsMkdirTarget returns whether a path is a tracked mkdir target.
func (b *Builder) IsMkdirTarget(path string) bool {
	return b.mkdirTargets[filepath.Clean(path)]
}

// CopyTargets returns all tracked copy destination paths.
func (b *Builder) CopyTargets() []string {
	targets := make([]string, 0, len(b.copyTargets))
	for k := range b.copyTargets {
		targets = append(targets, k)
	}
	return targets
}

// MkdirTargets returns all tracked mkdir target paths.
func (b *Builder) MkdirTargets() []string {
	targets := make([]string, 0, len(b.mkdirTargets))
	for k := range b.mkdirTargets {
		targets = append(targets, k)
	}
	return targets
}

// Commands returns the accumulated command list.
func (b *Builder) Commands() []Command {
	return b.commands
}

// Build returns the finalized Instructions ready for JSON serialization.
func (b *Builder) Build() Instructions {
	return Instructions{Commands: b.commands}
}
