# Feature Parity: Ruby Agent vs Go Agent

Feature parity mapping between the [AWS CodeDeploy Ruby agent](https://docs.aws.amazon.com/codedeploy/latest/userguide/codedeploy-agent.html) (versions 1.0.1.854–1.8.0) and this Go reimplementation. Sources: [agent version history](https://docs.aws.amazon.com/codedeploy/latest/userguide/codedeploy-agent.html), [agent configuration reference](https://docs.aws.amazon.com/codedeploy/latest/userguide/reference-agent-configuration.html), and the Go codebase.

## Core Protocol

| Feature | Ruby | Go | Notes |
|---|---|---|---|
| HTTPS polling (port 443) | :white_check_mark: | :white_check_mark: | Go adds `wait_between_runs_active` for faster pickup |
| SigV4 request signing | :white_check_mark: | :white_check_mark: | |
| Bounded concurrency | :white_check_mark: (1) | :white_check_mark: (16) | Go supports concurrent deployments |
| Exponential backoff with jitter | Partial | :white_check_mark: | Go uses equal-jitter strategy |
| Throttle detection (60 s delay) | :white_check_mark: | :white_check_mark: | |
| Crash recovery via tracking files | :white_check_mark: (since 1.0.1.1518) | :white_check_mark: | Go: 24 h TTL auto-cleanup |

## Identity & Credentials

| Feature | Ruby | Go |
|---|---|---|
| EC2 IMDS v1 | :white_check_mark: | :white_check_mark: (fallback) |
| EC2 IMDS v2 | :white_check_mark: (since 1.2.1) | :white_check_mark: (default) |
| `disable_imds_v1` config | :white_check_mark: (since 1.7.0) | :white_check_mark: |
| On-premises config file | :white_check_mark: | :white_check_mark: |
| STS session credentials | :white_check_mark: | :white_check_mark: |
| Environment variable overrides | :x: | :white_check_mark: (`AWS_REGION`, `AWS_HOST_IDENTIFIER`) |

## Deployment Lifecycle Hooks

All 7 scriptable hooks and 4 reserved hooks are supported in both agents.

## Deployment Types

| Feature | Ruby | Go |
|---|---|---|
| In-place | :white_check_mark: | :white_check_mark: |
| Blue/green | :white_check_mark: | :white_check_mark: |
| Blue/green rollback | :white_check_mark: | :white_check_mark: |
| Auto Scaling launch/termination | :white_check_mark: | :white_check_mark: |

## Bundle Download

| Feature | Ruby | Go |
|---|---|---|
| S3 download | :white_check_mark: | :white_check_mark: (with ETag verification) |
| S3 version ID | :white_check_mark: | :white_check_mark: |
| GitHub tarball/zipball | :white_check_mark: | :white_check_mark: |
| GitHub token auth | :white_check_mark: | :white_check_mark: |
| Archive: tar | :white_check_mark: | :white_check_mark: |
| Archive: tgz | :white_check_mark: | :white_check_mark: |
| Archive: zip | :white_check_mark: | :white_check_mark: |
| Subdirectory bundles | :white_check_mark: (since 1.1.2) | :white_check_mark: (strip-leading-directory) |

## AppSpec Features

| Feature | Ruby | Go |
|---|---|---|
| `files` section | :white_check_mark: | :white_check_mark: |
| `file_exists_behavior` (DISALLOW/OVERWRITE/RETAIN) | :white_check_mark: (since 1.3.2) | :white_check_mark: |
| `permissions` section (mode, owner, group) | :white_check_mark: | :white_check_mark: |
| ACLs | :white_check_mark: | :white_check_mark: |
| SELinux context | :white_check_mark: | :white_check_mark: |
| OS validation | :white_check_mark: | :white_check_mark: |
| `.yaml` extension support | :white_check_mark: (since 1.3.2) | :white_check_mark: |
| Custom appspec filename (local deploy) | :white_check_mark: (since 1.3.2) | :white_check_mark: |

## Hook Script Features

| Feature | Ruby | Go |
|---|---|---|
| `LIFECYCLE_EVENT` env var | :white_check_mark: | :white_check_mark: |
| `DEPLOYMENT_ID` env var | :white_check_mark: | :white_check_mark: |
| `APPLICATION_NAME` env var | :white_check_mark: | :white_check_mark: |
| `DEPLOYMENT_GROUP_NAME` env var | :white_check_mark: | :white_check_mark: |
| `DEPLOYMENT_GROUP_ID` env var | :white_check_mark: (since 1.0.1.854) | :white_check_mark: |
| S3 bundle env vars (`BUNDLE_BUCKET` etc.) | :white_check_mark: (since 1.4.0) | :white_check_mark: |
| Script timeout | :white_check_mark: | :white_check_mark: |
| `runas` support | :white_check_mark: | :white_check_mark: |
| Windows: rejects permissions/runas at parse time | :x: (runtime error) | :white_check_mark: (parse-time rejection) |

## Configuration Options

| Feature | Ruby | Go | Notes |
|---|---|---|---|
| `root_dir` | :white_check_mark: | :white_check_mark: | |
| `log_dir` | :white_check_mark: | :white_check_mark: | |
| `pid_dir` | :white_check_mark: | :white_check_mark: | |
| `program_name` | :white_check_mark: | :x: | Not needed (single binary) |
| `verbose` | :white_check_mark: | :x: | Parsed but not acted on |
| `log_aws_wire` | :white_check_mark: | :x: | Not implemented |
| `wait_between_runs` | :white_check_mark: | :white_check_mark: | |
| `wait_between_runs_active` | :x: | :white_check_mark: | Go-only: faster polling during active deployments |
| `wait_after_error` | :x: | :white_check_mark: | Go-only |
| `http_read_timeout` | :x: | :white_check_mark: | Go-only |
| `kill_agent_max_wait_time_seconds` | :x: | :white_check_mark: | Go-only: graceful shutdown timeout |
| `max_revisions` | :white_check_mark: (since 1.0.1.966) | :white_check_mark: | |
| `proxy_uri` | :white_check_mark: (since 1.0.1.824) | :white_check_mark: | |
| `on_premises_config_file` | :white_check_mark: | :white_check_mark: | |
| `enable_auth_policy` | :white_check_mark: (since 1.1.2) | :white_check_mark: | |
| `disable_imds_v1` | :white_check_mark: (since 1.7.0) | :white_check_mark: | |
| `use_fips_mode` | :white_check_mark: (since 1.0.1.1597) | :white_check_mark: | |
| `use_dual_stack` | :x: | :white_check_mark: | Go-only |
| `deploy_control_endpoint` | :white_check_mark: | :white_check_mark: | |
| `s3_endpoint_override` | :x: | :white_check_mark: | Go-only |
| `enable_deployments_log` | :x: | :white_check_mark: | Go-only: per-deployment log file |
| `ongoing_deployment_tracking` | :x: | :white_check_mark: | Go-only |

## Operations & Management

| Feature | Ruby | Go |
|---|---|---|
| Self-install command | :white_check_mark: | :white_check_mark: (declarative reconciliation) |
| Auto-updater | Removed in 1.1.0 (use SSM) | :x: |
| Log rotation | :white_check_mark: (daily, 7-day retention) | :white_check_mark: |
| Revision cleanup | :white_check_mark: | :white_check_mark: |
| Local CLI (`codedeploy-local`) | :white_check_mark: (since 1.0.1.1352) | :white_check_mark: |
| Version tracking (`.version` files) | :white_check_mark: (since 1.0.1.854) | :x: |
| CloudWatch Logs integration | :white_check_mark: (since 1.0.1.854) | :x: |
| Non-root user profiles | :white_check_mark: (since 1.0.1.966) | :x: |
| Long file paths (Windows) | :white_check_mark: (since 1.4.0) | N/A |
| VPC endpoints / PrivateLink | :white_check_mark: (since 1.3.2) | :white_check_mark: (via custom endpoints) |
| SHA-256 hash algorithm | :white_check_mark: (since 1.0.1.854) | :white_check_mark: (PKCS7 verification) |

## Platform Support

| Platform | Ruby | Go |
|---|---|---|
| Amazon Linux 2 | :white_check_mark: | :white_check_mark: |
| Amazon Linux 2023 | :white_check_mark: | :white_check_mark: |
| RHEL 7 | :white_check_mark: | :x: |
| RHEL 8 | :white_check_mark: | :white_check_mark: (rpm) |
| RHEL 9 | :white_check_mark: (since 1.7.0) | :white_check_mark: (rpm) |
| Ubuntu 18.04 | :white_check_mark: | :x: |
| Ubuntu 20.04 | :white_check_mark: | :white_check_mark: (deb) |
| Ubuntu 22.04 | :white_check_mark: | :white_check_mark: (deb) |
| Windows Server 2019 | :white_check_mark: | :white_check_mark: (exe) |
| Windows Server 2022 | :white_check_mark: | :white_check_mark: (exe) |
| Debian | :x: | :white_check_mark: (deb) |
| Alpine | :x: | :white_check_mark: (apk) |
| Arch Linux | :x: | :white_check_mark: (pkg.tar.zst) |
| macOS | :x: | :white_check_mark: (standalone) |
| FreeBSD | :x: | :white_check_mark: (standalone) |
| ARM (arm64) | :white_check_mark: | :white_check_mark: |
| ARM (armv7) | :x: | :white_check_mark: |

## Go Agent Exclusive Improvements

- Single static binary — no runtime dependency (Ruby agent requires Ruby 2.x/3.x)
- ~25 MB RAM footprint vs Ruby's higher memory use
- 16 concurrent deployments vs Ruby's sequential execution
- Active polling interval (`wait_between_runs_active`) for faster deployment pickup
- Parse-time validation catches configuration and appspec errors earlier
- Fail-fast design with crash recovery via 24 h TTL tracking files
- Process group isolation for reliable timeout kills
