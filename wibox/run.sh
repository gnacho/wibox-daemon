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
# Un solo pipe para go2rtc: video y audio G711A en el mismo MPEG-TS.
WIBOX_MPEGTS=1

export WIBOX_ACCOUNT WIBOX_PASSWORD WIBOX_CLIENT_ID WIBOX_DEVICE_ID
export WIBOX_UNLOCK_PASSWORD WIBOX_API_TOKEN WIBOX_REMOTE WIBOX_REMOTE_ONLY
export WIBOX_API_PORT WIBOX_REMOTE_ATTEMPTS WIBOX_AWAKE_POLL WIBOX_VIDEO_WAIT
export WIBOX_MPEGTS

echo "wibox: cuenta ${WIBOX_ACCOUNT}, dispositivo ${WIBOX_DEVICE_ID}, API en el puerto ${WIBOX_API_PORT}"

# go2rtc arranca el daemon bajo demanda cuando un cliente pide el stream, asi
# que el video solo se pide al timbre cuando alguien mira. starttimeout es largo
# a proposito: el timbre duerme y puede tardar minutos en despertar.
# /data es escribible en el add-on; si el supervisor lo monta de solo lectura
# se usa /tmp para no morir en el arranque.
G2R_CONFIG=/data/go2rtc.yaml
if ! touch "$G2R_CONFIG" 2>/dev/null; then
  G2R_CONFIG=/tmp/go2rtc.yaml
  echo "wibox: /data no es escribible, configuracion de go2rtc en ${G2R_CONFIG}"
fi

cat > "$G2R_CONFIG" <<'YAML'
streams:
  wibox: exec:/usr/bin/wibox-daemon -video -video-audio#killsignal=2#killtimeout=15#starttimeout=600
api:
  listen: "127.0.0.1:1985"
rtsp:
  listen: "0.0.0.0:8556"
YAML

# La API HTTP (apertura y estado) va en segundo plano; el proceso principal es
# go2rtc.
/usr/bin/wibox-daemon &
exec /usr/bin/go2rtc -config "$G2R_CONFIG"
