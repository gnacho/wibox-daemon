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
- Doorbell discovery on the local network (`-discover`): scans the QUII port
  in the machine's /24s and runs the setup against every candidate.
- Token-protected HTTP API: `/health`, `/devices`, `/open`.

## What it does NOT do

- **It does not open the door remotely.** The manufacturer's remote path goes
  through a P2P tunnel implemented by its proprietary SDK, which only ships
  compiled for arm64 (`libqv-p2p-v2.so`). Without that binary there is no
  tunnel. The daemon therefore works when it is on the same network as the
  doorbell.
- It does not send video or audio. It only opens the door and queries state.

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
| `WIBOX_UNLOCK_PASSWORD` | Opening PIN (defaults to `0925`) |
| `WIBOX_LAN_ADDR` | `ip:port` of the doorbell on the local network (stream port, 34567 by default) |
| `WIBOX_LAN_PORT` | Port scanned by `-discover` (defaults to `34567`) |
| `WIBOX_API_TOKEN` | HTTP API token. Empty = no authentication |
| `WIBOX_API_PORT` | API port (defaults to `8087`) |
| `WIBOX_CHANNEL` / `WIBOX_STREAM` | QUII channel and stream (defaults to `1` and `1`) |
| `WIBOX_BASE_URL` | Manufacturer cloud |
| `WIBOX_DEBUG` | Any value enables HTTP traces |

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

## Checking cloud access

```
./wibox-daemon -check
```

It logs in, lists the devices and requests the token. It is the quick way to
check whether the credentials are still valid.

## API

```
GET  /health                      no token
GET  /devices                     Authorization: Bearer <token>
GET  /open?device=<udid>          Authorization: Bearer <token>
```

Examples:

```
curl http://192.168.1.50:8087/health
curl -H "Authorization: Bearer $WIBOX_API_TOKEN" http://192.168.1.50:8087/devices
curl -H "Authorization: Bearer $WIBOX_API_TOKEN" \
     "http://192.168.1.50:8087/open?device=23123csfewyk"
```

## Verifying the door opening in the building

`docs/building-verification.md` is the script for the visit: discover the
doorbell, check the cloud, open the door and read the errors. The real opening
can only be tested on the same network as the doorbell.

## Deployment

See `deploy/wibox-daemon.service` (systemd) and `deploy/haos-wibox.yaml` (Home
Assistant). The daemon does not need root: it listens on a high port and only
talks outwards.

## Protocol

The details are in `docs/protocol.md` in the Android app repo
(`gnacho/wibox-android`). Summary of what this daemon implements:

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
