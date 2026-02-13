#!/bin/bash
set -e

# Verify file existence
test -f /opt/cdagent-test/config.txt || exit 1
test -f /opt/cdagent-test/data.json || exit 1
test -f /opt/cdagent-test/nested/deep.log || exit 1

# Verify file modes
MODE_CONFIG=$(stat -c %a /opt/cdagent-test/config.txt)
test "$MODE_CONFIG" = "644" || exit 1

MODE_DATA=$(stat -c %a /opt/cdagent-test/data.json)
test "$MODE_DATA" = "600" || exit 1

MODE_NESTED=$(stat -c %a /opt/cdagent-test/nested)
test "$MODE_NESTED" = "755" || exit 1

# Verify ownership
OWNER=$(stat -c %U:%G /opt/cdagent-test/config.txt)
test "$OWNER" = "root:root" || exit 1

echo "FILES_BASIC_PASS" > /tmp/codedeploy-integ-proof
