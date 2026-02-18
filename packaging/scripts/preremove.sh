#!/bin/sh
set -e
if command -v systemctl >/dev/null 2>&1; then
    systemctl stop codedeploy-agent.service >/dev/null 2>&1 || true
    systemctl disable codedeploy-agent.service >/dev/null 2>&1 || true
elif command -v rc-service >/dev/null 2>&1; then
    rc-service codedeploy-agent stop || true
    rc-update del codedeploy-agent default || true
fi
