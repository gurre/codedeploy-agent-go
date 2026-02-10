// Package selfinstall executes installation actions produced by the logic
// layer's reconciliation. It coordinates filesystem writes, binary copies,
// and service management through injected interfaces.
package selfinstall

import (
	"context"
	"fmt"
	"log/slog"
	"os"

	"github.com/gurre/codedeploy-agent-go/logic/selfinstall"
)

// FileInstaller abstracts filesystem operations needed during installation.
type FileInstaller interface {
	MkdirAll(path string) error
	WriteFile(path string, data []byte, perm os.FileMode) error
	CopyFile(src, dst string, perm os.FileMode) error
	Rename(oldpath, newpath string) error
}

// ServiceController abstracts init system service management.
type ServiceController interface {
	Enable(ctx context.Context, name string) error
	Start(ctx context.Context, name string) error
	DaemonReload(ctx context.Context) error
}

// Reconciler applies installation steps to the real system.
type Reconciler struct {
	files   FileInstaller
	service ServiceController
	// selfBinary is the path to the currently running binary (os.Executable).
	selfBinary string
	logger     *slog.Logger
}

// NewReconciler creates a Reconciler with the given filesystem and service
// implementations.
//
//	r := selfinstall.NewReconciler(files, svc, "/proc/self/exe", logger)
//	err := r.Install(ctx, manifest, steps)
func NewReconciler(files FileInstaller, service ServiceController, selfBinary string, logger *slog.Logger) *Reconciler {
	return &Reconciler{
		files:      files,
		service:    service,
		selfBinary: selfBinary,
		logger:     logger,
	}
}

// Install executes the given steps sequentially. It fails fast on the first
// error. Binary copies use an atomic write-then-rename pattern to avoid
// partial writes leaving a corrupt binary on disk.
//
//	err := r.Install(ctx, manifest, steps)
func (r *Reconciler) Install(ctx context.Context, m selfinstall.Manifest, steps []selfinstall.Step) error {
	reloadNeeded := false

	for _, step := range steps {
		if err := ctx.Err(); err != nil {
			return fmt.Errorf("selfinstall: cancelled: %w", err)
		}

		r.logger.Info("executing install step", "action", step.Action, "path", step.Path)

		switch step.Action {
		case selfinstall.CreateDir:
			if err := r.files.MkdirAll(step.Path); err != nil {
				return fmt.Errorf("selfinstall: mkdir %s: %w", step.Path, err)
			}

		case selfinstall.CopyBinary:
			if err := r.copyBinaryAtomic(step.Path, os.FileMode(step.Mode)); err != nil {
				return fmt.Errorf("selfinstall: copy binary: %w", err)
			}

		case selfinstall.WriteFile:
			if err := r.files.WriteFile(step.Path, step.Content, os.FileMode(step.Mode)); err != nil {
				return fmt.Errorf("selfinstall: write %s: %w", step.Path, err)
			}
			// Track whether a systemd unit was written so we can daemon-reload.
			if m.InitSystem == selfinstall.Systemd && step.Path == m.ServiceFilePath {
				reloadNeeded = true
			}

		case selfinstall.EnableService:
			if reloadNeeded {
				if err := r.service.DaemonReload(ctx); err != nil {
					return fmt.Errorf("selfinstall: daemon-reload: %w", err)
				}
				reloadNeeded = false
			}
			if err := r.service.Enable(ctx, step.Path); err != nil {
				return fmt.Errorf("selfinstall: enable %s: %w", step.Path, err)
			}

		case selfinstall.StartService:
			if reloadNeeded {
				if err := r.service.DaemonReload(ctx); err != nil {
					return fmt.Errorf("selfinstall: daemon-reload: %w", err)
				}
				reloadNeeded = false
			}
			if err := r.service.Start(ctx, step.Path); err != nil {
				return fmt.Errorf("selfinstall: start %s: %w", step.Path, err)
			}

		default:
			return fmt.Errorf("selfinstall: unknown action %d", step.Action)
		}
	}

	return nil
}

// copyBinaryAtomic copies the running binary to dst using a temp file and
// os.Rename for atomicity. This prevents a partial write from leaving a
// corrupt binary on disk if the process is killed mid-copy.
func (r *Reconciler) copyBinaryAtomic(dst string, mode os.FileMode) error {
	tmp := dst + ".tmp"
	if err := r.files.CopyFile(r.selfBinary, tmp, mode); err != nil {
		return fmt.Errorf("copy to temp: %w", err)
	}
	if err := r.files.Rename(tmp, dst); err != nil {
		return fmt.Errorf("rename %s -> %s: %w", tmp, dst, err)
	}
	return nil
}
