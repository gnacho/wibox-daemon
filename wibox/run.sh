#!/bin/sh
# Reads the add-on options and hands them to the daemon as environment. The
# daemon speaks the vendor cloud chain only, so no LAN address is configured.
set -e

OPTIONS=/data/options.json

if [ ! -f "$OPTIONS" ]; then
    echo "wibox: falta $OPTIONS" >&2
    exit 1
fi

value() {
    jq -r --arg key "$1" '.[$key] // ""' "$OPTIONS"
}

WIBOX_ACCOUNT="$(value account)"
WIBOX_PASSWORD="$(value password)"
WIBOX_CLIENT_ID="$(value client_id)"
WIBOX_DEVICE_ID="$(value device_id)"
WIBOX_UNLOCK_PASSWORD="$(value unlock_password)"
WIBOX_API_TOKEN="$(value api_token)"
WIBOX_REMOTE_ATTEMPTS="$(value attempts)"
WIBOX_AWAKE_POLL="$(value awake_poll)"
WIBOX_VIDEO_WAIT="$(value video_wait)"

for required in WIBOX_ACCOUNT WIBOX_PASSWORD WIBOX_DEVICE_ID WIBOX_UNLOCK_PASSWORD; do
    eval "current=\$$required"
    if [ -z "$current" ]; then
        echo "wibox: falta la opcion ${required}" >&2
        exit 1
    fi
done

WIBOX_REMOTE=1
WIBOX_REMOTE_ONLY=1
WIBOX_API_PORT=8087

export WIBOX_ACCOUNT WIBOX_PASSWORD WIBOX_CLIENT_ID WIBOX_DEVICE_ID
export WIBOX_UNLOCK_PASSWORD WIBOX_API_TOKEN WIBOX_REMOTE WIBOX_REMOTE_ONLY
export WIBOX_API_PORT WIBOX_REMOTE_ATTEMPTS WIBOX_AWAKE_POLL WIBOX_VIDEO_WAIT

echo "wibox: cuenta ${WIBOX_ACCOUNT}, dispositivo ${WIBOX_DEVICE_ID}, API en el puerto ${WIBOX_API_PORT}"

exec /usr/bin/wibox-daemon
