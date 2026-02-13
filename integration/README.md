# Integration Tests

EC2-based integration tests for the Go CodeDeploy agent. Verifies the agent can resolve identity via IMDS, poll for commands, download bundles from S3, install files, and execute lifecycle hook scripts on all 4 supported platforms.

## Platforms

- Amazon Linux 2023
- Amazon Linux 2
- Ubuntu 22.04
- Windows Server 2022

## Prerequisites

- AWS CLI v2 configured with credentials that can create CloudFormation stacks, EC2 instances, IAM roles, S3 buckets, and CodeDeploy resources
- Published GitHub release matching `CDAGENT_VERSION` (Linux instances install deb/rpm packages at boot)
- `zip` command

## Environment Variables

| Variable | Default | Purpose |
|----------|---------|---------|
| `CDAGENT_VERSION` | *(required)* | Agent release version to download from GitHub Releases |
| `CDAGENT_STACK_PREFIX` | `cdagent-integ` | CloudFormation stack and resource name prefix |
| `AWS_DEFAULT_REGION` | `us-east-1` | AWS region for all resources |

## Commands

```
CDAGENT_VERSION=0.1.0 ./integration/run.sh setup     # Create stack, upload bundles
CDAGENT_VERSION=0.1.0 ./integration/run.sh test      # Deploy bundles, verify hook execution
CDAGENT_VERSION=0.1.0 ./integration/run.sh teardown  # Empty bucket, delete stack
CDAGENT_VERSION=0.1.0 ./integration/run.sh all       # setup -> test -> teardown (teardown always runs)
```

## How It Works

1. **Setup** creates a CloudFormation stack with 4 EC2 instances. Linux instances install the agent package (deb/rpm) from GitHub Releases via UserData at boot; systemd manages the agent lifecycle. Windows downloads the binary directly. The runner uploads deployment bundle ZIPs to S3.

2. **Test** creates one CodeDeploy deployment per OS (each deployment group targets a single instance by EC2 tags). On first deployment, CodeDeploy executes 4 of the 9 lifecycle hooks — `BeforeInstall`, `AfterInstall`, `ApplicationStart`, `ValidateService`. The remaining hooks are skipped for two reasons: `ApplicationStop` is skipped because no prior revision exists, and the traffic hooks (`BeforeBlockTraffic`, `AfterBlockTraffic`, `BeforeAllowTraffic`, `AfterAllowTraffic`) are skipped because no load balancer is attached to the deployment group. Each hook script appends its event name to a proof file. The runner reads the proof file via SSM and checks for the expected events.

3. **Teardown** empties the S3 bucket and deletes the CloudFormation stack.

## Configuration

Instances are provisioned with `codedeployagent.yml` containing `wait_between_runs: 5` (mapped to `PollInterval` in `configloader.LoadAgent()`). See `state/config.Default()` for all default values. Windows instances override `root_dir` and `log_dir` to Windows paths.

## CloudFormation Parameters

See `cloudformation.yml` Parameters section. All AMI parameters resolve via SSM Parameter Store. The `InstanceType` defaults to `t3.micro`.

## Log Collection

The integration test framework automatically collects comprehensive logs after each deployment completes (success or failure). This provides detailed visibility into agent behavior and helps debug deployment issues.

### Log Files

**Deployment-specific logs** (collected after each deployment):
- `tmp/integ-{os}-{bundle-name}-{deployment-id}.log` — Logs for a specific deployment
- Example: `tmp/integ-al2023-files-basic-d-ABC123XYZ.log`

**General logs** (collected once at end of all tests as fallback):
- `tmp/integ-{os}-agent.log` — General agent logs for an OS
- Example: `tmp/integ-al2023-agent.log`

### Log Content

Each log file contains:

**Linux (AL2023, AL2, Ubuntu)**:
- Journalctl output (last 500 lines) — systemd service logs
- Main agent log: `/var/log/aws/codedeploy-agent/codedeploy-agent.log` (last 1000 lines)
- Rotated logs: `.log.1` through `.log.8` (last 500 lines each)
- Shared deployment log: `/opt/codedeploy-agent/deployment-root/deployment-logs/codedeploy-agent-deployments.log` (last 500 lines)
- Per-deployment script log: `{deployment-root}/{dg-id}/{d-id}/logs/scripts.log` (full file, contains hook execution details)
- Deployment directory listing (for debugging file placement)

