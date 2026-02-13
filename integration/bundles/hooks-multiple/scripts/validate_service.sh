#!/bin/bash
set -e

# Verify execution order
ORDER=$(cat /tmp/hook-order)
EXPECTED="STEP1
STEP2
STEP3
STEP4
STEP5"

if [ "$ORDER" != "$EXPECTED" ]; then
  echo "Order mismatch!"
  echo "Expected:"
  echo "$EXPECTED"
  echo "Got:"
  echo "$ORDER"
  exit 1
fi

echo "HOOKS_MULTIPLE_PASS" > /tmp/codedeploy-integ-proof
