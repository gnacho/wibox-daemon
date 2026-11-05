# Creating the camera in Home Assistant

Home Assistant does not accept a generic camera in `configuration.yaml` any
more. The camera is a config entry that has to be created through the API, and
its validation **connects to the stream**. So it can only be created while the
add-on is already delivering video, which means a keyframe has arrived.

## 1. Check that there is a picture

```sh
curl -s -H "Authorization: Bearer $WIBOX_API_TOKEN" \
  http://192.168.1.244:8087/status
```

`video_keyframes` has to be greater than zero. If it is zero the config flow
answers `{"stream_source": "timeout"}`.

The two counters tell different stories. `video_pending` above zero means the
doorbell is already sending media and the only thing missing is a keyframe, so
the stream is alive and worth waiting for. `video_keyframes` above zero means
there is a picture, which is what the flow needs.

You can keep the stream alive from another shell while this runs:

```sh
ffmpeg -v error -rtsp_transport tcp -i rtsp://192.168.1.244:8556/wibox \
  -t 120 -f null -
```

## 2. Create the entry

With a long lived token of Home Assistant:

```sh
HASS=https://homeassistant.local:8123
TOKEN=<long lived token>

FLOW=$(curl -s -X POST -H "Authorization: Bearer $TOKEN" \
  -H "Content-Type: application/json" \
  -d '{"handler":"generic"}' $HASS/api/config/config_entries/flow \
  | jq -r .flow_id)

curl -s -X POST -H "Authorization: Bearer $TOKEN" \
  -H "Content-Type: application/json" \
  -d '{
        "stream_source": "rtsp://192.168.1.244:8556/wibox",
        "still_image_url": "http://192.168.1.244:8087/health",
        "advanced": {
          "framerate": 2.0,
          "verify_ssl": false,
          "rtsp_transport": "tcp"
        }
      }' $HASS/api/config/config_entries/flow/$FLOW
```

A successful answer has `"type": "create_entry"`. A repeat of the first call on
a host that already has the entry returns an abort reason, so the script is
safe to run twice.

## 3. Watch it without polling by hand

`deploy/create-camera-when-ready.sh` does exactly this: it polls `/status`,
waits for `video_keyframes` to move above zero and then creates the entry. It
is idempotent (it leaves an existing wibox camera alone) and prints
`video_pending` in each round so a sleeping doorbell is easy to tell from a
stream that has media but no picture.

```sh
HASS_URL=http://192.168.1.244:8123 \
HASS_TOKEN=... \
WIBOX_API=http://192.168.1.244:8087 \
WIBOX_TOKEN=wiboxhaos2026 \
deploy/create-camera-when-ready.sh
```

The same loop by hand is below.

The daemon publishes counters in `/status`, so a loop can wait for the right
moment and create the entry once:

```sh
while :; do
  status=$(curl -s -H "Authorization: Bearer $WIBOX_API_TOKEN" \
    http://192.168.1.244:8087/status)
  keys=$(printf '%s' "$status" | python3 -c 'import json,sys; print(json.load(sys.stdin)["video_keyframes"])')
  pending=$(printf '%s' "$status" | python3 -c 'import json,sys; print(json.load(sys.stdin)["video_pending"])')
  echo "keyframes=$keys pending=$pending"
  [ "$keys" -gt 0 ] && break
  sleep 60
done
```

## Notes

- The `advanced` section is mandatory and has to be a mapping. Sending it as a
  string answers `{"advanced": "expected a mapping"}` and omitting it answers
  `{"advanced": "required key not provided"}`.
- Do not send `name`: it is not an accepted option.
- The go2rtc integration cannot be used instead: it allows a single instance
  and the add-on already brings its own go2rtc, which is not exposed to the
  integration.
- `still_image_url` points at the health endpoint, which answers JSON and is
  useless as a picture. It is there because the field is required. Set it to a
  real snapshot URL if the daemon ever offers one.
