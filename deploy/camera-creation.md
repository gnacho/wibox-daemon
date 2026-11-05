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

`video_keyframes` has to be greater than zero. If it is zero the stream is
carrying audio only (the doorbell is awake but has not sent a keyframe yet,
which can take around 90 s of stream), and the config flow will answer
`{"stream_source": "timeout"}`.

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

The daemon publishes counters in `/status`, so a loop can wait for the right
moment and create the entry once:

```sh
while :; do
  keys=$(curl -s -H "Authorization: Bearer $WIBOX_API_TOKEN" \
    http://192.168.1.244:8087/status \
    | python3 -c 'import json,sys; print(json.load(sys.stdin)["video_keyframes"])')
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
