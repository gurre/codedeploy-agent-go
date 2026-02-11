# Alternative CodeDeploy Agent in Golang
A rewrite of the AWS Codedeploy Agent in Golang.

## Differences from AWS CodeDeploy Ruby Agent

This Go implementation provides the following improvements over the official AWS CodeDeploy Ruby agent:

1. **Memory Efficiency**: Runs on ~25MB RAM with no memory growth, compared to Ruby agent's higher memory footprint and gradual growth over time
2. **Platform Validation**: Rejects deployments where appspec.yml `os:` field doesn't match the runtime platform, preventing cross-platform execution errors
3. **Single Binary**: No Ruby runtime dependency, simpler deployment and maintenance
4. **Fast Startup**: Near-instantaneous startup time vs Ruby interpreter initialization
5. **Resource Efficiency**: Lower CPU usage during idle periods
6. **Crash Recovery**: Fail-fast design with clear error messages for invariant violations

### Compatibility

The Go agent implements the same CodeDeploy protocol and appspec.yml format as the Ruby agent, ensuring compatibility with existing deployments and AWS CodeDeploy service.

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
VERSION=0.4.0
case $(uname -m) in x86_64) ARCH=amd64;; aarch64) ARCH=arm64;; armv7l) ARCH=armv7;; esac
curl -fsSLO "https://github.com/gurre/codedeploy-agent-go/releases/download/v${VERSION}/codedeploy-agent_${VERSION}_linux_${ARCH}.rpm"
dnf install -y "./codedeploy-agent_${VERSION}_linux_${ARCH}.rpm"
systemctl enable --now codedeploy-agent
```

### Amazon Linux 2

```bash
VERSION=0.4.0
case $(uname -m) in x86_64) ARCH=amd64;; aarch64) ARCH=arm64;; armv7l) ARCH=armv7;; esac
curl -fsSLO "https://github.com/gurre/codedeploy-agent-go/releases/download/v${VERSION}/codedeploy-agent_${VERSION}_linux_${ARCH}.rpm"
yum localinstall -y "./codedeploy-agent_${VERSION}_linux_${ARCH}.rpm"
systemctl enable --now codedeploy-agent
```

### Ubuntu / Debian

```bash
VERSION=0.4.0
case $(uname -m) in x86_64) ARCH=amd64;; aarch64) ARCH=arm64;; armv7l) ARCH=armv7;; esac
curl -fsSLO "https://github.com/gurre/codedeploy-agent-go/releases/download/v${VERSION}/codedeploy-agent_${VERSION}_linux_${ARCH}.deb"
dpkg -i "./codedeploy-agent_${VERSION}_linux_${ARCH}.deb"
systemctl enable --now codedeploy-agent
```

### Alpine Linux

```bash
VERSION=0.4.0
case $(uname -m) in x86_64) ARCH=amd64;; aarch64) ARCH=arm64;; armv7l) ARCH=armv7;; esac
wget -q "https://github.com/gurre/codedeploy-agent-go/releases/download/v${VERSION}/codedeploy-agent_${VERSION}_linux_${ARCH}.apk"
apk add --allow-untrusted "./codedeploy-agent_${VERSION}_linux_${ARCH}.apk"
rc-update add codedeploy-agent default
rc-service codedeploy-agent start
```

### Arch Linux

```bash
VERSION=0.4.0
case $(uname -m) in x86_64) ARCH=amd64;; aarch64) ARCH=arm64;; armv7l) ARCH=armv7;; esac
curl -fsSLO "https://github.com/gurre/codedeploy-agent-go/releases/download/v${VERSION}/codedeploy-agent_${VERSION}_linux_${ARCH}.pkg.tar.zst"
pacman -U --noconfirm "./codedeploy-agent_${VERSION}_linux_${ARCH}.pkg.tar.zst"
systemctl enable --now codedeploy-agent
```

### Windows Server

```powershell
<powershell>
$VERSION = "0.4.0"
$VERSION_DASHED = $VERSION.Replace('.', '-')
$ARCH = if ($env:PROCESSOR_ARCHITECTURE -eq 'ARM64') { 'arm64' } else { 'x86_64' }

