# Checking everything on the building network

The daemon works from anywhere through the vendor cloud: login, device list,
token, remote door opening and the video/audio stream are verified on x86
without the phone and without the manufacturer SDK. What still needs the
building network is the **local** path: the doorbell answers QUII on its own
network, and the talk channel only exists there.

Use this runbook when you are in the building, or when the daemon runs on a
machine inside it.

## What you need

- A `wibox-daemon-linux-amd64` (or `arm64`) binary, or a container image,
  from the current release. Check `./wibox-daemon -version`.
- The account, password, device id and opening PIN in the config file
  (`deploy/wibox-daemon.conf.example` shows the variables).
- The phone app closed. The doorbell accepts a single QUII session, so an open
  stream from the app makes everything else answer BUSY.

## Step 0: release the channel

Close the live view in the phone app and wait a few seconds. If something is
still holding the session, the bell answers `setup status=3` or a plain BUSY.

## Step 1: find the doorbell

```
./wibox-daemon -discover
```

Expected output:

```
timbre encontrado en 192.168.1.60:34567
WIBOX_LAN_ADDR=192.168.1.60:34567
```

The scan covers one /24 per local interface, so a bigger subnet needs the
address typed by hand (`WIBOX_LAN_ADDR`, `WIBOX_LAN_PORT`).

## Step 2: check the cloud

```
./wibox-daemon -check
```

This logs in, lists the devices and asks for a token. If it fails, nothing
else will work: the token is what feeds the QUII password.

## Step 3: open the door on the LAN

```
WIBOX_LAN_ADDR=192.168.1.60:34567 ./wibox-daemon -remote
```

With `WIBOX_LAN_ADDR` set and `WIBOX_REMOTE_ONLY=0`, the daemon tries the LAN
first and only falls back to the cloud tunnel. A successful opening prints
`rc=0`; the HTTP API answers `{"ok":true,"rc":0}` on `/open`.

## Step 4: the stream

```
./wibox-daemon -video -video-audio > stream.ts
ffprobe stream.ts
```

With `WIBOX_MPEGTS=1` (the add-on sets it) the output is MPEG-TS with an H.264
track and a G.711A track. Audio starts as soon as the bell sends it; the
picture waits for a keyframe, which the bell schedules on its own (roughly
every 90 s of stream, and not in every session). `ffprobe` reports the audio
track as `unknown`: it does not recognise A-law inside MPEG-TS.

## Step 5: talk to the visitor

```
ffmpeg -i mensaje.wav -ar 8000 -ac 1 -f wav -c:a pcm_s16le /tmp/mensaje8k.wav
WIBOX_LAN_ADDR=192.168.1.60:34567 ./wibox-daemon -talk /tmp/mensaje8k.wav
```

Only over the LAN: the cloud tunnel has no speaker channel.

## Error table

| Symptom | Cause |
|---|---|
| `rc=-10029` | Opening PIN rejected |
| `setup status=3` | Bell busy, another session open |
| `talk: setup rechazado` | Another talk or live session running |
| `data-encode-key de N bytes` | The token is not the one expected, check `-check` |
| `timeout esperando p2pconnect` | Bell asleep (it runs on batteries) or offline |
| No frames after the play | The bell did not send a keyframe yet |

## What to bring back

- The output of `-discover`, with the address it found.
- The `rc` of the opening and the log lines around it.
- Whether the stream carried audio, video, both or nothing.
- Whether `-talk` returned without error.
