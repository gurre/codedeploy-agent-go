#!/bin/bash
set -e

# Verify SELinux context
CONTEXT=$(stat -c %C /opt/cdagent-test-selinux/index.html)
echo "Context: $CONTEXT"
echo "$CONTEXT" | grep -q "httpd_sys_content_t" || exit 1

# Verify SELinux is enforcing
SELINUX_MODE=$(getenforce)
test "$SELINUX_MODE" = "Enforcing" || exit 1

echo "SELINUX_PASS" > /tmp/codedeploy-integ-proof
