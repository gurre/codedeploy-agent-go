#!/bin/sh
set -e
if command -v systemctl >/dev/null 2>&1; then
    systemctl daemon-reload >/dev/null 2>&1 || true
    systemctl enable codedeploy-agent.service >/dev/null 2>&1 || true
    systemctl start codedeploy-agent.service >/dev/null 2>&1 || true
elif command -v rc-update >/dev/null 2>&1; then
    rc-update add codedeploy-agent default || true
    rc-service codedeploy-agent start || true
fi
