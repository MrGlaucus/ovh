#!/bin/sh
set -eu

# named volume 初次挂载时由 Docker 创建为 root；启动时只修正 /data，
# 随后降权运行，避免应用进程拥有 root 权限。
mkdir -p /data/cache /data/logs
chown -R ovh:ovh /data
exec su-exec ovh:ovh /usr/local/bin/ovh-server
