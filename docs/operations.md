# Operating the daemon

This is the day-to-day page: how to tell whether the daemon is healthy, how to
ship a new version, and what to do when something looks broken. The protocol
itself is in [protocol.md](protocol.md) and the field runbook for the building
network is in [building-verification.md](building-verification.md).

## Health in one command

With the add-on installed, everything interesting is in `/status`:

```
curl -s -H "Authorization: Bearer $WIBOX_API_TOKEN" http://<host>:8087/status
```

What to look at:

| Field | Healthy value | What it means when it is not |
|---|---|---|
| `version` | the tag you shipped | The add-on still runs an older image. |
| `cloud_ok` / `cloud_error` | `true` / empty | The daemon cannot log in to the vendor cloud. Check `account` and `password`. |
| `device_online` | `true` when somebody is at the door | The bell sleeps on batteries. A door opening has little chance right now. |
| `devices_checked` | recent timestamp | How old that observation is. |
| `video_session` | `true` while a viewer is connected | `false` with `video_pending` at zero means the bell is not sending anything. |
| `video_pending` | zero | Above zero means media is arriving and the picture is waiting for a keyframe. The stream is alive. |
| `video_lock_free` | `true` | `false` means another process on this host holds the lock; `video_lock_pid` says which one. |
| `last_open` | the last attempt, with `rc` | `0` opened, `-10029` wrong PIN, `-1` with a `p2pconnect` timeout means the bell was not reachable. |

`/health` answers `{"ok":true}` without a token. That is what the watchdog
polls.

## Looking inside the add-on

```
docker ps --filter name=app_<slug>
docker exec app_<slug> ps -o pid,etime,args
docker exec app_<slug> tail -30 /data/video.log
docker exec app_<slug> cat /data/go2rtc.yaml
```

Two processes are normal: the API and the `-video -video-audio` child that
go2rtc spawns when somebody watches the stream. The child owns the single QUII
session while it runs, which is why `/open` asks it over the control socket
instead of opening a second tunnel.

The video log is written by the daemon itself because go2rtc keeps the stderr
of its children. It rotates at two megabytes and keeps one previous copy.

## Shipping a version

1. Bump `version` in `wibox/config.yaml` and `ARG WIBOX_VERSION` in
   `wibox/Dockerfile`. The add-on builds the source tarball of that tag, so a
   code change that is not tagged never reaches Home Assistant.
2. `make check && make race && make cover`.
3. Commit, `git tag -a vX.Y.Z -m "..."`, push the branch and the tag.
4. `make release` and `gh release create vX.Y.Z dist/* --notes-file notes.md`.
5. In Home Assistant, reload the store and update the add-on:

```
T=$(jq -r .access_token /mnt/data/supervisor/cli.json)
curl -s -X POST -H "Authorization: Bearer $T" http://172.30.32.2/store/reload
# wait ~25 s
ha store apps update <slug> --no-progress
```

6. Confirm `version` in `/status`. The supervisor sends SIGTERM on update and
   the daemon drains its requests for up to fifteen seconds before exiting.

## When a door opening fails

`/open` waits up to `WIBOX_REMOTE_BUDGET` seconds looking for the bell. If the
caller cannot afford that, add `&wait=0`: the daemon answers `503` at once when
the last cloud check saw the bell offline. A second opening within
`WIBOX_OPEN_COOLDOWN` seconds receives `429` with `retry_after_s`.

## Watching right after opening

The bell accepts one session at a time, and an opening keeps it busy for a
while. A video process started a second later therefore waits at `p2pconnect`
and never gets media, which looks like a broken camera.

The daemon writes the time of every successful opening to
`<video.lock>.lastopen` and the video process waits out the rest of the window
before dialling. `WIBOX_OPEN_SETTLE` (add-on option `open_settle`) sets the
window: `75` seconds by default, `0` restores the old behaviour. Failed openings
are not recorded, so a `503` does not delay the next view.

## Known limits

- The bell sends no PPS and, in every capture so far, no IDR either. The
  picture only becomes decodable when the device decides to send a keyframe.
- It runs on batteries and sleeps within about thirty seconds. Nothing in the
  protocol wakes it except a physical event or the official app.
- The speaker channel only exists on the LAN. `-talk <wav>` works inside the
  building, not over the cloud tunnel.

## The camera entry creates itself

Home Assistant's generic camera integration validates the stream by opening it,
so before the first keyframe the flow only answers `{"stream_source": "timeout"}`.
The daemon knows when the picture arrives (the `video_keyframes` counter comes
from the live video process), so it can create the entry at the right moment.

Set three variables, or the matching add-on options, to turn it on:

| Variable | Meaning |
|---|---|
| `WIBOX_HA_URL` | Base URL of Home Assistant, for example `http://192.168.1.244:8123` |
| `WIBOX_HA_TOKEN` | Long lived access token with permission to create config entries |
| `WIBOX_HA_STREAM` | RTSP URL the entry should point at, for example `rtsp://192.168.1.244:8556/wibox` |

With any of them empty the feature is off and nothing changes. When all three
are set, the daemon checks `/status` once a minute and creates the entry if it
does not exist yet. The still image points at the daemon's own `/snapshot.jpg`,
so the card has something to show even when the doorbell sleeps.

There are two ways to create the entry, and the daemon tries both:

1. **With `stream_source`.** It works once the stream delivers media, because
   the flow validates by opening the RTSP URL.
2. **Without `stream_source`.** The flow cannot validate, so instead of failing
   it asks for confirmation (`step_id: user_confirm`, field `confirmed_ok`).
   Answering `{"confirmed_ok": true}` creates the entry anyway, backed by the
   still image. This is what works while the doorbell sends no keyframe.

The `advanced` block is mandatory in both cases, even without a stream.

## Live view takes a while to start

Measured against the add-on with the doorbell asleep: `ffprobe` of
`rtsp://<host>:8556/wibox` prints the tracks (`h264`, `pcm_alaw`) but only after
**about 32 seconds**, and it returns nothing at all within 8 seconds.

The daemon is not the cause. Run inside the container, the producer writes its
first packet within two seconds even with no tunnel open:

```
timeout 8 /usr/bin/wibox-daemon -video -video-audio > /tmp/h.ts
bytes a 2s: 564   # PAT, PMT and the SPS/PPS
```

What happens is that go2rtc does not publish the stream until its MPEG-TS
demuxer has real media from the source, and the source only gets media once the
tunnel with the doorbell is up (which needs a waking window). Until then
`/api/streams` shows the producer running and no consumers, and the RTSP
`DESCRIBE` waits.

That is also why Home Assistant's camera flow used to time out: it validates by
opening the stream and gives up in seconds. See "The camera entry creates
itself" for how the entry is created anyway.

## Frames before the first keyframe

By default every video frame before the first keyframe is discarded, because a
decoder cannot use slices without a reference. The doorbell can go a whole
session without sending one, and then a consumer receives audio and nothing
else, which is what made Home Assistant's stream validation fail.

`WIBOX_VIDEO_ALL_FRAMES=1`, or the `video_all_frames` add-on option, forwards
every frame instead. Measured with the bell awake for a minute:

```
1125 video frames, 1785248 bytes, first frame 352x288 at 20 fps
zero keyframes, NAL census map[1:1055 7:45]
```

So the stream is well formed and a validator accepts it, but the picture is
still not viewable: ffmpeg answers `top block unavailable for requested intra
mode` and writes nothing, and Home Assistant's decoders would do no better. The
option is off by default for that reason: it fills the pipe without producing a
picture.
