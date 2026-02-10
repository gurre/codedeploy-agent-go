// Command codedeploy-local runs a one-shot local deployment without the
// CodeDeploy service. It downloads/links the bundle, installs files, and
// executes lifecycle hook scripts.
//
// Usage:
//
//	codedeploy-local [flags]
//
// Flags:
//
//	-l, --bundle-location   Bundle location (path, s3://bucket/key, or github URL)
//	-t, --type              Bundle type: tar, tgz, zip, directory (default: directory)
//	-b, --file-exists-behavior  DISALLOW, OVERWRITE, or RETAIN (default: DISALLOW)
//	-g, --deployment-group  Deployment group ID (default: default-local-deployment-group)
//	-d, --deployment-group-name  Deployment group name (default: LocalFleet)
//	-a, --application-name  Application name (default: bundle location)
//	-e, --events            Comma-separated lifecycle events to execute
//	-c, --agent-configuration-file  Path to codedeployagent.yml
//	-A, --appspec-filename  AppSpec file name (default: appspec.yml)
package main

import (
	"context"
	"flag"
	"fmt"
	"os"
	"strings"

	"github.com/gurre/codedeploy-agent-go/entrypoint/localcli"
)

func main() {
	opts := localcli.DefaultOptions()

	var eventsStr string

	flag.StringVar(&opts.BundleLocation, "l", "", "Bundle location")
	flag.StringVar(&opts.BundleLocation, "bundle-location", "", "Bundle location")
	flag.StringVar(&opts.BundleType, "t", opts.BundleType, "Bundle type (tar, tgz, zip, directory)")
	flag.StringVar(&opts.BundleType, "type", opts.BundleType, "Bundle type (tar, tgz, zip, directory)")
	flag.StringVar(&opts.FileExistsBehavior, "b", opts.FileExistsBehavior, "File exists behavior (DISALLOW, OVERWRITE, RETAIN)")
	flag.StringVar(&opts.FileExistsBehavior, "file-exists-behavior", opts.FileExistsBehavior, "File exists behavior (DISALLOW, OVERWRITE, RETAIN)")
	flag.StringVar(&opts.DeploymentGroup, "g", opts.DeploymentGroup, "Deployment group ID")
	flag.StringVar(&opts.DeploymentGroup, "deployment-group", opts.DeploymentGroup, "Deployment group ID")
	flag.StringVar(&opts.DeploymentGroupName, "d", opts.DeploymentGroupName, "Deployment group name")
	flag.StringVar(&opts.DeploymentGroupName, "deployment-group-name", opts.DeploymentGroupName, "Deployment group name")
	flag.StringVar(&opts.ApplicationName, "a", "", "Application name")
	flag.StringVar(&opts.ApplicationName, "application-name", "", "Application name")
	flag.StringVar(&eventsStr, "e", "", "Comma-separated lifecycle events")
	flag.StringVar(&eventsStr, "events", "", "Comma-separated lifecycle events")
	flag.StringVar(&opts.ConfigFile, "c", "", "Path to agent configuration file")
	flag.StringVar(&opts.ConfigFile, "agent-configuration-file", "", "Path to agent configuration file")
	flag.StringVar(&opts.AppSpecFilename, "A", opts.AppSpecFilename, "AppSpec filename")
	flag.StringVar(&opts.AppSpecFilename, "appspec-filename", opts.AppSpecFilename, "AppSpec filename")

	flag.Usage = func() {
		fmt.Fprintf(os.Stderr, "Usage: codedeploy-local [flags]\n\nRuns a local deployment without the CodeDeploy service.\n\nFlags:\n")
		flag.PrintDefaults()
	}

	flag.Parse()

	if opts.BundleLocation == "" {
		cwd, err := os.Getwd()
		if err != nil {
			fmt.Fprintf(os.Stderr, "codedeploy-local: %s\n", err)
			os.Exit(1)
		}
		opts.BundleLocation = cwd
	}

	if eventsStr != "" {
		opts.Events = strings.Split(eventsStr, ",")
		for i := range opts.Events {
			opts.Events[i] = strings.TrimSpace(opts.Events[i])
		}
	}

	if err := localcli.Run(context.Background(), opts); err != nil {
		fmt.Fprintf(os.Stderr, "codedeploy-local: %s\n", err)
		os.Exit(1)
	}
}
