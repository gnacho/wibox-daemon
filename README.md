# wibox-daemon

Go daemon for Linux x86 that speaks the Fermax Wi-Box protocol from the cloud
generation (model IDS7938, Quvii/ubell SDK) and exposes a simple HTTP API for
Home Assistant or other apps.

It replaces the phone as the gateway: you do not need the app open or the phone
at hand.

*[Read this in Spanish](README.es.md)*

## What it does today

- Login against the manufacturer cloud (`r1-8.qvcloud.net`) over the userauth
  duplex channel.
- Device list of the account.
- Device token (64-character auth-code and 32-byte encryption key).
- Door opening over QUII on TCP against the doorbell on the local network.
- **Door opening remotely, in pure Go**: `query-hlrv2` discovery, UST MQTT
  broker, `p2pconnect`, UDP NAT hole punching probe, RB-UDP tunnel and QUII
  inside the tunnel. No manufacturer SDK (which only ships for arm64) and no
  phone involved.
- Remote H.264 Annex-B video on stdout, so go2rtc can hand it to Home
  Assistant. Optional G.711A audio, either on stderr or muxed together with the
  video into a single MPEG-TS stream (`WIBOX_MPEGTS=1`).
- Doorbell discovery on the local network (`-discover`): scans the QUII port
  in the machine's /24s and runs the setup against every candidate.
- Token-protected HTTP API: `/health`, `/devices`, `/open`.

## What it does NOT do

- It does not serve RTSP and does not transcode: video comes out as raw H.264
  Annex-B and go2rtc does the packaging.
- It is not an NVR and does not keep the door open: every `-video` is one
  session.
- The doorbell runs on batteries and sleeps. It wakes on physical activity or
  when the official app is used, and the window is about 30 seconds. While it
  sleeps, `p2pconnect` returns `aoffline` and the daemon retries until
  `WIBOX_REMOTE_ATTEMPTS` is exhausted.

## Build

```
go build -o wibox-daemon .
```

Tested with Go 1.27 on `linux/amd64`. It also builds for arm64:

```
GOARCH=arm64 go build -o wibox-daemon-arm64 .
```

## Configuration

Everything through environment variables (or a systemd `EnvironmentFile`):

| Variable | Purpose |
|---|---|
| `WIBOX_ACCOUNT` | Doorbell account (the one returned when accepting the invitation) |
| `WIBOX_PASSWORD` | Password of that account |
| `WIBOX_CLIENT_ID` | App client identifier (defaults to `003-2021-87dad215f30f1f7e`) |
| `WIBOX_DEVICE_ID` | Doorbell UDID (when unset, the one passed in `/open` is used) |
| `WIBOX_UNLOCK_PASSWORD` | Opening PIN (no default, set it in the config file) |
| `WIBOX_LAN_ADDR` | `ip:port` of the doorbell on the local network (stream port, 34567 by default) |
| `WIBOX_LAN_PORT` | Port scanned by `-discover` (defaults to `34567`) |
| `WIBOX_API_TOKEN` | HTTP API token. Empty = no authentication |
| `WIBOX_API_PORT` | API port (defaults to `8087`) |
| `WIBOX_CHANNEL` / `WIBOX_STREAM` | QUII channel and stream (defaults to `1` and `2`) |
| `WIBOX_BASE_URL` | Manufacturer cloud |
| `WIBOX_REMOTE` | `0` disables the remote path (enabled by default) |
| `WIBOX_REMOTE_ONLY` | `1` skips the local path even when `WIBOX_LAN_ADDR` is set |
| `WIBOX_REMOTE_ATTEMPTS` | `p2pconnect` attempts (defaults to `20`, 12 s each) |
| `WIBOX_AWAKE_POLL` | Seconds between probes while the doorbell sleeps (defaults to `3`) |
| `WIBOX_VIDEO_WAIT` | Seconds without frames before cutting the video (defaults to `20`) |
| `WIBOX_MPEGTS` | `1` muxes video and G.711A audio into MPEG-TS, so one `exec:` pipe carries both |
| `WIBOX_AUDIO_FILE` | Writes the raw G.711A audio to that file instead of stderr |
| `WIBOX_PLAY_RESENDS` | Times the play is resent while waiting for a keyframe (defaults to `3`, `0` disables it) |
| `WIBOX_CONTROL_SOCK` | Unix socket where the video process takes door-opening requests (default `/tmp/wibox-video.sock`) |
| `WIBOX_LOG_FILE` | Also write the log to this file. Needed inside the add-on: go2rtc swallows its child stderr |
| `WIBOX_CLOUD_CHECK` | Seconds between cloud login checks reported in `/status` (default `600`, `0` disables it) |
| `WIBOX_VIDEO_MAX` | Hard cap on one video session, in seconds (default `900`, `0` disables it) |
| `WIBOX_VIDEO_LOCK` | Lock file that keeps a second video process off the doorbell (default `/tmp/wibox-video.lock`) |
| `WIBOX_OPEN_COOLDOWN` | Seconds to ignore a second opening after a successful one (default `3`, `0` disables it) |
| `WIBOX_VIDEO_RETRY` | Base wait, in seconds, between video reconnects before the backoff doubles (default `5`) |
| `WIBOX_DEBUG` | Any value enables HTTP and MQTT traces |

