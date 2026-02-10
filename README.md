# codedeploy-agent-go
A rewrite of the AWS Codedeploy Agent in Golang.

## Installation

Packages and binaries are published to
[GitHub Releases](https://github.com/gurre/codedeploy-agent-go/releases)
for every tagged version. Each release includes rpm, deb, apk, and archlinux
packages for Linux, and standalone binaries for Linux, macOS, FreeBSD, and
Windows.

All snippets below are EC2 UserData scripts. They auto-detect the instance
architecture (x86_64, arm64/Graviton, armv7). Replace `0.1.0` with the
desired release version.

### Amazon Linux 2023 / RHEL 9 / Fedora

```bash
VERSION=0.1.0
case $(uname -m) in x86_64) ARCH=amd64;; aarch64) ARCH=arm64;; armv7l) ARCH=armv7;; esac
curl -fsSLO "https://github.com/gurre/codedeploy-agent-go/releases/download/v${VERSION}/codedeploy-agent_${VERSION}_linux_${ARCH}.rpm"
dnf install -y "./codedeploy-agent_${VERSION}_linux_${ARCH}.rpm"
systemctl enable --now codedeploy-agent
```

### Amazon Linux 2

```bash
VERSION=0.1.0
case $(uname -m) in x86_64) ARCH=amd64;; aarch64) ARCH=arm64;; armv7l) ARCH=armv7;; esac
curl -fsSLO "https://github.com/gurre/codedeploy-agent-go/releases/download/v${VERSION}/codedeploy-agent_${VERSION}_linux_${ARCH}.rpm"
yum localinstall -y "./codedeploy-agent_${VERSION}_linux_${ARCH}.rpm"
systemctl enable --now codedeploy-agent
```

### Ubuntu / Debian

```bash
VERSION=0.1.0
case $(uname -m) in x86_64) ARCH=amd64;; aarch64) ARCH=arm64;; armv7l) ARCH=armv7;; esac
curl -fsSLO "https://github.com/gurre/codedeploy-agent-go/releases/download/v${VERSION}/codedeploy-agent_${VERSION}_linux_${ARCH}.deb"
dpkg -i "./codedeploy-agent_${VERSION}_linux_${ARCH}.deb"
systemctl enable --now codedeploy-agent
```

### Alpine Linux

```bash
VERSION=0.1.0
case $(uname -m) in x86_64) ARCH=amd64;; aarch64) ARCH=arm64;; armv7l) ARCH=armv7;; esac
wget -q "https://github.com/gurre/codedeploy-agent-go/releases/download/v${VERSION}/codedeploy-agent_${VERSION}_linux_${ARCH}.apk"
apk add --allow-untrusted "./codedeploy-agent_${VERSION}_linux_${ARCH}.apk"
rc-update add codedeploy-agent default
rc-service codedeploy-agent start
```

### Arch Linux

```bash
VERSION=0.1.0
case $(uname -m) in x86_64) ARCH=amd64;; aarch64) ARCH=arm64;; armv7l) ARCH=armv7;; esac
curl -fsSLO "https://github.com/gurre/codedeploy-agent-go/releases/download/v${VERSION}/codedeploy-agent_${VERSION}_linux_${ARCH}.pkg.tar.zst"
pacman -U --noconfirm "./codedeploy-agent_${VERSION}_linux_${ARCH}.pkg.tar.zst"
systemctl enable --now codedeploy-agent
```

### Windows Server

```powershell
<powershell>
$VERSION = "0.1.0"
$ARCH = if ($env:PROCESSOR_ARCHITECTURE -eq 'ARM64') { 'arm64' } else { 'x86_64' }

mkdir -Force C:\codedeploy-agent\bin, C:\codedeploy-agent\deployment-root, `
    C:\codedeploy-agent\logs, C:\codedeploy-agent\conf

curl.exe -fsSLo C:\codedeploy-agent\bin\codedeploy-agent.exe `
    "https://github.com/gurre/codedeploy-agent-go/releases/download/v${VERSION}/codedeploy-agent_windows_${ARCH}.exe"

$action = New-ScheduledTaskAction -Execute C:\codedeploy-agent\bin\codedeploy-agent.exe `
    -Argument "start"
$trigger = New-ScheduledTaskTrigger -AtStartup
Register-ScheduledTask -TaskName codedeploy-agent -Action $action -Trigger $trigger `
    -User SYSTEM -RunLevel Highest
Start-ScheduledTask -TaskName codedeploy-agent
</powershell>
```

### Linux (binary)

Downloads the standalone binary and uses the built-in `install` subcommand,
which detects the init system (systemd or SysV), creates directories, writes
a service file and default config, then enables and starts the service.

```bash
VERSION=0.1.0
case $(uname -m) in x86_64) ARCH=x86_64;; aarch64) ARCH=arm64;; armv7l) ARCH=armv7;; esac
curl -fsSLo codedeploy-agent "https://github.com/gurre/codedeploy-agent-go/releases/download/v${VERSION}/codedeploy-agent_linux_${ARCH}"
chmod +x codedeploy-agent
sudo ./codedeploy-agent install
```

Flags: `--no-start` to install without starting, `--install-dir` to change
the target directory.

### macOS / FreeBSD (binary)

Standalone binaries are available for macOS (x86_64, arm64) and FreeBSD
(x86_64, arm64). Download and run `install` as above, substituting `darwin`
or `freebsd` for the OS name in the URL.

### Build from source

```
GOOS=linux GOARCH=amd64 go build -o codedeploy-agent ./cmd/codedeploy-agent
sudo ./codedeploy-agent install
```

### What gets created

```
/opt/codedeploy-agent/
├── bin/
│   └── codedeploy-agent              # agent binary
├── certs/                             # CA chain for deployment signing
├── deployment-root/                   # deployment artifacts
/etc/codedeploy-agent/conf/
    └── codedeployagent.yml            # config (not overwritten on re-install)
/etc/systemd/system/codedeploy-agent.service  # systemd
  or /etc/init.d/codedeploy-agent              # sysv
```

### Managing the service

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
Amazon Linux 2, Ubuntu 22.04, and Windows Server 2022.

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
- 4 EC2 instances (one per OS), egress-only security group, no SSH
- 1 CodeDeploy application with 4 deployment groups (one per instance, matched
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
