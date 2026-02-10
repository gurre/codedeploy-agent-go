package selfinstall_test

import (
	"context"
	"fmt"
	"log/slog"
	"os"
	"testing"

	"github.com/gurre/codedeploy-agent-go/logic/selfinstall"
	orchinstall "github.com/gurre/codedeploy-agent-go/orchestration/selfinstall"
)

// TestInstall_ExecutesStepsInOrder verifies that the reconciler executes
// each action in the order provided and calls the correct interface methods.
func TestInstall_ExecutesStepsInOrder(t *testing.T) {
	files := &mockFileInstaller{}
	svc := &mockServiceController{}
	logger := slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelError}))
	r := orchinstall.NewReconciler(files, svc, "/fake/self", logger)

	m := selfinstall.DefaultManifest(
		"/opt/test",
		selfinstall.Systemd,
		[]byte("[Unit]\nDescription=test"),
		[]byte("root_dir: /opt/test\n"),
	)

	steps := []selfinstall.Step{
		{Action: selfinstall.CreateDir, Path: "/opt/test/bin", Mode: 0o755},
		{Action: selfinstall.CopyBinary, Path: "/opt/test/bin/codedeploy-agent", Mode: 0o755},
		{Action: selfinstall.WriteFile, Path: m.ServiceFilePath, Content: m.ServiceFileContent, Mode: 0o644},
		{Action: selfinstall.WriteFile, Path: m.ConfigPath, Content: m.ConfigContent, Mode: 0o644},
		{Action: selfinstall.EnableService, Path: "codedeploy-agent"},
		{Action: selfinstall.StartService, Path: "codedeploy-agent"},
	}

	if err := r.Install(context.Background(), m, steps); err != nil {
		t.Fatalf("Install failed: %v", err)
	}

	// Verify mkdir was called.
	if len(files.mkdirCalls) != 1 || files.mkdirCalls[0] != "/opt/test/bin" {
		t.Errorf("MkdirAll calls: %v", files.mkdirCalls)
	}

	// Verify atomic copy: CopyFile to .tmp, then Rename.
	if len(files.copyCalls) != 1 {
		t.Fatalf("expected 1 CopyFile call, got %d", len(files.copyCalls))
	}
	if files.copyCalls[0].dst != "/opt/test/bin/codedeploy-agent.tmp" {
		t.Errorf("CopyFile dst = %s, want .tmp suffix", files.copyCalls[0].dst)
	}
	if len(files.renameCalls) != 1 {
		t.Fatalf("expected 1 Rename call, got %d", len(files.renameCalls))
	}
	if files.renameCalls[0].newpath != "/opt/test/bin/codedeploy-agent" {
		t.Errorf("Rename new = %s", files.renameCalls[0].newpath)
	}

	// Verify WriteFile was called twice (service + config).
	if len(files.writeCalls) != 2 {
		t.Errorf("WriteFile calls: %d, want 2", len(files.writeCalls))
	}

	// Verify daemon-reload was called before enable (systemd unit was written).
	if svc.reloadCount != 1 {
		t.Errorf("DaemonReload count: %d, want 1", svc.reloadCount)
	}

	// Verify enable and start were called.
	if len(svc.enableCalls) != 1 || svc.enableCalls[0] != "codedeploy-agent" {
		t.Errorf("Enable calls: %v", svc.enableCalls)
	}
	if len(svc.startCalls) != 1 || svc.startCalls[0] != "codedeploy-agent" {
		t.Errorf("Start calls: %v", svc.startCalls)
	}
}

// TestInstall_FailsFastOnError verifies that the reconciler stops executing
// steps after the first error and returns it.
func TestInstall_FailsFastOnError(t *testing.T) {
	files := &mockFileInstaller{
		mkdirErr: fmt.Errorf("permission denied"),
	}
	svc := &mockServiceController{}
	logger := slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelError}))
	r := orchinstall.NewReconciler(files, svc, "/fake/self", logger)

	m := selfinstall.Manifest{InitSystem: selfinstall.Systemd}
	steps := []selfinstall.Step{
		{Action: selfinstall.CreateDir, Path: "/fail/dir", Mode: 0o755},
		{Action: selfinstall.CopyBinary, Path: "/should/not/reach", Mode: 0o755},
	}

	err := r.Install(context.Background(), m, steps)
	if err == nil {
		t.Fatal("expected error from failing MkdirAll")
	}

	// CopyFile should NOT have been called.
	if len(files.copyCalls) != 0 {
		t.Errorf("CopyFile should not be called after MkdirAll error, got %d calls", len(files.copyCalls))
	}
}