A configuration file can also be passed with `-config <file>`. That is how
go2rtc launches it, since a child process does not inherit the systemd
environment:

## Discovering the doorbell on the local network

If you do not know the doorbell IP, the daemon looks for it:

```
./wibox-daemon -discover
```

It scans the QUII port (34567 by default) in the /24s of the machine
interfaces, runs the setup against every candidate and, if it finds one, prints
the `WIBOX_LAN_ADDR=...` line ready to paste into the configuration. It does not
open the door or request video: only the handshake.

Note: the doorbell only answers when the daemon runs on the same network as the
doorbell. From another network the scan finds nothing, and that is not a daemon
failure.

## Talking to the visitor (LAN only)

The doorbell has a speaker, but only on its local network: the cloud tunnel
carries video and the audio from the bell, never audio towards it.

```sh
ffmpeg -i mensaje.wav -ar 8000 -ac 1 -f wav -c:a pcm_s16le /tmp/mensaje8k.wav
WIBOX_LAN_ADDR=192.168.1.60:34567 ./wibox-daemon -talk /tmp/mensaje8k.wav
```

The file has to be PCM16, mono, 8 kHz. The daemon opens a talk session on the
same TCP port used for video, sends the audio in 40 ms frames and closes.

## Checking cloud access

```
./wibox-daemon -check
```

It logs in, lists the devices and requests the token. It is the quick way to
check whether the credentials are still valid.

`./wibox-daemon -version` prints the build version and the platform it was
compiled for, which is the fastest way to know what a deployment is running.

## API

```
GET  /health                      no token
GET  /devices                     Authorization: Bearer <token>
GET  /open?device=<udid>          Authorization: Bearer <token>
GET  /open?device=<udid>&wait=0   Authorization: Bearer <token>
GET  /status                      Authorization: Bearer <token>
```

Add `wait=0` to `/open` and the call returns at once with a 503 when the last
cloud check saw the doorbell asleep, instead of waiting up to four minutes for
it to wake. Useful for automations that cannot block.

`/status` reports the version, uptime, whether a video session is alive and the
last opening attempt (route, code, error, time). It also carries
`video_frames`, `video_keyframes` and `video_pending`, always present and at
zero when nothing is streaming: the last one counts frames already received
that cannot be used until a keyframe arrives, which is how you tell a stream
that is sending media without a picture from one that is silent. `device_online` says whether the doorbell was listed as online the last time the daemon asked the cloud, and `devices_checked` is when that was.

Examples:

```
curl http://192.168.1.50:8087/health
curl -H "Authorization: Bearer $WIBOX_API_TOKEN" http://192.168.1.50:8087/devices
curl -H "Authorization: Bearer $WIBOX_API_TOKEN" \
     "http://192.168.1.50:8087/open?device=23123csfewyk"
```

