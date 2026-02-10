#!/bin/bash
# Integration test runner for the Go CodeDeploy agent.
# Deploys the agent to 5 EC2 instances (AL2023, AL2, Ubuntu, RHEL9, Windows),
# triggers CodeDeploy deployments, and verifies lifecycle hook execution.
#
# Commands:
#   setup    - Build binaries, create CloudFormation stack, upload artifacts, install agent
#   test     - Create deployments, wait for completion, verify hook execution
#   teardown - Empty S3 bucket, delete CloudFormation stack
#   all      - setup -> test -> teardown (teardown runs even if test fails)
#
# Optional environment variables:
#   CDAGENT_STACK_PREFIX  - CloudFormation stack prefix (default: cdagent-integ)
#   AWS_DEFAULT_REGION    - AWS region (default: us-east-1)
#   RHEL9_AMI_ID          - Override RHEL 9 AMI (default: latest from ec2 describe-images)

set -euo pipefail

export AWS_PAGER=""

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
REPO_DIR="$(cd "${SCRIPT_DIR}/.." && pwd)"
TMP_DIR="${REPO_DIR}/tmp"
BIN_DIR="${REPO_DIR}/bin"

STACK_PREFIX="${CDAGENT_STACK_PREFIX:-cdagent-integ}"
STACK_NAME="${STACK_PREFIX}"
REGION="${AWS_DEFAULT_REGION:-us-east-1}"

OS_NAMES=(al2023 al2 ubuntu rhel9 windows)
LINUX_OS_NAMES=(al2023 al2 ubuntu rhel9)

# First-deployment hooks: ApplicationStop is skipped because no prior revision exists.
# BeforeBlockTraffic, AfterBlockTraffic, BeforeAllowTraffic, AfterAllowTraffic are
# skipped because no load balancer is attached to the deployment group.
EXPECTED_HOOKS="BeforeInstall AfterInstall ApplicationStart ValidateService"

# Track per-OS test results (indexed parallel to OS_NAMES).
TEST_RESULTS=()

# ---------------------------------------------------------------------------
# Logging
# ---------------------------------------------------------------------------
log() { echo "==> $*"; }
err() { echo "ERR $*" >&2; }

# ---------------------------------------------------------------------------
# Build
# ---------------------------------------------------------------------------
build_binaries() {
    log "Building linux/amd64 binary"
    CGO_ENABLED=0 GOOS=linux GOARCH=amd64 \
        go build -o "${BIN_DIR}/codedeploy-agent-linux-amd64" \
        "${REPO_DIR}/cmd/codedeploy-agent"

    log "Building windows/amd64 binary"
    CGO_ENABLED=0 GOOS=windows GOARCH=amd64 \
        go build -o "${BIN_DIR}/codedeploy-agent-windows-amd64.exe" \
        "${REPO_DIR}/cmd/codedeploy-agent"
}

# ---------------------------------------------------------------------------
# Infrastructure
# ---------------------------------------------------------------------------

# Resolve the latest RHEL 9 x86_64 HVM AMI from Red Hat's account.
# RHEL does not publish AMI IDs to SSM Parameter Store, so we query
# ec2 describe-images and sort by creation date.
resolve_rhel9_ami() {
    if [[ -n "${RHEL9_AMI_ID:-}" ]]; then
        log "Using provided RHEL9_AMI_ID=${RHEL9_AMI_ID}"
        return
    fi

    log "Resolving latest RHEL 9 AMI for ${REGION}"
    RHEL9_AMI_ID=$(aws ec2 describe-images \
        --owners 309956199498 \
        --filters \
            "Name=name,Values=RHEL-9.*_HVM-*-x86_64-*" \
            "Name=architecture,Values=x86_64" \
            "Name=state,Values=available" \
        --query "sort_by(Images, &CreationDate)[-1].ImageId" \
        --region "${REGION}" \
        --output text)

    if [[ -z "${RHEL9_AMI_ID}" || "${RHEL9_AMI_ID}" == "None" ]]; then
        err "Could not resolve RHEL 9 AMI in ${REGION}. Set RHEL9_AMI_ID manually."
        return 1
    fi

    log "Resolved RHEL9_AMI_ID=${RHEL9_AMI_ID}"
}

