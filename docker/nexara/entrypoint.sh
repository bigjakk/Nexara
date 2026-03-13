#!/bin/sh
set -e

PUID="${PUID:-1000}"
PGID="${PGID:-1000}"

# Create nexara group and user if they don't already match
if ! getent group nexara >/dev/null 2>&1; then
    addgroup -g "$PGID" nexara
fi

if ! getent passwd nexara >/dev/null 2>&1; then
    adduser -u "$PUID" -G nexara -s /sbin/nologin -D -H nexara
fi

# Ensure the data directory exists and is owned by the app user
mkdir -p /data/nexara
chown -R "$PUID:$PGID" /data/nexara

# Drop privileges and run the application
exec su-exec "$PUID:$PGID" /nexara "$@"
