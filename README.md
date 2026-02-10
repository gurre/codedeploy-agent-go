# codedeploy-agent-go
A rewrite of the AWS Codedeploy Agent in Golang.

## Installation

The agent is a single static binary that self-installs onto the host. Build it,
copy it to the target machine, and run `install` as root. The installer detects
the init system (systemd or SysV), creates directories, writes a service file
and default config, then enables and starts the service.

### Build

```
GOOS=linux GOARCH=amd64 go build -o codedeploy-agent ./cmd/codedeploy-agent
```

### Amazon Linux 2023 / RHEL 9 (systemd)

```
sudo ./codedeploy-agent install
```

Registers a systemd unit at `/etc/systemd/system/codedeploy-agent.service`
(`Type=exec`, restarts on failure).

### Ubuntu 22.04 (systemd)

```
sudo ./codedeploy-agent install
```

Same as above — Ubuntu 22.04 uses systemd.

### Amazon Linux 2 (systemd)

```
sudo ./codedeploy-agent install
```

Same as above — Amazon Linux 2 uses systemd.

### Older SysV-only hosts

On hosts without systemd (detected by the absence of `/run/systemd/system`),
the installer writes an init script to `/etc/init.d/codedeploy-agent` and
enables it via `chkconfig`.

```
sudo ./codedeploy-agent install
```

### Install without starting

```
sudo ./codedeploy-agent install --no-start
```

### Custom install directory

```
sudo ./codedeploy-agent install --install-dir /opt/my-agent
```

### What gets created

```
/opt/codedeploy-agent/
├── bin/
│   └── codedeploy-agent              # self-copied binary
├── deployment-root/                   # deployment artifacts
/var/log/aws/codedeploy-agent/        # log directory
/etc/codedeploy-agent/conf/
    └── codedeployagent.yml            # default config (not overwritten on re-install)
/etc/systemd/system/codedeploy-agent.service  # systemd
  or /etc/init.d/codedeploy-agent              # sysv
```

Re-running `install` is idempotent: existing config is preserved, the binary
is only replaced when its content has changed, and the service file is kept
in sync with the embedded version.

### Managing the service

After installation the agent runs as a systemd/SysV service named
`codedeploy-agent`.

```
sudo systemctl status codedeploy-agent
sudo systemctl restart codedeploy-agent
sudo journalctl -u codedeploy-agent
```

## Releasing

Releases are built locally with [GoReleaser](https://goreleaser.com/) and
published to GitHub Releases. A release produces compressed archives, bare
binaries, and Linux packages (deb, rpm, apk, archlinux) for all supported
architectures.

### Prerequisites

- [GoReleaser](https://goreleaser.com/install/) installed locally
- A `GITHUB_TOKEN` with `repo` scope (for uploading release assets)

### Steps

```
git tag v0.1.0
git push origin v0.1.0
GITHUB_TOKEN=<token> goreleaser release --clean
```

### Dry run

Build all artifacts locally without publishing:

```
goreleaser release --snapshot --clean
```

Artifacts are written to `dist/`.

## Integration Tests

End-to-end tests that deploy the agent to real EC2 instances and run a
CodeDeploy deployment against each supported platform: Amazon Linux 2023,
Amazon Linux 2, Ubuntu 22.04, RHEL 9, and Windows Server 2022.

### Requirements

- **AWS credentials** with permissions to create CloudFormation stacks, EC2
  instances, IAM roles/instance profiles, S3 buckets, and CodeDeploy
  applications. The caller must also be able to send SSM Run Commands.
- **Go toolchain** — the runner cross-compiles `cmd/codedeploy-agent` for
  `linux/amd64` and `windows/amd64`.
- **`zip`** — used to package the test bundles.

### What It Creates

The runner applies a CloudFormation stack (`integration/cloudformation.yml`)
that provisions:

- 1 IAM role + instance profile (SSM and S3 read access for the instances)
- 1 IAM service role for CodeDeploy
- 1 S3 bucket for agent binaries and deployment bundles
- 5 EC2 instances (one per OS), egress-only security group, no SSH
- 1 CodeDeploy application with 5 deployment groups (one per instance, matched
  by EC2 tags)

All resources are prefixed with `CDAGENT_STACK_PREFIX` (default
`cdagent-integ`) and confined to one stack for clean teardown.

### Running

```
./integration/run.sh all
```

This runs `setup` → `test` → `teardown` in sequence. Teardown runs even if
tests fail. You can also run each phase independently — see
`integration/README.md` for the full command reference, configuration details,
and troubleshooting.