mkdir -Force C:\codedeploy-agent\bin, C:\codedeploy-agent\deployment-root, `
    C:\codedeploy-agent\logs, C:\codedeploy-agent\conf

curl.exe --ssl-no-revoke -fsSLo C:\codedeploy-agent\bin\codedeploy-agent.exe `
    "https://github.com/gurre/codedeploy-agent-go/releases/download/v${VERSION}/codedeploy-agent_${VERSION_DASHED}_windows_${ARCH}.exe"

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
VERSION=0.4.0
VERSION_DASHED=$(echo "$VERSION" | tr '.' '-')
case $(uname -m) in x86_64) ARCH=x86_64;; aarch64) ARCH=arm64;; armv7l) ARCH=armv7;; esac
curl -fsSLo codedeploy-agent "https://github.com/gurre/codedeploy-agent-go/releases/download/v${VERSION}/codedeploy-agent_${VERSION_DASHED}_linux_${ARCH}"
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

## AppSpec File Support

The Go agent implements the AWS CodeDeploy AppSpec file format as documented in the [AWS CodeDeploy AppSpec File Reference](https://docs.aws.amazon.com/codedeploy/latest/userguide/reference-appspec-file.html). This section documents supported features, platform-specific behaviors, and any differences from the AWS specification.

### Supported Features

| Section | Linux | Windows | Notes |
|---------|-------|---------|-------|
| `version` | ✓ | ✓ | Only 0.0 supported |
| `os` | ✓ | ✓ | Must match runtime platform |
| `files` | ✓ | ✓ | Source/destination mapping |
| `permissions` | ✓ | ✗ | Linux only |
| `hooks` | ✓ | ✓ | All 9 lifecycle events |

### Lifecycle Events

The agent supports all 9 lifecycle events for EC2/On-Premises deployments:

| Event | Description | Deployment Root |
|-------|-------------|-----------------|
| `ApplicationStop` | Stop the application before update | Current |
| `DownloadBundle` | Reserved - agent downloads bundle | N/A |
| `BeforeInstall` | Pre-installation tasks | MostRecent |
| `Install` | Reserved - agent copies files | N/A |
| `AfterInstall` | Post-installation tasks | Current |
| `ApplicationStart` | Start the application | Current |
| `ValidateService` | Verify deployment success | Current |
| `BeforeBlockTraffic` | Pre-deregistration tasks (load balancers) | Current |
| `AfterBlockTraffic` | Post-deregistration tasks | Current |
| `BeforeAllowTraffic` | Pre-registration tasks (load balancers) | Current |
| `AfterAllowTraffic` | Post-registration tasks | Current |

**Deployment Root** indicates which deployment directory scripts run from:
- **Current**: `/opt/codedeploy-agent/deployment-root/.../d-DEPLOYID-XXXX/deployment-archive/`
- **MostRecent**: Last successful deployment or Current if first deployment
- **LastSuccessful**: Last successful deployment (empty if first deployment)

### Files Section

Maps source files from the deployment bundle to destination paths on the instance:

```yaml
files:
  - source: /              # Copy all files from bundle root
    destination: /opt/app
  - source: config/
    destination: /etc/app/
```

- **source**: Path within the deployment bundle (relative or `/` for all)
- **destination**: Absolute path on the target instance
- Directories are copied recursively

#### file_exists_behavior

Controls what happens when destination files already exist:

```yaml
file_exists_behavior: OVERWRITE
```

| Value | Behavior |
|-------|----------|
| `DISALLOW` | Fail deployment if files exist (default) |
| `OVERWRITE` | Replace existing files |
| `RETAIN` | Keep existing files, skip copying |

**Note**: `file_exists_behavior` applies globally to **all** files in the deployment. It cannot be specified per-file.

### Permissions Section (Linux Only)

Set ownership, mode, ACLs, and SELinux context on deployed files:

```yaml
permissions:
  - object: /opt/app
    pattern: "**"
    owner: deploy
    group: www-data
    mode: "0755"
    type:
      - file
      - directory
```

**Fields**:
- **object**: Base path (required)
- **pattern**: Glob pattern for matching files (default: `**`)
- **except**: Glob patterns to exclude
- **owner**: File owner (user name or UID)
- **group**: File group (group name or GID)
- **mode**: Octal permissions (e.g., `"0755"`)
- **type**: Apply to `file`, `directory`, or both (default: both)

**ACLs** (POSIX Access Control Lists):
```yaml
permissions:
  - object: /opt/app/data
    type:
      - directory
    acls:
      entries:
        - user:deploy:rwx
        - group:web:r-x
        - d:user::rwx          # Default ACL for new files
```

**SELinux Context**:
```yaml
permissions:
  - object: /opt/app
    context:
      user: system_u
      role: object_r
      type: httpd_sys_content_t
      range:
        low: s0
        high: s0:c0.c1023
```

### Hooks Section

Execute scripts during deployment lifecycle events:

```yaml
hooks:
  ApplicationStop:
    - location: scripts/stop.sh
      timeout: 300
      runas: root
```

**Fields**:
- **location**: Path to script within deployment bundle (required)
- **timeout**: Script timeout in seconds (default: 3600, max per event: 3600)
- **runas**: User to run script as (Linux only, requires passwordless sudo)

**Environment Variables** available to hook scripts:
- `LIFECYCLE_EVENT`: Current event name
- `DEPLOYMENT_ID`: CodeDeploy deployment ID
- `APPLICATION_NAME`: CodeDeploy application name
- `DEPLOYMENT_GROUP_NAME`: CodeDeploy deployment group name
- `DEPLOYMENT_GROUP_ID`: CodeDeploy deployment group ID

### Platform-Specific Behaviors

#### Linux
- **Archive formats**: tar, tar.gz (tgz), zip all supported
- **Permissions**: Full support for mode, owner, group, ACLs, SELinux
- **runas**: Supported (requires passwordless sudo for non-root users)
- **Hook scripts**: Executed with shell (`/bin/sh`)

#### Windows
- **Archive formats**: zip, tar, tar.gz all supported (AWS only supports zip)
- **Permissions**: Not supported (section rejected at parse time)
- **runas**: Not supported (AWS limitation, rejected at parse time)
- **Hook scripts**: Executed with cmd.exe or PowerShell based on extension

### Validation and Constraints

The agent validates AppSpec files at deployment time and rejects:
- **Version mismatch**: Only `version: 0.0` is accepted
- **OS mismatch**: AppSpec `os:` field must match runtime platform (prevents cross-platform execution)
- **Cumulative timeout**: Total of all script timeouts in one lifecycle event cannot exceed 3600 seconds
- **runas on Windows**: Rejected at parse time (AWS does not support)
- **Permissions on Windows**: Rejected at parse time (Linux-only feature)

### Examples

Example AppSpec files are available in the integration test bundles:
- `integration/bundles/*/appspec.yml` - Platform-specific examples with hooks, files, and permissions

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
git tag v0.4.0
git push origin v0.4.0
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
- **Published GitHub release** matching `CDAGENT_VERSION` — instances download
  the agent binary from GitHub Releases at boot.
- **`zip`** — used to package the test bundles.

### What It Creates

The runner applies a CloudFormation stack (`integration/cloudformation.yml`)
that provisions:

- 1 IAM role + instance profile (SSM and S3 read access for the instances)
- 1 IAM service role for CodeDeploy
- 1 S3 bucket for deployment bundles
- 4 EC2 instances (one per OS), egress-only security group, no SSH
- 1 CodeDeploy application with 4 deployment groups (one per instance, matched
  by EC2 tags)

All resources are prefixed with `CDAGENT_STACK_PREFIX` (default
`cdagent-integ`) and confined to one stack for clean teardown.

### Running

```
CDAGENT_VERSION=0.4.0 ./integration/run.sh all
```

This runs `setup` → `test` → `teardown` in sequence. Teardown runs even if
tests fail. You can also run each phase independently — see
`integration/README.md` for the full command reference, configuration details,
and troubleshooting.