resolve_default_vpc() {
    if [[ -n "${VPC_ID:-}" ]]; then
        log "Using provided VPC_ID=${VPC_ID}"
        return
    fi

    log "Resolving default VPC for ${REGION}"
    VPC_ID=$(aws ec2 describe-vpcs \
        --filters "Name=is-default,Values=true" \
        --query "Vpcs[0].VpcId" \
        --region "${REGION}" \
        --output text)

    if [[ -z "${VPC_ID}" || "${VPC_ID}" == "None" ]]; then
        err "No default VPC in ${REGION}. Set VPC_ID manually."
        return 1
    fi

    log "Resolved VPC_ID=${VPC_ID}"
}

create_stack() {
    resolve_rhel9_ami
    resolve_default_vpc

    # Delete any pre-existing stack so create-stack does not collide.
    local status
    status=$(aws cloudformation describe-stacks \
        --stack-name "${STACK_NAME}" \
        --region "${REGION}" \
        --query "Stacks[0].StackStatus" \
        --output text 2>/dev/null || echo "DOES_NOT_EXIST")

    if [[ "${status}" != "DOES_NOT_EXIST" ]]; then
        log "Stack ${STACK_NAME} exists (${status}), tearing down first"
        load_stack_outputs 2>/dev/null || true
        delete_stack
    fi

    log "Creating CloudFormation stack ${STACK_NAME}"
    aws cloudformation create-stack \
        --stack-name "${STACK_NAME}" \
        --template-body "file://${SCRIPT_DIR}/cloudformation.yml" \
        --capabilities CAPABILITY_NAMED_IAM \
        --parameters \
            "ParameterKey=StackPrefix,ParameterValue=${STACK_PREFIX}" \
            "ParameterKey=Rhel9AmiId,ParameterValue=${RHEL9_AMI_ID}" \
            "ParameterKey=VpcId,ParameterValue=${VPC_ID}" \
        --region "${REGION}"

    log "Waiting for stack creation to complete"
    aws cloudformation wait stack-create-complete \
        --stack-name "${STACK_NAME}" \
        --region "${REGION}"
}

load_stack_outputs() {
    log "Loading stack outputs"
    local outputs
    outputs=$(aws cloudformation describe-stacks \
        --stack-name "${STACK_NAME}" \
        --region "${REGION}" \
        --query "Stacks[0].Outputs" \
        --output text)

    # Parse tab-separated output: each line is "KEY\tVALUE\t..."
    # Use grep -w (word boundary) to prevent partial matches (e.g. InstanceIdAl2 matching InstanceIdAl2023).
    BUCKET=$(echo "${outputs}" | grep -w "BucketName" | awk '{print $NF}')
    CODEDEPLOY_APP=$(echo "${outputs}" | grep -w "CodeDeployAppName" | awk '{print $NF}')

    INSTANCE_ID_AL2023=$(echo "${outputs}" | grep -w "InstanceIdAl2023" | awk '{print $NF}')
    INSTANCE_ID_AL2=$(echo "${outputs}" | grep -w "InstanceIdAl2" | awk '{print $NF}')
    INSTANCE_ID_UBUNTU=$(echo "${outputs}" | grep -w "InstanceIdUbuntu" | awk '{print $NF}')
    INSTANCE_ID_RHEL9=$(echo "${outputs}" | grep -w "InstanceIdRhel9" | awk '{print $NF}')
    INSTANCE_ID_WINDOWS=$(echo "${outputs}" | grep -w "InstanceIdWindows" | awk '{print $NF}')

    DG_AL2023=$(echo "${outputs}" | grep -w "DgAl2023Name" | awk '{print $NF}')
    DG_AL2=$(echo "${outputs}" | grep -w "DgAl2Name" | awk '{print $NF}')
    DG_UBUNTU=$(echo "${outputs}" | grep -w "DgUbuntuName" | awk '{print $NF}')
    DG_RHEL9=$(echo "${outputs}" | grep -w "DgRhel9Name" | awk '{print $NF}')
    DG_WINDOWS=$(echo "${outputs}" | grep -w "DgWindowsName" | awk '{print $NF}')

    log "Bucket=${BUCKET} App=${CODEDEPLOY_APP}"
}

