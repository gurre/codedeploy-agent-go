// Package deployment defines the on-disk directory layout for deployments.
// All path computation is pure (no I/O), making it safe for any layer to use.
package deployment

import "path/filepath"

// Layout computes all deployment-related paths from a root directory,
// deployment group ID, and deployment ID.
type Layout struct {
	rootDir           string
	deploymentGroupID string
	deploymentID      string
}

// NewLayout creates a layout for the given root directory, deployment group, and deployment.
//
//	layout := deployment.NewLayout("/opt/codedeploy-agent/deployment-root", "dg-123", "d-456")
//	archiveDir := layout.ArchiveDir()
func NewLayout(rootDir, deploymentGroupID, deploymentID string) Layout {
	return Layout{
		rootDir:           rootDir,
		deploymentGroupID: deploymentGroupID,
		deploymentID:      deploymentID,
	}
}

// DeploymentRootDir returns the top-level directory for this deployment.
// Example: /opt/codedeploy-agent/deployment-root/dg-123/d-456
func (l Layout) DeploymentRootDir() string {
	return filepath.Join(l.rootDir, l.deploymentGroupID, l.deploymentID)
}

// ArchiveDir returns the unpacked deployment archive directory.
// Example: /opt/codedeploy-agent/deployment-root/dg-123/d-456/deployment-archive
func (l Layout) ArchiveDir() string {
	return filepath.Join(l.DeploymentRootDir(), "deployment-archive")
}

// BundleFile returns the path to the downloaded bundle artifact.
// Example: /opt/codedeploy-agent/deployment-root/dg-123/d-456/bundle.tar
func (l Layout) BundleFile() string {
	return filepath.Join(l.DeploymentRootDir(), "bundle.tar")
}

// ScriptLogFile returns the path to the script execution log.
// Example: /opt/codedeploy-agent/deployment-root/dg-123/d-456/logs/scripts.log
func (l Layout) ScriptLogFile() string {
	return filepath.Join(l.DeploymentRootDir(), "logs", "scripts.log")
}

// GroupDir returns the deployment group directory (containing all deployment dirs).
// Example: /opt/codedeploy-agent/deployment-root/dg-123
func (l Layout) GroupDir() string {
	return filepath.Join(l.rootDir, l.deploymentGroupID)
}

// InstructionsDir returns the directory for install/cleanup instruction files.
// This is shared across all deployments (not per-deployment).
// Example: /opt/codedeploy-agent/deployment-root/deployment-instructions
func InstructionsDir(rootDir string) string {
	return filepath.Join(rootDir, "deployment-instructions")
}

// CleanupFile returns the path to the cleanup file for a deployment group.
// Example: /opt/codedeploy-agent/deployment-root/deployment-instructions/dg-123-cleanup
func CleanupFile(rootDir, deploymentGroupID string) string {
	return filepath.Join(InstructionsDir(rootDir), deploymentGroupID+"-cleanup")
}

// InstallFile returns the path to the install instructions file for a deployment group.
// Example: /opt/codedeploy-agent/deployment-root/deployment-instructions/dg-123-install.json
func InstallFile(rootDir, deploymentGroupID string) string {
	return filepath.Join(InstructionsDir(rootDir), deploymentGroupID+"-install.json")
}

// LastSuccessfulFile returns the path to the file tracking the last successful deployment.
// Example: /opt/codedeploy-agent/deployment-root/deployment-instructions/dg-123_last_successful_install
func LastSuccessfulFile(rootDir, deploymentGroupID string) string {
	return filepath.Join(InstructionsDir(rootDir), deploymentGroupID+"_last_successful_install")
}

// MostRecentFile returns the path to the file tracking the most recent deployment.
// Example: /opt/codedeploy-agent/deployment-root/deployment-instructions/dg-123_most_recent_install
func MostRecentFile(rootDir, deploymentGroupID string) string {
	return filepath.Join(InstructionsDir(rootDir), deploymentGroupID+"_most_recent_install")
}

// LogsDir returns the per-deployment logs directory.
// Example: /opt/codedeploy-agent/deployment-root/dg-123/d-456/logs
func (l Layout) LogsDir() string {
	return filepath.Join(l.DeploymentRootDir(), "logs")
}

// DeploymentLogsDir returns the shared deployment-logs directory under rootDir.
// Example: /opt/codedeploy-agent/deployment-root/deployment-logs
func DeploymentLogsDir(rootDir string) string {
	return filepath.Join(rootDir, "deployment-logs")
}

// DeploymentLogFile returns the path to the shared deployments log file.
// Example: /opt/codedeploy-agent/deployment-root/deployment-logs/codedeploy-agent-deployments.log
func DeploymentLogFile(rootDir string) string {
	return filepath.Join(DeploymentLogsDir(rootDir), "codedeploy-agent-deployments.log")
}

// OngoingDeploymentDir returns the directory for tracking in-progress deployments.
// Example: /opt/codedeploy-agent/deployment-root/ongoing-deployment
func OngoingDeploymentDir(rootDir, trackingSubdir string) string {
	return filepath.Join(rootDir, trackingSubdir)
}

// OngoingDeploymentFile returns the tracking file path for a specific deployment.
// Example: /opt/codedeploy-agent/deployment-root/ongoing-deployment/d-456
func OngoingDeploymentFile(rootDir, trackingSubdir, deploymentID string) string {
	return filepath.Join(OngoingDeploymentDir(rootDir, trackingSubdir), deploymentID)
}
