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
WIBOX_OPEN_COOLDOWN="$(value open_cooldown)"
WIBOX_VIDEO_RETRY="$(value video_retry)"
WIBOX_VIDEO_RETRY_MAX="$(value video_retry_max)"
WIBOX_REMOTE_BUDGET="$(value remote_budget)"
WIBOX_VIDEO_START_TIMEOUT="$(value video_start_timeout)"
WIBOX_HA_URL="$(value ha_url)"
WIBOX_HA_TOKEN="$(value ha_token)"
WIBOX_HA_STREAM="$(value ha_stream)"
WIBOX_CLOUD_CHECK="$(value cloud_check)"
WIBOX_VIDEO_MAX="$(value video_max)"
WIBOX_PLAY_RESENDS="$(value play_resends)"

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
export WIBOX_VIDEO_MAX WIBOX_PLAY_RESENDS
export WIBOX_MPEGTS

# go2rtc no reenvia el stderr de sus hijos, asi que el log del proceso de video
# solo se puede ver si el daemon escribe a un fichero propio.
WIBOX_LOG_FILE=/data/video.log
# El add-on puede estar semanas en marcha: si el log pasa de 2 MB se guarda
# una copia y se empieza de cero para no llenar /data.
if [ -f "$WIBOX_LOG_FILE" ] && [ "$(wc -c < "$WIBOX_LOG_FILE" 2>/dev/null || echo 0)" -gt 2097152 ]; then
  mv -f "$WIBOX_LOG_FILE" "${WIBOX_LOG_FILE}.1" 2>/dev/null || true
fi
if ! touch "$WIBOX_LOG_FILE" 2>/dev/null; then
  WIBOX_LOG_FILE=/tmp/video.log
  echo "wibox: /data no es escribible, log de video en ${WIBOX_LOG_FILE}"
fi
export WIBOX_LOG_FILE

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
  wibox: exec:/usr/bin/wibox-daemon -video -video-audio#killsignal=2#killtimeout=15#starttimeout=300
api:
  listen: "127.0.0.1:1985"
rtsp:
  listen: "0.0.0.0:8556"
YAML

# La API HTTP (apertura y estado) va en segundo plano; el proceso principal es
# go2rtc.
/usr/bin/wibox-daemon &
sed -i "s/#starttimeout=[0-9]*/#starttimeout=${WIBOX_VIDEO_START_TIMEOUT:-300}/" "$G2R_CONFIG"
exec /usr/bin/go2rtc -config "$G2R_CONFIG"