delete_stack() {
    log "Emptying S3 bucket ${BUCKET:-unknown}"
    if [[ -n "${BUCKET:-}" ]]; then
        aws s3 rm "s3://${BUCKET}" --recursive --region "${REGION}" || true
    fi

    log "Deleting CloudFormation stack ${STACK_NAME}"
    aws cloudformation delete-stack \
        --stack-name "${STACK_NAME}" \
        --region "${REGION}"

    log "Waiting for stack deletion to complete"
    aws cloudformation wait stack-delete-complete \
        --stack-name "${STACK_NAME}" \
        --region "${REGION}"
}

# ---------------------------------------------------------------------------
# Artifacts
# ---------------------------------------------------------------------------
upload_artifacts() {
    log "Uploading agent binaries to S3"
    aws s3 cp "${BIN_DIR}/codedeploy-agent-linux-amd64" \
        "s3://${BUCKET}/agent/codedeploy-agent-linux-amd64" --region "${REGION}"
    aws s3 cp "${BIN_DIR}/codedeploy-agent-windows-amd64.exe" \
        "s3://${BUCKET}/agent/codedeploy-agent-windows-amd64.exe" --region "${REGION}"

    log "Creating and uploading bundle ZIPs"
    mkdir -p "${TMP_DIR}"

    (cd "${SCRIPT_DIR}/bundles/linux" && zip -r "${TMP_DIR}/bundle-linux.zip" .)
    (cd "${SCRIPT_DIR}/bundles/windows" && zip -r "${TMP_DIR}/bundle-windows.zip" .)

    aws s3 cp "${TMP_DIR}/bundle-linux.zip" \
        "s3://${BUCKET}/bundles/bundle-linux.zip" --region "${REGION}"
    aws s3 cp "${TMP_DIR}/bundle-windows.zip" \
        "s3://${BUCKET}/bundles/bundle-windows.zip" --region "${REGION}"
}

# ---------------------------------------------------------------------------
# SSM helpers
# ---------------------------------------------------------------------------
wait_for_ssm() {
    local instance_id="$1"
    local max_attempts=40
    local attempt=0

    log "Waiting for SSM on ${instance_id}"
    while [[ ${attempt} -lt ${max_attempts} ]]; do
        local status
        status=$(aws ssm describe-instance-information \
            --filters "Key=InstanceIds,Values=${instance_id}" \
            --region "${REGION}" \
            --query "InstanceInformationList[0].PingStatus" \
            --output text 2>/dev/null || echo "None")

        if [[ "${status}" == "Online" ]]; then
            log "SSM online for ${instance_id}"
            return 0
        fi

        attempt=$((attempt + 1))
        sleep 15
    done

    err "SSM timeout for ${instance_id}"
    return 1
}