**Windows**:
- Agent logs: `C:\codedeploy-agent\logs\agent-stdout.log`, `agent-run.log`, `agent-stderr.log` (last 500 lines each)
- UserData log: `C:\ProgramData\Amazon\EC2-Windows\Launch\Log\UserdataExecution.log` (last 500 lines)
- Per-deployment script log: `{deployment-root}\{dg-id}\{d-id}\logs\scripts.log` (full file)
- Deployment directory listing

### Log File Structure

Each log file starts with a header showing context:

```
======================================================================
Log Collection: al2023
Deployment ID: d-ABC123XYZ
Bundle: files-basic
Timestamp: 2026-02-11T15:45:30Z
======================================================================
```

Followed by sections for each log source, marked with headers like:
- `=== journalctl (CodeDeploy Agent Service) ===`
- `=== Main Agent Log (codedeploy-agent.log) ===`
- `=== Per-Deployment Script Log (scripts.log for d-...) ===`

### Correlating Logs with Tests

Use the filename to identify which test generated the logs:

- Main OS deployments: `integ-{os}-linux-d-{id}.log`
- Feature tests: `integ-{os}-{bundle-name}-d-{id}.log`
- General debugging: `integ-{os}-agent.log`

Example workflow after a test failure:
1. Identify the failed test from console output
2. Find the corresponding log file: `tmp/integ-ubuntu-permissions-acl-d-*.log`
3. Check the `scripts.log` section for hook execution errors
4. Review the main agent log section for agent-level errors
5. Compare timestamps across sections to understand the sequence of events

### Path Discovery

Per-deployment logs are located in deployment directories like `/opt/codedeploy-agent/deployment-root/{dg-id}/{d-id}/logs/scripts.log`. The deployment group ID (`dg-id`) is an internal AWS identifier not visible to the test runner.

The framework automatically discovers these paths by searching for the deployment ID as a directory name. If a deployment directory is found, the log file will show:

```
Found deployment directory: /opt/codedeploy-agent/deployment-root/dg-xyz/d-ABC123XYZ
```

If not found (e.g., deployment failed before creating the directory):

```
Deployment directory not found for d-ABC123XYZ
```

## Troubleshooting

**SSM not connecting** — Instances need outbound HTTPS (port 443) to reach SSM endpoints. The security group allows this by default. Verify the instance profile has `AmazonSSMManagedInstanceCore`. Check SSM agent status:

```
aws ssm describe-instance-information --filters Key=InstanceIds,Values=<id> --region <region>
```

**Agent not starting** — Check the agent journal and log via SSM, or review the general log file `tmp/integ-{os}-agent.log` which includes journalctl output:

```
aws ssm send-command --instance-ids <id> --document-name AWS-RunShellScript \
    --parameters 'commands=["journalctl -u codedeploy-agent --no-pager"]'
```

**Deployment stuck** — The agent polls every 5 seconds. If a deployment stays `InProgress` beyond 300 seconds, check the agent service is running (`systemctl status codedeploy-agent`). Deployment-specific logs in `tmp/integ-{os}-{bundle}-{deployment-id}.log` show agent activity during that deployment.

**Hook script failures** — Check the per-deployment `scripts.log` section in the log file. It shows stdout/stderr from each hook script and any errors that occurred during execution.

**File placement issues** — The deployment directory listing shows exactly which files were installed and their permissions. Compare against the AppSpec `files` section.

**Log collection failures** — If a log section shows `(failed to collect)` or `(file not found)`, the SSM command failed or the file doesn't exist. This is normal for rotated logs that haven't been created yet. Check the general agent log for SSM connectivity issues.

**Manually inspect instances** — All instances support SSM Session Manager. No SSH keys or inbound ports are needed:

```
aws ssm start-session --target <instance-id> --region <region>
```
