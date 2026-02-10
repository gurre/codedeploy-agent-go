// Package installer generates install instructions from the appspec files section,
// executes cleanup of previous installations, and performs the new installation.
package installer

import (
	"bufio"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"

	"github.com/gurre/codedeploy-agent-go/logic/appspec"
	"github.com/gurre/codedeploy-agent-go/logic/instruction"
)

// FileOperator performs file system operations during installation.
type FileOperator interface {
	Copy(source, destination string) error
	Mkdir(path string) error
	MkdirAll(path string) error
	Chmod(path string, mode os.FileMode) error
	Chown(path, owner, group string) error
	SetACL(path string, acl []string) error
	SetContext(path string, seUser, seType, seRange string) error
	RemoveContext(path string) error
	Remove(path string) error
}

// Installer manages the install/cleanup lifecycle for deployments.
type Installer struct {
	fileOp FileOperator
	logger *slog.Logger
}

// NewInstaller creates an installer with the given file operator.
//
//	inst := installer.NewInstaller(filesystem.NewOperator(), slog.Default())
func NewInstaller(fileOp FileOperator, logger *slog.Logger) *Installer {
	return &Installer{
		fileOp: fileOp,
		logger: logger,
	}
}

// Install performs cleanup of the previous deployment and installs the new one.
// It generates instructions from the appspec, writes them to instruction files,
// and executes the copy/permission commands.
func (inst *Installer) Install(
	deploymentGroupID string,
	archiveDir string,
	instructionsDir string,
	spec appspec.Spec,
	fileExistsBehavior string,
) error {
	if err := os.MkdirAll(instructionsDir, 0o755); err != nil {
		return fmt.Errorf("installer: mkdir instructions: %w", err)
	}

	// Execute cleanup from previous deployment
	cleanupPath := filepath.Join(instructionsDir, deploymentGroupID+"-cleanup")
	if err := inst.executeCleanup(cleanupPath); err != nil {
		return fmt.Errorf("installer: cleanup: %w", err)
	}

	// Generate instructions from appspec
	builder, err := inst.generateInstructions(archiveDir, spec, fileExistsBehavior)
	if err != nil {
		return fmt.Errorf("installer: generate: %w", err)
	}

	// Write install instructions JSON
	instructions := builder.Build()
	installData, err := instructions.ToJSON()
	if err != nil {
		return fmt.Errorf("installer: marshal instructions: %w", err)
	}
	installPath := filepath.Join(instructionsDir, deploymentGroupID+"-install.json")
	if err := os.WriteFile(installPath, installData, 0o644); err != nil {
		return fmt.Errorf("installer: write install file: %w", err)
	}

	// Execute commands and write cleanup file
	if err := inst.executeCommands(builder.Commands(), cleanupPath); err != nil {
		return fmt.Errorf("installer: execute: %w", err)
	}

	return nil
}

func (inst *Installer) executeCleanup(cleanupPath string) error {
	data, err := os.ReadFile(cleanupPath)
	if os.IsNotExist(err) {
		return nil
	}
	if err != nil {
		return err
	}

	entries := instruction.ParseRemoveCommands(string(data))
	for _, entry := range entries {
		if entry.IsContext {
			_ = inst.fileOp.RemoveContext(entry.Path)
		} else {
			_ = inst.fileOp.Remove(entry.Path)
		}
	}

	return os.Remove(cleanupPath)
}

func (inst *Installer) generateInstructions(
	archiveDir string,
	spec appspec.Spec,
	fileExistsBehavior string,
) (*instruction.Builder, error) {
	builder := instruction.NewBuilder()

	for _, fm := range spec.Files {
		sourcePath := filepath.Join(archiveDir, fm.Source)
		feb := spec.FileExistsBehavior
		if feb == "" {
			feb = fileExistsBehavior
		}

		info, err := os.Stat(sourcePath)
		if err != nil {
			return nil, fmt.Errorf("source %q: %w", fm.Source, err)
		}

		if info.IsDir() {
			if err := inst.fillMissingAncestors(builder, fm.Destination); err != nil {
				return nil, err
			}
			if err := inst.generateDirectoryCopy(builder, sourcePath, fm.Destination, feb); err != nil {
				return nil, err
			}
		} else {
			fileDestination := filepath.Join(fm.Destination, filepath.Base(sourcePath))
			if err := inst.fillMissingAncestors(builder, fileDestination); err != nil {
				return nil, err
			}
			if err := inst.generateFileCopy(builder, sourcePath, fileDestination, feb); err != nil {
				return nil, err
			}
		}
	}

	// Apply permissions
	for _, perm := range spec.Permissions {
		if err := inst.applyPermissions(builder, perm); err != nil {
			return nil, err
		}
	}

	return builder, nil
}

func (inst *Installer) generateDirectoryCopy(
	builder *instruction.Builder, sourcePath, destination, feb string,
) error {
	if _, err := os.Stat(destination); os.IsNotExist(err) {
		if err := builder.Mkdir(destination); err != nil {
			return err
		}
	}

	entries, err := os.ReadDir(sourcePath)
	if err != nil {
		return err
	}

	for _, entry := range entries {
		entrySource := filepath.Join(sourcePath, entry.Name())
		entryDest := filepath.Join(destination, entry.Name())

		if entry.IsDir() {
			if err := inst.generateDirectoryCopy(builder, entrySource, entryDest, feb); err != nil {
				return err
			}
		} else {
			if err := inst.generateFileCopy(builder, entrySource, entryDest, feb); err != nil {
				return err
			}
		}
	}
	return nil
}

