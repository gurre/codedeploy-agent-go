// Package servicefile embeds the init system service files for the CodeDeploy
// agent. The embedded files are written to disk during self-installation.
package servicefile

import _ "embed"

//go:embed codedeploy-agent.service
var systemdUnit []byte

//go:embed codedeploy-agent
var sysVScript []byte

// SystemdUnit returns the systemd unit file for the CodeDeploy agent.
//
//	unit := servicefile.SystemdUnit()
//	os.WriteFile("/etc/systemd/system/codedeploy-agent.service", unit, 0644)
func SystemdUnit() []byte { return systemdUnit }

// SysVScript returns the SysV init script for the CodeDeploy agent.
//
//	script := servicefile.SysVScript()
//	os.WriteFile("/etc/init.d/codedeploy-agent", script, 0755)
func SysVScript() []byte { return sysVScript }
