# Integration Tests

EC2-based integration tests for the Go CodeDeploy agent. Verifies the agent can resolve identity via IMDS, poll for commands, download bundles from S3, install files, and execute lifecycle hook scripts on all 5 supported platforms.

## Platforms

- Amazon Linux 2023
- Amazon Linux 2
- Ubuntu 22.04
- RHEL 9
- Windows Server 2022

## Prerequisites

- AWS CLI v2 configured with credentials that can create CloudFormation stacks, EC2 instances, IAM roles, S3 buckets, and CodeDeploy resources
- Go toolchain (for cross-compilation)
- `zip` command

## Environment Variables

| Variable | Default | Purpose |
|----------|---------|---------|
| `CDAGENT_STACK_PREFIX` | `cdagent-integ` | CloudFormation stack and resource name prefix |
| `AWS_DEFAULT_REGION` | `us-east-1` | AWS region for all resources |
| `RHEL9_AMI_ID` | latest via `ec2 describe-images` | Override the auto-resolved RHEL 9 AMI |

## Commands

```
./integration/run.sh setup     # Build, create stack, upload, install agent
./integration/run.sh test      # Deploy bundles, verify hook execution
./integration/run.sh teardown  # Empty bucket, delete stack
./integration/run.sh all       # setup -> test -> teardown (teardown always runs)
```

## How It Works

1. **Setup** cross-compiles `cmd/codedeploy-agent` for `linux/amd64` and `windows/amd64`, creates a CloudFormation stack with 5 EC2 instances, uploads binaries and bundle ZIPs to S3, and installs the agent on each instance via SSM Run Command.

2. **Test** creates one CodeDeploy deployment per OS (each deployment group targets a single instance by EC2 tags). On first deployment, CodeDeploy executes 4 of the 9 lifecycle hooks — `BeforeInstall`, `AfterInstall`, `ApplicationStart`, `ValidateService`. The remaining hooks are skipped for two reasons: `ApplicationStop` is skipped because no prior revision exists, and the traffic hooks (`BeforeBlockTraffic`, `AfterBlockTraffic`, `BeforeAllowTraffic`, `AfterAllowTraffic`) are skipped because no load balancer is attached to the deployment group. Each hook script appends its event name to a proof file. The runner reads the proof file via SSM and checks for the expected events.

3. **Teardown** empties the S3 bucket and deletes the CloudFormation stack.

## Configuration

Instances are provisioned with `codedeployagent.yml` containing `wait_between_runs: 5` (mapped to `PollInterval` in `configloader.LoadAgent()`). See `state/config.Default()` for all default values. Windows instances override `root_dir` and `log_dir` to Windows paths.

## CloudFormation Parameters

See `cloudformation.yml` Parameters section. All AMI parameters except `Rhel9AmiId` resolve via SSM Parameter Store. The `InstanceType` defaults to `t3.micro`.

## Troubleshooting

**SSM not connecting** — Instances need outbound HTTPS (port 443) to reach SSM endpoints. The security group allows this by default. Verify the instance profile has `AmazonSSMManagedInstanceCore`. Check SSM agent status:

```
aws ssm describe-instance-information --filters Key=InstanceIds,Values=<id> --region <region>
```

**Agent not starting** — Check the agent log via SSM:

```
aws ssm send-command --instance-ids <id> --document-name AWS-RunShellScript \
    --parameters 'commands=["cat /var/log/aws/codedeploy-agent/agent-stdout.log"]'
```

**Deployment stuck** — The agent polls every 5 seconds. If a deployment stays `InProgress` beyond 300 seconds, check the agent process is running (`pgrep codedeploy-agent`). Agent logs are collected to `tmp/integ-<os>-agent.log` after each test.

**Manually inspect instances** — All instances support SSM Session Manager. No SSH keys or inbound ports are needed:

```
aws ssm start-session --target <instance-id> --region <region>
```
