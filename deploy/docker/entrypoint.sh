#!/bin/sh
set -eu

DATA_DIR="${NYATUNNEL_DATA:-/data}"

mkdir -p "$DATA_DIR"
chown -R nyatunnel:nyatunnel "$DATA_DIR"
if [ -n "${NYATUNNEL_SECRETS_KEY_FILE:-}" ]; then
	key_dir="$(dirname "$NYATUNNEL_SECRETS_KEY_FILE")"
	mkdir -p "$key_dir"
	chown -R nyatunnel:nyatunnel "$key_dir"
	chmod 700 "$key_dir"
fi

exec su-exec nyatunnel /usr/local/bin/nyatunnel-server "$@"