func (inst *Installer) generateFileCopy(
	builder *instruction.Builder, source, destination, feb string,
) error {
	if _, err := os.Stat(destination); err == nil {
		// File exists
		switch feb {
		case "DISALLOW":
			return fmt.Errorf("file already exists at %s", destination)
		case "OVERWRITE":
			return builder.Copy(source, destination)
		case "RETAIN":
			return nil // Skip
		default:
			return fmt.Errorf("invalid file_exists_behavior: %s", feb)
		}
	}
	return builder.Copy(source, destination)
}

func (inst *Installer) fillMissingAncestors(builder *instruction.Builder, destination string) error {
	missing := make([]string, 0, 8)
	parent := filepath.Dir(destination)
	for parent != "." && parent != "/" {
		if _, err := os.Stat(parent); err == nil {
			break
		}
		missing = append(missing, parent)
		parent = filepath.Dir(parent)
	}
	// Reverse to create shallowest ancestors first
	for i, j := 0, len(missing)-1; i < j; i, j = i+1, j-1 {
		missing[i], missing[j] = missing[j], missing[i]
	}
	for _, dir := range missing {
		if err := builder.Mkdir(dir); err != nil {
			return err
		}
	}
	return nil
}

func (inst *Installer) applyPermissions(builder *instruction.Builder, perm appspec.Permission) error {
	object := filepath.Clean(perm.Object)

	if builder.IsCopyTarget(object) {
		if hasType(perm.Type, "file") {
			if err := perm.ValidateFilePermission(); err != nil {
				return err
			}
			if err := perm.ValidateFileACL(); err != nil {
				return err
			}
			inst.setPermissions(builder, object, perm)
		}
	} else if builder.IsMkdirTarget(object) || isExistingDir(object) {
		for _, match := range findMatches(builder, perm) {
			inst.setPermissions(builder, match, perm)
		}
	}
	return nil
}

func (inst *Installer) setPermissions(builder *instruction.Builder, path string, perm appspec.Permission) {
	if err := builder.MarkPermission(path); err != nil {
		inst.logger.Warn("permission already set", "path", path, "error", err)
		return
	}
	if perm.Mode != nil {
		builder.SetMode(path, perm.Mode.Raw)
	}
	if perm.ACLs != nil {
		builder.SetACL(path, perm.ACLs.Entries)
	}
	if perm.Context != nil {
		rangeStr := ""
		if perm.Context.Range != nil {
			rangeStr = perm.Context.Range.GetRange()
		}
		builder.SetContext(path, instruction.ContextCmd{
			User:  perm.Context.User,
			Type:  perm.Context.Type,
			Range: rangeStr,
		})
	}
	if perm.Owner != "" || perm.Group != "" {
		builder.SetOwner(path, perm.Owner, perm.Group)
	}
}

func (inst *Installer) executeCommands(commands []instruction.Command, cleanupPath string) error {
	cleanupFile, err := os.Create(cleanupPath)
	if err != nil {
		return err
	}
	defer func() { _ = cleanupFile.Close() }()

	bw := bufio.NewWriter(cleanupFile)
	defer func() { _ = bw.Flush() }()

	for _, cmd := range commands {
		switch cmd.Type {
		case instruction.TypeCopy:
			_, _ = fmt.Fprintln(bw, cmd.Destination)
			if err := inst.fileOp.Copy(cmd.Source, cmd.Destination); err != nil {
				return err
			}
		case instruction.TypeMkdir:
			if err := inst.fileOp.Mkdir(cmd.Directory); err != nil {
				return err
			}
			_, _ = fmt.Fprintln(bw, cmd.Directory)
		case instruction.TypeChmod:
			mode, _ := appspec.ParseMode(cmd.Mode)
			if err := inst.fileOp.Chmod(cmd.File, os.FileMode(mode.Value)); err != nil {
				return err
			}
		case instruction.TypeChown:
			if err := inst.fileOp.Chown(cmd.File, cmd.Owner, cmd.Group); err != nil {
				return err
			}
		case instruction.TypeSetfacl:
			if err := inst.fileOp.SetACL(cmd.File, cmd.ACL); err != nil {
				return err
			}
		case instruction.TypeSemanage:
			if cmd.Context == nil {
				continue
			}
			if err := inst.fileOp.SetContext(cmd.File, cmd.Context.User, cmd.Context.Type, cmd.Context.Range); err != nil {
				return err
			}
			_, _ = fmt.Fprintf(bw, "semanage\x00%s\n", cmd.File)
		}
	}
	return bw.Flush()
}

func findMatches(builder *instruction.Builder, perm appspec.Permission) []string {
	var matches []string
	if hasType(perm.Type, "file") {
		for _, target := range builder.CopyTargets() {
			if perm.MatchesPattern(target) && !perm.MatchesExcept(target) {
				matches = append(matches, target)
			}
		}
	}
	if hasType(perm.Type, "directory") {
		for _, target := range builder.MkdirTargets() {
			if perm.MatchesPattern(target) && !perm.MatchesExcept(target) {
				matches = append(matches, target)
			}
		}
	}
	return matches
}

func hasType(types []string, t string) bool {
	for _, tt := range types {
		if tt == t {
			return true
		}
	}
	return false
}

func isExistingDir(path string) bool {
	info, err := os.Stat(path)
	return err == nil && info.IsDir()
}
