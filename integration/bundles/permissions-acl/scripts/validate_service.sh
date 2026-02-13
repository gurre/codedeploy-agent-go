#!/bin/bash
set -e

# Verify directory ACLs
getfacl /opt/cdagent-test-acl | grep -q "user::rwx" || exit 1
getfacl /opt/cdagent-test-acl | grep -q "default:user::rwx" || exit 1

# Verify file ACLs
getfacl /opt/cdagent-test-acl/readme.txt | grep -q "user::rw-" || exit 1

echo "ACL_PASS" > /tmp/codedeploy-integ-proof
