#!/bin/bash
set -e

# Verify file exists
test -f /opt/cdagent-test-feb/testfile.txt || exit 1

# Verify content is RETAINED (should still be OVERWRITTEN from previous deployment)
CONTENT=$(cat /opt/cdagent-test-feb/testfile.txt)
if [ "$CONTENT" != "OVERWRITTEN" ]; then
  echo "Expected OVERWRITTEN (retained), got: $CONTENT"
  exit 1
fi

echo "RETAINED" > /tmp/codedeploy-integ-proof
