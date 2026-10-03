#!/bin/sh
set -eu

DATA_DIR="${NYATUNNEL_DATA:-/data}"

mkdir -p "$DATA_DIR"
chown -R nyatunnel:nyatunnel "$DATA_DIR"

exec su-exec nyatunnel /usr/local/bin/nyatunnel-server "$@"
