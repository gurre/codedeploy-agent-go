#!/bin/bash
set -e

# Verify file exists
test -f /opt/cdagent-test-feb/testfile.txt || exit 1

# Verify content
CONTENT=$(cat /opt/cdagent-test-feb/testfile.txt)
if [ "$CONTENT" != "INITIAL" ]; then
  echo "Expected INITIAL, got: $CONTENT"
  exit 1
fi

echo "INITIAL" > /tmp/codedeploy-integ-proof