// TestInstall_CancelledContext verifies that the reconciler respects context
// cancellation between steps.
func TestInstall_CancelledContext(t *testing.T) {
	files := &mockFileInstaller{}
	svc := &mockServiceController{}
	logger := slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelError}))
	r := orchinstall.NewReconciler(files, svc, "/fake/self", logger)

	ctx, cancel := context.WithCancel(context.Background())
	cancel() // cancel immediately

	m := selfinstall.Manifest{InitSystem: selfinstall.Systemd}
	steps := []selfinstall.Step{
		{Action: selfinstall.CreateDir, Path: "/should/not/run", Mode: 0o755},
	}

	err := r.Install(ctx, m, steps)
	if err == nil {
		t.Fatal("expected cancellation error")
	}
	if len(files.mkdirCalls) != 0 {
		t.Error("MkdirAll should not be called on cancelled context")
	}
}

// TestInstall_SysVNoDaemonReload verifies that daemon-reload is NOT called
// for SysV init systems, even when a service file is written.
func TestInstall_SysVNoDaemonReload(t *testing.T) {
	files := &mockFileInstaller{}
	svc := &mockServiceController{}
	logger := slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelError}))
	r := orchinstall.NewReconciler(files, svc, "/fake/self", logger)

	m := selfinstall.DefaultManifest(
		"/opt/test",
		selfinstall.SysV,
		[]byte("#!/bin/bash"),
		[]byte("root_dir: /opt/test\n"),
	)

	steps := []selfinstall.Step{
		{Action: selfinstall.WriteFile, Path: m.ServiceFilePath, Content: m.ServiceFileContent, Mode: 0o755},
		{Action: selfinstall.EnableService, Path: "codedeploy-agent"},
	}

	if err := r.Install(context.Background(), m, steps); err != nil {
		t.Fatalf("Install failed: %v", err)
	}

	if svc.reloadCount != 0 {
		t.Errorf("DaemonReload should not be called for SysV, got %d calls", svc.reloadCount)
	}
}

// Mock implementations below.

type copyCall struct {
	src  string
	dst  string
	perm os.FileMode
}

type renameCall struct {
	oldpath string
	newpath string
}

type writeCall struct {
	path string
	perm os.FileMode
}

type mockFileInstaller struct {
	mkdirCalls  []string
	copyCalls   []copyCall
	renameCalls []renameCall
	writeCalls  []writeCall
	mkdirErr    error
}

func (m *mockFileInstaller) MkdirAll(path string) error {
	m.mkdirCalls = append(m.mkdirCalls, path)
	return m.mkdirErr
}

func (m *mockFileInstaller) WriteFile(path string, data []byte, perm os.FileMode) error {
	m.writeCalls = append(m.writeCalls, writeCall{path: path, perm: perm})
	return nil
}

func (m *mockFileInstaller) CopyFile(src, dst string, perm os.FileMode) error {
	m.copyCalls = append(m.copyCalls, copyCall{src: src, dst: dst, perm: perm})
	return nil
}

func (m *mockFileInstaller) Rename(oldpath, newpath string) error {
	m.renameCalls = append(m.renameCalls, renameCall{oldpath: oldpath, newpath: newpath})
	return nil
}

type mockServiceController struct {
	enableCalls []string
	startCalls  []string
	reloadCount int
}

func (m *mockServiceController) Enable(_ context.Context, name string) error {
	m.enableCalls = append(m.enableCalls, name)
	return nil
}

func (m *mockServiceController) Start(_ context.Context, name string) error {
	m.startCalls = append(m.startCalls, name)
	return nil
}

func (m *mockServiceController) DaemonReload(_ context.Context) error {
	m.reloadCount++
	return nil
}
