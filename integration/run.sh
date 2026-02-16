#!/bin/bash
# Integration test runner for the Go CodeDeploy agent.
# Linux instances install the agent package (deb/rpm) from GitHub Releases at boot via UserData.
# The runner creates infrastructure, uploads deployment bundles to S3,
# triggers CodeDeploy deployments, and verifies lifecycle hook execution.
#
# Commands:
#   setup    - Create CloudFormation stack, upload deployment bundles
#   test     - Create deployments, wait for completion, verify hook execution
#   teardown - Empty S3 bucket, delete CloudFormation stack
#   all      - setup -> test -> teardown (teardown runs even if test fails)
#
# Required environment variables:
#   CDAGENT_VERSION       - Agent release version (e.g. 0.1.0)
#
# Optional environment variables:
#   CDAGENT_STACK_PREFIX  - CloudFormation stack prefix (default: cdagent-integ)
#   AWS_DEFAULT_REGION    - AWS region (default: us-east-1)


set -euo pipefail

export AWS_PAGER=""

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
REPO_DIR="$(cd "${SCRIPT_DIR}/.." && pwd)"
TMP_DIR="${REPO_DIR}/tmp"

AGENT_VERSION="${CDAGENT_VERSION:?CDAGENT_VERSION must be set (e.g. 0.1.0)}"

STACK_PREFIX="${CDAGENT_STACK_PREFIX:-cdagent-integ}"
STACK_NAME="${STACK_PREFIX}"
REGION="${AWS_DEFAULT_REGION:-us-east-1}"