run_ssm_command() {
    local instance_id="$1"
    local document="$2"
    shift 2
    local parameters="$*"

    local cmd_id
    cmd_id=$(aws ssm send-command \
        --instance-ids "${instance_id}" \
        --document-name "${document}" \
        --parameters "${parameters}" \
        --timeout-seconds 120 \
        --region "${REGION}" \
        --query "Command.CommandId" \
        --output text)

    # Wait for command to finish.
    local status="InProgress"
    local wait_attempts=0
    while [[ "${status}" == "InProgress" || "${status}" == "Pending" ]]; do
        sleep 5
        wait_attempts=$((wait_attempts + 1))
        if [[ ${wait_attempts} -gt 30 ]]; then
            err "SSM command ${cmd_id} timed out on ${instance_id}"
            return 1
        fi
        status=$(aws ssm get-command-invocation \
            --command-id "${cmd_id}" \
            --instance-id "${instance_id}" \
            --region "${REGION}" \
            --query "Status" \
            --output text 2>/dev/null || echo "InProgress")
    done

    if [[ "${status}" != "Success" ]]; then
        err "SSM command ${cmd_id} failed with status ${status} on ${instance_id}"
        aws ssm get-command-invocation \
            --command-id "${cmd_id}" \
            --instance-id "${instance_id}" \
            --region "${REGION}" \
            --query "StandardErrorContent" \
            --output text 2>/dev/null || true
        return 1
    fi

    # Return stdout.
    aws ssm get-command-invocation \
        --command-id "${cmd_id}" \
        --instance-id "${instance_id}" \
        --region "${REGION}" \
        --query "StandardOutputContent" \
        --output text
}

# ---------------------------------------------------------------------------
# Agent installation
# ---------------------------------------------------------------------------
install_agent_linux() {
    local instance_id="$1"
    log "Installing agent on Linux instance ${instance_id}"

    # Install AWS CLI v2 if missing (Ubuntu and RHEL 9 do not ship with it; Amazon Linux does).
    run_ssm_command "${instance_id}" "AWS-RunShellScript" \
        "commands=[
            'command -v aws >/dev/null || (curl -fsSL \"https://awscli.amazonaws.com/awscli-exe-linux-x86_64.zip\" -o /tmp/awscliv2.zip && cd /tmp && unzip -qo awscliv2.zip && ./aws/install)',
            'aws s3 cp s3://${BUCKET}/agent/codedeploy-agent-linux-amd64 /opt/codedeploy-agent/bin/codedeploy-agent --region ${REGION}',
            'chmod +x /opt/codedeploy-agent/bin/codedeploy-agent',
            'nohup /opt/codedeploy-agent/bin/codedeploy-agent /etc/codedeploy-agent/conf/codedeployagent.yml > /var/log/aws/codedeploy-agent/agent-stdout.log 2>&1 &',
            'sleep 2',
            'pgrep -f codedeploy-agent || (echo AGENT_NOT_RUNNING && exit 1)'
        ]"
}

install_agent_windows() {
    local instance_id="$1"
    log "Installing agent on Windows instance ${instance_id}"

    run_ssm_command "${instance_id}" "AWS-RunPowerShellScript" \
        "commands=[
            'Read-S3Object -BucketName ${BUCKET} -Key agent/codedeploy-agent-windows-amd64.exe -File C:\\codedeploy-agent\\bin\\codedeploy-agent.exe -Region ${REGION}',
            'Start-Process -FilePath C:\\codedeploy-agent\\bin\\codedeploy-agent.exe -ArgumentList C:\\codedeploy-agent\\conf\\codedeployagent.yml -WindowStyle Hidden -RedirectStandardOutput C:\\codedeploy-agent\\logs\\agent-stdout.log -RedirectStandardError C:\\codedeploy-agent\\logs\\agent-stderr.log',
            'Start-Sleep -Seconds 2',
            'if (-not (Get-Process codedeploy-agent -ErrorAction SilentlyContinue)) { Write-Error \"AGENT_NOT_RUNNING\"; exit 1 }'
        ]"
}

# ---------------------------------------------------------------------------
# Deployment
# ---------------------------------------------------------------------------
get_instance_id() {
    local os_name="$1"
    case "${os_name}" in
        al2023)  echo "${INSTANCE_ID_AL2023}" ;;
        al2)     echo "${INSTANCE_ID_AL2}" ;;
        ubuntu)  echo "${INSTANCE_ID_UBUNTU}" ;;
        rhel9)   echo "${INSTANCE_ID_RHEL9}" ;;
        windows) echo "${INSTANCE_ID_WINDOWS}" ;;
    esac
}