The doorbell only accepts one QUII session at a time. When go2rtc is already
streaming, the video process owns it, so the API asks that process to open the
door through a local unix socket instead of starting a second tunnel (which
would only get a BUSY answer). If no stream is running, `/open` builds its own
tunnel as usual.

## Video in Home Assistant

The daemon does not implement RTSP. It writes raw H.264 Annex-B to stdout and
lets [go2rtc](https://github.com/AlexxIT/go2rtc) do the packaging, which is
already the streaming engine inside Home Assistant (2024.11 and later).

`exec:` with the `pipe` transport reads exactly that: a raw H.264/H.265
bitstream on stdout.

```yaml
# deploy/go2rtc.yaml
streams:
  wibox: exec:/usr/local/bin/wibox-daemon -config /etc/wibox-daemon.conf -video#killsignal=2#killtimeout=5

api:
  listen: "127.0.0.1:1984"
rtsp:
  listen: "127.0.0.1:8554"
```

Then add it to Home Assistant as a generic camera pointing at
`rtsp://127.0.0.1:8554/wibox`, or as a go2rtc stream for WebRTC. Two caveats:

- Every `-video` run is one session against the doorbell. With the doorbell
  asleep, the process spends its time in the `p2pconnect` retry loop, so it is
  better to open the stream on demand (a dashboard tap) than to leave it
  always-on.
- Audio is G.711A (PCMA). With `WIBOX_MPEGTS=1` the daemon writes MPEG-TS with
  both tracks on stdout (H.264 with stream type `0x1B`, A-law with `0x90`), which
  is what the add-on uses: go2rtc maps `0x90` to its PCMA codec. Without the flag
  `-video-audio` writes the audio to stderr and a second `ffmpeg` source would be
  needed, because go2rtc treats two `exec:` sources as failover, not as a merge.
  Note that `ffprobe` does not recognise A-law inside MPEG-TS (ffmpeg itself
  writes it as stream type `0x06`), so it is not a valid gate for that track.

## Camera in Home Assistant

Home Assistant no longer takes a generic camera in YAML and the config flow
validates by opening the stream, so the entry can only be created once the
stream carries a keyframe. `deploy/camera-creation.md` has the exact calls,
including the mandatory `advanced` block.
`deploy/create-camera-when-ready.sh` does the waiting for you: it polls
`/status`, prints `video_pending` each round so a sleeping doorbell is easy to
tell from a stream without a picture, and creates the entry the first time
`video_keyframes` moves above zero. It is idempotent.

## Home Assistant add-on

Home Assistant OS cannot run a bare binary, so the daemon also ships as an
add-on. It runs inside HAOS, talks to the vendor cloud and exposes the HTTP API
on port 8087, which means the door can be opened from Home Assistant without the
phone and without being on the building network.

```
Settings -> Add-ons -> Add-on store -> Repositories
   add https://github.com/gnacho/wibox-daemon
install "Wibox Doorbell"
```

Options, all of them adjustable from the add-on page: account, password,
client id, device id, opening pin and API token; `attempts`, `awake_poll`,
`video_wait`, `video_max`, `play_resends`, `cloud_check`, `open_cooldown` and
`video_retry` for the timings. The add-on forces the remote chain, so no LAN
address is configured.

From Home Assistant:

```yaml
rest_command:
  wibox_abrir:
    url: "http://homeassistant.local:8087/open?device=<device_id>"
    method: GET
    headers:
      Authorization: "Bearer <api_token>"
    timeout: 300
  wibox_abrir_ya:
    url: "http://homeassistant.local:8087/open?device=<device_id>&wait=0"
    method: GET
    headers:
      Authorization: "Bearer <api_token>"
    timeout: 30
```

The first command waits: if the doorbell is asleep the daemon keeps retrying
until it gets a peer and only then opens, so the timeout is generous and
`attempts` bounds that wait. The second one answers straight away with 503 when
the last cloud check saw the bell offline, which is what an automation wants.
`open_cooldown` keeps a double press from opening twice.

### Video through the add-on

The add-on ships its own [go2rtc](https://github.com/AlexxIT/go2rtc), so the
camera works without installing anything else. go2rtc is the main process and
the daemon is its `exec:` source, which means the video session is only started
when someone watches the camera and is killed afterwards.

```
rtsp://<haos-ip>:8556/wibox
```

Add it in Home Assistant as a generic camera (RTSP) or through the go2rtc
integration. `starttimeout` is set to 600 s because the doorbell sleeps and may
take minutes to answer: open the camera on demand rather than leaving it
always-on, and expect the first image to be slow when nobody has rung.

## Verifying the door opening in the building

`docs/building-verification.md` is the script for the visit: discover the
doorbell, check the cloud, open the door and read the errors. The real opening
can only be tested on the same network as the doorbell.

## Troubleshooting

Everything the daemon knows is in `/status`. These are the usual readings:

| What you see | What it means | What to do |
|---|---|---|
| `device_online: false` | The cloud has not seen the doorbell since `devices_checked`. It runs on batteries. | Wait for movement at the portal, or open with `?wait=0` and handle the immediate 503. |
| `cloud_ok: false` with `cloud_error` | Login to the manufacturer cloud failed. | Check `WIBOX_ACCOUNT`/`WIBOX_PASSWORD` and that the gateway answers. |
| `video_pending` growing, `video_keyframes: 0` | Media is arriving, the picture is waiting for a keyframe. The stream is alive. | Nothing to fix: the doorbell decides when to send an IDR. |
| `video_lock_free: false` | Another video process on this machine holds the lock (`/tmp/wibox-video.lock`), so the local session is its. | Nothing to fix: the lock is doing its job. |
| `video_session: true`, counters at zero | A player is connected but the bell has not sent anything yet. | Keep watching; it will fill up when the bell wakes. |
| `last_open.rc: 0` | The door opened. | Nothing. |
| `last_open.rc: -10029` | The opening PIN was rejected. | Fix `WIBOX_UNLOCK_PASSWORD`. |
| `last_open.rc: -1` with a p2pconnect timeout | The bell went back to sleep before the tunnel was ready. | Try again in a waking window. |

## Deployment

See `deploy/wibox-daemon.service` (systemd) and `deploy/haos-wibox.yaml` (Home
Assistant). The daemon does not need root: it listens on a high port and only
talks outwards.

## Protocol

The full wire protocol is in [`docs/protocol.md`](docs/protocol.md). Summary of
what this daemon implements:

1. **userauth duplex**: `GET /auth/nologin;jus_duplex=down` only to get the
   `jsessionid` cookie, `POST /auth/nologin;jus_duplex=up` with the XML
   envelope, and then `GET .../down` to collect the queued response. The order
   matters: if the long-poll is in flight when the upload arrives, the gateway
   answers
   `CAUTHNETClient::Response(packet): BeginResponseGenerator return failed`.
2. **XStream envelope**: `content` before `header`, kebab-case fields, empty
   `version`, 3-space indentation.
3. **QUII over TCP**: 32-byte setup with plaintext `0xA9`, PLAY with user
   `adminapp2`, and transparent commands with type `0xFE`. AES-256-CBC with a
   zero IV and SHA-256 of (plaintext header + body) appended to the body.
4. **Opening**: transparent command 4, body with lock number, channel, and the
   hexadecimal SHA-256 of the pin from byte 16.

## Verification

```
go test ./...
```

22 tests: byte-for-byte XStream envelope against the official client format,
parsing of real responses captured from the gateway, AES-256-CBC vector computed
separately with openssl, QUII header layout, SHA-256 tail of the body, opening
body (command 4, pin at byte 16), full opening over a loopback socket that acts
as the doorbell, password rejection, setup with a bad status, LAN discovery, and
the `/open` HTTP contract (token required, 200 with `ok`, 502 with the error
code).

## Related projects

- [wibox-android](https://github.com/gnacho/wibox-android): Android app (Kotlin)
  for the same ecosystem. The protocol notes live in its `docs/protocol.md`.

## License

GNU Affero General Public License v3.0. See [LICENSE](LICENSE).