OS_NAMES=(al2023 al2 ubuntu windows)

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
# Infrastructure
# ---------------------------------------------------------------------------

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
            "ParameterKey=VpcId,ParameterValue=${VPC_ID}" \
            "ParameterKey=AgentVersion,ParameterValue=${AGENT_VERSION}" \
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
    INSTANCE_ID_WINDOWS=$(echo "${outputs}" | grep -w "InstanceIdWindows" | awk '{print $NF}')

    DG_AL2023=$(echo "${outputs}" | grep -w "DgAl2023Name" | awk '{print $NF}')
    DG_AL2=$(echo "${outputs}" | grep -w "DgAl2Name" | awk '{print $NF}')
    DG_UBUNTU=$(echo "${outputs}" | grep -w "DgUbuntuName" | awk '{print $NF}')
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
# Bundles
# ---------------------------------------------------------------------------
upload_bundles() {
    log "Creating and uploading bundle ZIPs"
    mkdir -p "${TMP_DIR}"

    # Upload all bundles in integration/bundles/
    for bundle_dir in "${SCRIPT_DIR}"/bundles/*; do
        if [[ ! -d "${bundle_dir}" ]]; then
            continue
        fi
        local bundle_name
        bundle_name=$(basename "${bundle_dir}")
        log "Zipping ${bundle_name}"
        (cd "${bundle_dir}" && zip -r "${TMP_DIR}/bundle-${bundle_name}.zip" .)
        aws s3 cp "${TMP_DIR}/bundle-${bundle_name}.zip" \
            "s3://${BUCKET}/bundles/bundle-${bundle_name}.zip" --region "${REGION}"
    done
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

# Find deployment directory by searching for deployment ID.
# Returns full path to deployment directory or empty string if not found.
find_deployment_dir() {
    local instance_id="$1"
    local deployment_id="$2"
    local os_name="$3"
    local root_dir="${4:-/opt/codedeploy-agent/deployment-root}"

    if [[ "${os_name}" == "windows" ]]; then
        # Windows: search in C:\codedeploy-agent\deployment-root\*\{deployment-id}
        local search_cmd="Get-ChildItem -Path 'C:\\codedeploy-agent\\deployment-root\\*\\${deployment_id}' -Directory -ErrorAction SilentlyContinue | Select-Object -First 1 -ExpandProperty FullName"
        run_ssm_command "${instance_id}" "AWS-RunPowerShellScript" "commands=['${search_cmd}']" 2>/dev/null || echo ""
    else
        # Linux: use find to search for deployment ID directory
        local search_cmd="find ${root_dir} -maxdepth 2 -type d -name '${deployment_id}' -print -quit 2>/dev/null || echo ''"
        run_ssm_command "${instance_id}" "AWS-RunShellScript" "commands=['${search_cmd}']" 2>/dev/null || echo ""
    fi
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
        windows) echo "${INSTANCE_ID_WINDOWS}" ;;
    esac
}

get_dg_name() {
    local os_name="$1"
    case "${os_name}" in
        al2023)  echo "${DG_AL2023}" ;;
        al2)     echo "${DG_AL2}" ;;
        ubuntu)  echo "${DG_UBUNTU}" ;;
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

    log "Creating deployment for ${os_name} (DG=${dg_name})" >&2
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

    # Strip carriage returns (Windows CRLF -> LF)
    proof=$(echo "${proof}" | tr -d '\r')

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

# Collect comprehensive logs from Linux instances.
# Includes journalctl, main log, rotated logs, shared deployment log, and per-deployment logs.
collect_logs_linux() {
    local instance_id="$1"
    local deployment_id="${2:-}"

    local output=""

    # Collect journalctl output (last 500 lines)
    output+="$(echo '=== journalctl (CodeDeploy Agent Service) ===')"
    output+=$'\n'
    output+="$(run_ssm_command "${instance_id}" "AWS-RunShellScript" \
        "commands=['journalctl -u codedeploy-agent --no-pager -n 500 2>/dev/null || echo \"(no journal)\"']" 2>/dev/null || echo "(failed to collect)")"
    output+=$'\n\n'

    # Collect main agent log (last 1000 lines)
    output+="$(echo '=== Main Agent Log (codedeploy-agent.log) ===')"
    output+=$'\n'
    output+="$(run_ssm_command "${instance_id}" "AWS-RunShellScript" \
        "commands=['tail -n 1000 /var/log/aws/codedeploy-agent/codedeploy-agent.log 2>/dev/null || echo \"(file not found)\"']" 2>/dev/null || echo "(failed to collect)")"
    output+=$'\n\n'

    # Collect rotated logs (.log.1 through .log.8, last 500 lines each)
    for i in {1..8}; do
        local log_file="/var/log/aws/codedeploy-agent/codedeploy-agent.log.${i}"
        output+="$(echo "=== Rotated Agent Log (codedeploy-agent.log.${i}) ===")"
        output+=$'\n'
        output+="$(run_ssm_command "${instance_id}" "AWS-RunShellScript" \
            "commands=['tail -n 500 ${log_file} 2>/dev/null || echo \"(file not found)\"']" 2>/dev/null || echo "(failed to collect)")"
        output+=$'\n\n'
    done

    # Collect shared deployment log (last 500 lines)
    output+="$(echo '=== Shared Deployment Log (codedeploy-agent-deployments.log) ===')"
    output+=$'\n'
    output+="$(run_ssm_command "${instance_id}" "AWS-RunShellScript" \
        "commands=['tail -n 500 /opt/codedeploy-agent/deployment-root/deployment-logs/codedeploy-agent-deployments.log 2>/dev/null || echo \"(file not found)\"']" 2>/dev/null || echo "(failed to collect)")"
    output+=$'\n\n'

    # If deployment_id provided, collect per-deployment logs
    if [[ -n "${deployment_id}" ]]; then
        output+="$(echo "=== Per-Deployment Script Log (scripts.log for ${deployment_id}) ===")"
        output+=$'\n'

        local dep_dir
        dep_dir=$(find_deployment_dir "${instance_id}" "${deployment_id}" "linux")

        if [[ -n "${dep_dir}" && "${dep_dir}" != "(file not found)" ]]; then
            output+="Found deployment directory: ${dep_dir}"
            output+=$'\n\n'

            # Collect scripts.log (full file)
            output+="$(run_ssm_command "${instance_id}" "AWS-RunShellScript" \
                "commands=['cat ${dep_dir}/logs/scripts.log 2>/dev/null || echo \"(file not found)\"']" 2>/dev/null || echo "(failed to collect)")"
            output+=$'\n\n'

            # List deployment directory structure
            output+="$(echo '=== Deployment Directory Structure ===')"
            output+=$'\n'
            output+="$(run_ssm_command "${instance_id}" "AWS-RunShellScript" \
                "commands=['ls -lR ${dep_dir} 2>/dev/null || echo \"(failed to list)\"']" 2>/dev/null || echo "(failed to collect)")"
            output+=$'\n'
        else
            output+="Deployment directory not found for ${deployment_id}"
            output+=$'\n'
        fi
    fi

    echo "${output}"
}

# Collect comprehensive logs from Windows instances.
# Includes agent logs, UserData log, and per-deployment logs.
collect_logs_windows() {
    local instance_id="$1"
    local deployment_id="${2:-}"

    local output=""

    # Collect agent logs (last 500 lines each)
    output+="$(echo '=== Agent stdout Log (agent-stdout.log) ===')"
    output+=$'\n'
    output+="$(run_ssm_command "${instance_id}" "AWS-RunPowerShellScript" \
        "commands=['Get-Content C:\\codedeploy-agent\\logs\\agent-stdout.log -Tail 500 -ErrorAction SilentlyContinue']" 2>/dev/null || echo "(failed to collect)")"
    output+=$'\n\n'

    output+="$(echo '=== Agent run Log (agent-run.log) ===')"
    output+=$'\n'
    output+="$(run_ssm_command "${instance_id}" "AWS-RunPowerShellScript" \
        "commands=['Get-Content C:\\codedeploy-agent\\logs\\agent-run.log -Tail 500 -ErrorAction SilentlyContinue']" 2>/dev/null || echo "(failed to collect)")"
    output+=$'\n\n'

    output+="$(echo '=== Agent stderr Log (agent-stderr.log) ===')"
    output+=$'\n'
    output+="$(run_ssm_command "${instance_id}" "AWS-RunPowerShellScript" \
        "commands=['Get-Content C:\\codedeploy-agent\\logs\\agent-stderr.log -Tail 500 -ErrorAction SilentlyContinue']" 2>/dev/null || echo "(failed to collect)")"
    output+=$'\n\n'

    # Collect UserData log (last 500 lines)
    output+="$(echo '=== UserData Execution Log ===')"
    output+=$'\n'
    output+="$(run_ssm_command "${instance_id}" "AWS-RunPowerShellScript" \
        "commands=['Get-Content C:\\ProgramData\\Amazon\\EC2-Windows\\Launch\\Log\\UserdataExecution.log -Tail 500 -ErrorAction SilentlyContinue']" 2>/dev/null || echo "(failed to collect)")"
    output+=$'\n\n'

    # If deployment_id provided, collect per-deployment logs
    if [[ -n "${deployment_id}" ]]; then
        output+="$(echo "=== Per-Deployment Script Log (scripts.log for ${deployment_id}) ===")"
        output+=$'\n'

        local dep_dir
        dep_dir=$(find_deployment_dir "${instance_id}" "${deployment_id}" "windows")

        if [[ -n "${dep_dir}" && "${dep_dir}" != "(file not found)" ]]; then
            output+="Found deployment directory: ${dep_dir}"
            output+=$'\n\n'

            # Collect scripts.log (full file)
            output+="$(run_ssm_command "${instance_id}" "AWS-RunPowerShellScript" \
                "commands=['Get-Content \"${dep_dir}\\logs\\scripts.log\" -ErrorAction SilentlyContinue']" 2>/dev/null || echo "(failed to collect)")"
            output+=$'\n\n'

            # List deployment directory structure
            output+="$(echo '=== Deployment Directory Structure ===')"
            output+=$'\n'
            output+="$(run_ssm_command "${instance_id}" "AWS-RunPowerShellScript" \
                "commands=['Get-ChildItem -Path \"${dep_dir}\" -Recurse -ErrorAction SilentlyContinue | Format-Table -AutoSize']" 2>/dev/null || echo "(failed to collect)")"
            output+=$'\n'
        else
            output+="Deployment directory not found for ${deployment_id}"
            output+=$'\n'
        fi
    fi

    echo "${output}"
}

# Main log collection orchestrator.
# Collects comprehensive logs from an instance, optionally for a specific deployment.
# Parameters:
#   instance_id  - EC2 instance ID
#   os_name      - OS name (al2023, al2, ubuntu, windows)
#   deployment_id - Optional deployment ID for per-deployment logs
#   bundle_name  - Optional bundle name for filename
collect_logs() {
    local instance_id="$1"
    local os_name="$2"
    local deployment_id="${3:-}"
    local bundle_name="${4:-}"

    mkdir -p "${TMP_DIR}"

    # Determine output filename
    local output_file
    if [[ -n "${deployment_id}" && -n "${bundle_name}" ]]; then
        output_file="${TMP_DIR}/integ-${os_name}-${bundle_name}-${deployment_id}.log"
        log "Collecting logs from ${os_name} (${instance_id}) for deployment ${deployment_id}"
    else
        output_file="${TMP_DIR}/integ-${os_name}-agent.log"
        log "Collecting general logs from ${os_name} (${instance_id})"
    fi

    # Create header with deployment context
    local header=""
    header+="======================================================================"
    header+=$'\n'
    header+="Log Collection: ${os_name}"
    header+=$'\n'
    if [[ -n "${deployment_id}" ]]; then
        header+="Deployment ID: ${deployment_id}"
        header+=$'\n'
    fi
    if [[ -n "${bundle_name}" ]]; then
        header+="Bundle: ${bundle_name}"
        header+=$'\n'
    fi
    header+="Timestamp: $(date -u +%Y-%m-%dT%H:%M:%SZ)"
    header+=$'\n'
    header+="======================================================================"
    header+=$'\n\n'

    # Collect OS-specific logs
    local log_content
    if [[ "${os_name}" == "windows" ]]; then
        log_content=$(collect_logs_windows "${instance_id}" "${deployment_id}")
    else
        log_content=$(collect_logs_linux "${instance_id}" "${deployment_id}")
    fi

    # Write to file
    echo "${header}${log_content}" > "${output_file}"
    log "Saved to ${output_file}"
}

# ---------------------------------------------------------------------------
# Feature-specific test helpers
# ---------------------------------------------------------------------------
test_bundle() {
    local bundle_name="$1"
    local deployment_group="$2"
    local instance_id="$3"
    local expected_proof="$4"

    log "Testing bundle ${bundle_name} on ${deployment_group}"

    local deployment_id
    deployment_id=$(aws deploy create-deployment \
        --application-name "${CODEDEPLOY_APP}" \
        --deployment-group-name "${deployment_group}" \
        --revision "revisionType=S3,s3Location={bucket=${BUCKET},key=bundles/bundle-${bundle_name}.zip,bundleType=zip}" \
        --region "${REGION}" \
        --query "deploymentId" \
        --output text)

    log "Created deployment ${deployment_id}"

    local test_result=0
    if ! wait_deployment "${deployment_id}"; then
        err "Deployment ${deployment_id} failed"
        test_result=1
    fi

    if [[ ${test_result} -eq 0 ]]; then
        if ! verify_proof "${instance_id}" "${expected_proof}"; then
            test_result=1
        fi
    fi

    # Extract OS name from instance_id for log collection
    local os_name
    case "${instance_id}" in
        "${INSTANCE_ID_AL2023}") os_name="al2023" ;;
        "${INSTANCE_ID_AL2}") os_name="al2" ;;
        "${INSTANCE_ID_UBUNTU}") os_name="ubuntu" ;;
        "${INSTANCE_ID_WINDOWS}") os_name="windows" ;;
        *) os_name="unknown" ;;
    esac

    # Collect logs regardless of test result
    collect_logs "${instance_id}" "${os_name}" "${deployment_id}" "${bundle_name}" || true

    return ${test_result}
}

verify_proof() {
    local instance_id="$1"
    local expected="$2"

    log "Verifying proof on ${instance_id}"

    local proof
    proof=$(run_ssm_command "${instance_id}" "AWS-RunShellScript" \
        "commands=['cat /tmp/codedeploy-integ-proof 2>/dev/null || echo MISSING']")

    if echo "${proof}" | grep -q "${expected}"; then
        log "  [PASS] Found ${expected}"
        return 0
    else
        err "  [FAIL] Expected ${expected}, got: ${proof}"
        return 1
    fi
}

test_file_exists_behavior() {
    local dg="$1"
    local instance_id="$2"

    log "Testing file_exists_behavior sequence"

    # Deploy initial
    if ! test_bundle "file-exists-initial" "${dg}" "${instance_id}" "INITIAL"; then
        err "Initial deployment failed"
        return 1
    fi

    # Verify content
    local content
    content=$(run_ssm_command "${instance_id}" "AWS-RunShellScript" \
        "commands=['cat /opt/cdagent-test-feb/testfile.txt']")
    if ! echo "${content}" | grep -q "INITIAL"; then
        err "Initial content wrong: ${content}"
        return 1
    fi

    # Deploy with OVERWRITE
    if ! test_bundle "file-exists-overwrite" "${dg}" "${instance_id}" "OVERWRITTEN"; then
        err "Overwrite deployment failed"
        return 1
    fi

    content=$(run_ssm_command "${instance_id}" "AWS-RunShellScript" \
        "commands=['cat /opt/cdagent-test-feb/testfile.txt']")
    if ! echo "${content}" | grep -q "OVERWRITTEN"; then
        err "Overwrite failed: ${content}"
        return 1
    fi

    # Deploy with RETAIN
    if ! test_bundle "file-exists-retain" "${dg}" "${instance_id}" "RETAINED"; then
        err "Retain deployment failed"
        return 1
    fi

    content=$(run_ssm_command "${instance_id}" "AWS-RunShellScript" \
        "commands=['cat /opt/cdagent-test-feb/testfile.txt']")
    if ! echo "${content}" | grep -q "OVERWRITTEN"; then
        err "Retain failed - content changed: ${content}"
        return 1
    fi

    log "file_exists_behavior sequence PASSED"
    return 0
}

# ---------------------------------------------------------------------------
# Phase orchestration
# ---------------------------------------------------------------------------
do_setup() {
    create_stack
    load_stack_outputs
    upload_bundles

    log "Waiting for SSM on all instances"
    for os_name in "${OS_NAMES[@]}"; do
        local iid
        iid=$(get_instance_id "${os_name}")
        wait_for_ssm "${iid}"
    done

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

        # Collect deployment-specific logs
        collect_logs "${iid}" "${os_name}" "${deploy_id}" "linux" || true
    done

    # Collect general logs as fallback for debugging
    log "Collecting general agent logs from all instances"
    for i in "${!OS_NAMES[@]}"; do
        local os_name="${OS_NAMES[$i]}"
        local iid
        iid=$(get_instance_id "${os_name}")
        collect_logs "${iid}" "${os_name}" || true
    done

    # Feature-specific tests
    log ""
    log "=============================="
    log " Running Feature Tests"
    log "=============================="

    # files-basic test (AL2023, Ubuntu)
    log "Testing files-basic feature"
    if ! test_bundle "files-basic" "${DG_AL2023}" "${INSTANCE_ID_AL2023}" "FILES_BASIC_PASS"; then
        err "files-basic test failed on AL2023"
        failed=$((failed + 1))
    fi

    if ! test_bundle "files-basic" "${DG_UBUNTU}" "${INSTANCE_ID_UBUNTU}" "FILES_BASIC_PASS"; then
        err "files-basic test failed on Ubuntu"
        failed=$((failed + 1))
    fi

    # permissions-selinux test (AL2023)
    log "Testing permissions-selinux feature"
    if ! test_bundle "permissions-selinux" "${DG_AL2023}" "${INSTANCE_ID_AL2023}" "SELINUX_PASS"; then
        err "permissions-selinux test failed on AL2023"
        failed=$((failed + 1))
    fi

    # hooks-multiple test (AL2023)
    log "Testing hooks-multiple feature"
    if ! test_bundle "hooks-multiple" "${DG_AL2023}" "${INSTANCE_ID_AL2023}" "HOOKS_MULTIPLE_PASS"; then
        err "hooks-multiple test failed on AL2023"
        failed=$((failed + 1))
    fi

    # file_exists_behavior sequence (AL2023)
    log "Testing file_exists_behavior feature"
    if ! test_file_exists_behavior "${DG_AL2023}" "${INSTANCE_ID_AL2023}"; then
        err "file_exists_behavior test failed on AL2023"
        failed=$((failed + 1))
    fi

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
    echo ""
    echo "Feature Tests:"
    echo "  files-basic (AL2023, Ubuntu)"
    echo "  permissions-selinux (AL2023)"
    echo "  hooks-multiple (AL2023)"
    echo "  file_exists_behavior (AL2023)"
    echo "=============================="
    echo ""

    if [[ ${failed} -gt 0 ]]; then
        err "${failed} test(s) failed"
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