get_dg_name() {
    local os_name="$1"
    case "${os_name}" in
        al2023)  echo "${DG_AL2023}" ;;
        al2)     echo "${DG_AL2}" ;;
        ubuntu)  echo "${DG_UBUNTU}" ;;
        rhel9)   echo "${DG_RHEL9}" ;;
        windows) echo "${DG_WINDOWS}" ;;
    esac
}

get_bundle_key() {
    local os_name="$1"
    if [[ "${os_name}" == "windows" ]]; then
        echo "bundles/bundle-windows.zip"
    else
        echo "bundles/bundle-linux.zip"
    fi
}

create_deployment() {
    local os_name="$1"
    local dg_name
    dg_name=$(get_dg_name "${os_name}")
    local bundle_key
    bundle_key=$(get_bundle_key "${os_name}")

    log "Creating deployment for ${os_name} (DG=${dg_name})"
    aws deploy create-deployment \
        --application-name "${CODEDEPLOY_APP}" \
        --deployment-group-name "${dg_name}" \
        --revision "revisionType=S3,s3Location={bucket=${BUCKET},key=${bundle_key},bundleType=zip}" \
        --region "${REGION}" \
        --query "deploymentId" \
        --output text
}

wait_deployment() {
    local deployment_id="$1"
    local timeout=300
    local elapsed=0
    local backoff=5

    while [[ ${elapsed} -lt ${timeout} ]]; do
        local status
        status=$(aws deploy get-deployment \
            --deployment-id "${deployment_id}" \
            --region "${REGION}" \
            --query "deploymentInfo.status" \
            --output text)

        case "${status}" in
            Succeeded)
                log "Deployment ${deployment_id} succeeded"
                return 0
                ;;
            Failed|Stopped)
                err "Deployment ${deployment_id} ${status}"
                aws deploy get-deployment \
                    --deployment-id "${deployment_id}" \
                    --region "${REGION}" \
                    --query "deploymentInfo.errorInformation" \
                    --output text 2>/dev/null || true
                return 1
                ;;
        esac

        sleep "${backoff}"
        elapsed=$((elapsed + backoff))
        # Exponential backoff capped at 30s.
        if [[ ${backoff} -lt 30 ]]; then
            backoff=$((backoff * 2))
            if [[ ${backoff} -gt 30 ]]; then
                backoff=30
            fi
        fi
    done

    err "Deployment ${deployment_id} timed out after ${timeout}s"
    return 1
}

verify_hooks() {
    local instance_id="$1"
    local os_name="$2"

    log "Verifying hooks on ${os_name} (${instance_id})"

    local proof
    if [[ "${os_name}" == "windows" ]]; then
        proof=$(run_ssm_command "${instance_id}" "AWS-RunPowerShellScript" \
            "commands=['Get-Content C:\\codedeploy-integ-proof']")
    else
        proof=$(run_ssm_command "${instance_id}" "AWS-RunShellScript" \
            "commands=['cat /tmp/codedeploy-integ-proof']")
    fi

    log "Proof file for ${os_name}:"
    echo "${proof}"

    local missing=0
    for hook in ${EXPECTED_HOOKS}; do
        if echo "${proof}" | grep -q "^${hook}$"; then
            log "  [PASS] ${hook}"
        else
            err "  [FAIL] ${hook} missing from proof file"
            missing=$((missing + 1))
        fi
    done

    if [[ ${missing} -gt 0 ]]; then
        return 1
    fi
    return 0
}

collect_logs() {
    local instance_id="$1"
    local os_name="$2"

    log "Collecting agent logs from ${os_name} (${instance_id})"
    mkdir -p "${TMP_DIR}"

    local agent_log
    if [[ "${os_name}" == "windows" ]]; then
        agent_log=$(run_ssm_command "${instance_id}" "AWS-RunPowerShellScript" \
            "commands=['Get-Content C:\\codedeploy-agent\\logs\\agent-stdout.log -ErrorAction SilentlyContinue']" 2>/dev/null || echo "(no log)")
    else
        agent_log=$(run_ssm_command "${instance_id}" "AWS-RunShellScript" \
            "commands=['cat /var/log/aws/codedeploy-agent/agent-stdout.log 2>/dev/null || echo \"(no log)\"']" 2>/dev/null || echo "(no log)")
    fi

    echo "${agent_log}" > "${TMP_DIR}/integ-${os_name}-agent.log"
    log "Saved to ${TMP_DIR}/integ-${os_name}-agent.log"
}

