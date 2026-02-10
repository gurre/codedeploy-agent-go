// Command codedeploy-agent runs the AWS CodeDeploy agent daemon.
// It polls the CodeDeploy Commands service for deployment commands and
// executes them (downloading bundles, installing files, running hooks).
//
// Usage:
//
//	codedeploy-agent [config-file]          Start the agent daemon
//	codedeploy-agent install [flags]        Self-install onto this host
//
// Install flags:
//
//	--install-dir    Installation directory (default: /opt/codedeploy-agent)
//	--no-start       Install without starting the service
//
// The default config file path is /etc/codedeploy-agent/conf/codedeployagent.yml.
package main

import (
	"context"
	"flag"
	"fmt"
	"os"

	"github.com/gurre/codedeploy-agent-go/entrypoint/agent"
	"github.com/gurre/codedeploy-agent-go/entrypoint/selfinstall"
)

const defaultConfigPath = "/etc/codedeploy-agent/conf/codedeployagent.yml"

func main() {
	if len(os.Args) > 1 && os.Args[1] == "install" {
		runInstall()
		return
	}

	configPath := defaultConfigPath
	if len(os.Args) > 1 {
		configPath = os.Args[1]
	}

	if err := agent.Run(context.Background(), configPath); err != nil {
		fmt.Fprintf(os.Stderr, "codedeploy-agent: %s\n", err)
		os.Exit(1)
	}
}

func runInstall() {
	opts := selfinstall.DefaultOptions()

	fs := flag.NewFlagSet("install", flag.ExitOnError)
	fs.StringVar(&opts.InstallDir, "install-dir", opts.InstallDir, "Installation directory")
	fs.BoolVar(&opts.NoStart, "no-start", opts.NoStart, "Install without starting the service")
	fs.Usage = func() {
		fmt.Fprintf(os.Stderr, "Usage: codedeploy-agent install [flags]\n\nSelf-installs the agent onto this host.\n\nFlags:\n")
		fs.PrintDefaults()
	}

	if err := fs.Parse(os.Args[2:]); err != nil {
		os.Exit(1)
	}

	if os.Getuid() != 0 {
		fmt.Fprintf(os.Stderr, "codedeploy-agent install: must be run as root\n")
		os.Exit(1)
	}

	if err := selfinstall.Run(context.Background(), opts); err != nil {
		fmt.Fprintf(os.Stderr, "codedeploy-agent install: %s\n", err)
		os.Exit(1)
	}
}
