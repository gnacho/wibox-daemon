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

## Known limits

- The bell sends no PPS and, in every capture so far, no IDR either. The
  picture only becomes decodable when the device decides to send a keyframe.
- It runs on batteries and sleeps within about thirty seconds. Nothing in the
  protocol wakes it except a physical event or the official app.
- The speaker channel only exists on the LAN. `-talk <wav>` works inside the
  building, not over the cloud tunnel.