# ---------------------------------------------------------------------------
# Phase orchestration
# ---------------------------------------------------------------------------
do_setup() {
    build_binaries
    create_stack
    load_stack_outputs
    upload_artifacts

    log "Waiting for SSM on all instances"
    for os_name in "${OS_NAMES[@]}"; do
        local iid
        iid=$(get_instance_id "${os_name}")
        wait_for_ssm "${iid}"
    done

    log "Installing agent on Linux instances"
    for os_name in "${LINUX_OS_NAMES[@]}"; do
        local iid
        iid=$(get_instance_id "${os_name}")
        install_agent_linux "${iid}"
    done

    log "Installing agent on Windows instance"
    install_agent_windows "${INSTANCE_ID_WINDOWS}"

    log "Sleeping 30s for CodeDeploy agent registration"
    sleep 30

    log "Setup complete"
}

do_test() {
    load_stack_outputs

    local failed=0

    # Create all deployments (indexed parallel to OS_NAMES).
    DEPLOY_IDS=()
    for i in "${!OS_NAMES[@]}"; do
        local os_name="${OS_NAMES[$i]}"
        DEPLOY_IDS[$i]=$(create_deployment "${os_name}")
        log "Deployment for ${os_name}: ${DEPLOY_IDS[$i]}"
    done

    # Wait and verify each deployment.
    for i in "${!OS_NAMES[@]}"; do
        local os_name="${OS_NAMES[$i]}"
        local deploy_id="${DEPLOY_IDS[$i]}"
        local iid
        iid=$(get_instance_id "${os_name}")

        if wait_deployment "${deploy_id}"; then
            if verify_hooks "${iid}" "${os_name}"; then
                TEST_RESULTS[$i]="PASS"
            else
                TEST_RESULTS[$i]="FAIL (hooks)"
                failed=$((failed + 1))
            fi
        else
            TEST_RESULTS[$i]="FAIL (deployment)"
            failed=$((failed + 1))
        fi

        collect_logs "${iid}" "${os_name}"
    done

    # Print summary.
    echo ""
    echo "=============================="
    echo " Integration Test Results"
    echo "=============================="
    printf "%-12s %s\n" "OS" "Result"
    printf "%-12s %s\n" "---" "------"
    for i in "${!OS_NAMES[@]}"; do
        printf "%-12s %s\n" "${OS_NAMES[$i]}" "${TEST_RESULTS[$i]}"
    done
    echo "=============================="
    echo ""

    if [[ ${failed} -gt 0 ]]; then
        err "${failed} OS(es) failed"
        return 1
    fi

    log "All integration tests passed"
    return 0
}

do_teardown() {
    # Load outputs if not already loaded (needed for bucket name).
    if [[ -z "${BUCKET:-}" ]]; then
        load_stack_outputs 2>/dev/null || true
    fi
    delete_stack
    log "Teardown complete"
}

do_all() {
    do_setup

    local test_rc=0
    do_test || test_rc=$?

    do_teardown
    exit "${test_rc}"
}

# ---------------------------------------------------------------------------
# Entry point
# ---------------------------------------------------------------------------
usage() {
    echo "Usage: $0 {setup|test|teardown|all}"
    exit 1
}

if [[ $# -lt 1 ]]; then
    usage
fi

command="$1"
case "${command}" in
    setup)    do_setup ;;
    test)     do_test ;;
    teardown) do_teardown ;;
    all)      do_all ;;
    *)        usage ;;
esac
