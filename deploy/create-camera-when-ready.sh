#!/bin/bash
# Creates the Home Assistant camera entry the first time the doorbell sends a
# decodable picture. The generic camera integration validates the stream by
# opening it, so creating the entry earlier only returns
# {"stream_source": "timeout"} while the bell sleeps or sends slices with no
# keyframe.
#
# Requirements: curl and jq. Run it from a machine that reaches both Home
# Assistant and the daemon (the HAOS host itself works).
#
#   HASS_URL=http://192.168.1.244:8123 HASS_TOKEN=... \
#   WIBOX_API=http://192.168.1.244:8087 WIBOX_TOKEN=... \
#   ./create-camera-when-ready.sh
#
# Optional: WIBOX_STREAM (default rtsp://<host>:8556/wibox), POLL (60 s),
# MAX_ROUNDS (0 = forever).

set -u

: "${HASS_URL:?falta HASS_URL}"
: "${HASS_TOKEN:?falta HASS_TOKEN}"
: "${WIBOX_API:?falta WIBOX_API}"
: "${WIBOX_TOKEN:?falta WIBOX_TOKEN}"

host=$(printf '%s' "$WIBOX_API" | sed -e 's#^https\?://##' -e 's#[:/].*$##')
STREAM=${WIBOX_STREAM:-rtsp://$host:8556/wibox}
STILL=$WIBOX_API/health
POLL=${POLL:-60}
MAX_ROUNDS=${MAX_ROUNDS:-0}
# Sin un consumidor RTSP enganchado, go2rtc no arranca el productor y el
# timbre no manda nada. Con PULL=1 (por defecto, si hay ffmpeg) se mantiene
# una tirada del stream en segundo plano para forzarlo.
PULL=${PULL:-1}

log() {
  printf '%s %s\n' "$(date -Is)" "$*"
}

hass() {
  local method=$1 path=$2 body=${3:-}
  if [ -n "$body" ]; then
    curl -s -m 30 -X "$method" -H "Authorization: Bearer $HASS_TOKEN" \
      -H 'Content-Type: application/json' -d "$body" "$HASS_URL$path"
  else
    curl -s -m 30 -X "$method" -H "Authorization: Bearer $HASS_TOKEN" "$HASS_URL$path"
  fi
}

already() {
  # La integracion no deja leer las opciones de una entrada, asi que se mira
  # si ya existe una camara con "wibox" en el nombre.
  hass GET /api/states | jq -e 'any(.[];
    (.entity_id | startswith("camera.")) and
    ((.attributes.friendly_name // "") | ascii_downcase | test("wibox")))' >/dev/null 2>&1
}

create() {
  flow=$(hass POST /api/config/config_entries/flow '{"handler":"generic"}' | jq -r '.flow_id // empty')
  if [ -z "$flow" ]; then
    log "no se pudo abrir el flujo generic"
    return 1
  fi
  body=$(jq -n --arg s "$STREAM" --arg i "$STILL" '{
    stream_source: $s,
    still_image_url: $i,
    advanced: {framerate: 2.0, verify_ssl: false, rtsp_transport: "tcp"}
  }')
  answer=$(hass POST "/api/config/config_entries/flow/$flow" "$body")
  echo "$answer"
  printf '%s' "$answer" | jq -e '.type == "create_entry"' >/dev/null 2>&1
}

puller=
if [ "$PULL" != 0 ] && command -v ffmpeg >/dev/null 2>&1; then
  (
    while :; do
      # Tiradas cortas: mientras hay consumidor el productor ocupa la unica
      # sesion QUII del timbre, y abrir la puerta importa mas que el video.
      ffmpeg -v error -rtsp_transport tcp -i "$STREAM" -t 30 -f null - >/dev/null 2>&1
      sleep 30
    done
  ) &
  puller=$!
  trap 'kill "$puller" 2>/dev/null' EXIT
  log "tirando del stream para forzar al productor (pid $puller)"
fi

round=0
while :; do
  round=$((round + 1))
  status=$(curl -s -m 20 -H "Authorization: Bearer $WIBOX_TOKEN" "$WIBOX_API/status")
  keys=$(printf '%s' "$status" | jq -r '.video_keyframes // 0' 2>/dev/null || echo 0)
  pending=$(printf '%s' "$status" | jq -r '.video_pending // 0' 2>/dev/null || echo 0)
  log "ronda $round: keyframes=$keys pendientes=$pending"

  if [ "${keys:-0}" -gt 0 ] 2>/dev/null; then
    if already; then
      log "la camara ya estaba creada"
      exit 0
    fi
    if create; then
      log "camara creada"
      exit 0
    fi
    log "el alta fallo, se reintenta en la proxima ronda"
  fi

  if [ "$MAX_ROUNDS" -gt 0 ] && [ "$round" -ge "$MAX_ROUNDS" ]; then
    log "sin imagen en $round rondas"
    exit 1
  fi
  sleep "$POLL"
done
