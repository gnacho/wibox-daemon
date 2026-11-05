# Wire protocol

What this daemon speaks, written down so the next person does not have to
reverse-engineer it again. Everything here was checked against the code in this
repository and, where noted, against the doorbell itself.

The vendor ships three layers stacked on top of each other:

1. **userauth**: XML envelopes over a duplex HTTP channel to the cloud. Gives a
   session, the device list and a per-device token.
2. **UST**: an MQTT broker that tells the doorbell where it can be reached
   (its public and private endpoints), plus a UDP probe to open the NAT.
3. **RB-UDP**: a custom reliable-ish transport over UDP that carries QUII,
   the voice/video protocol the doorbell already speaks on the LAN.

No vendor binary is involved. The `.so` that ships with the Android app only
does the P2P tunnel, and it exists for arm64 only.

## 1. userauth

Base URL `https://r1-8.qvcloud.net`. The certificate is self-signed and has no
CN, so clients skip verification (the official app accepts everything too).

### Duplex channel

```
GET  /auth/nologin;jus_duplex=down    -> Set-Cookie: jsessionid=...
POST /auth/nologin;jus_duplex=up      -> command (no session yet)
POST /auth/user;jus_duplex=up         -> command (with session)
GET  /auth/nologin;jus_duplex=down    -> response, queued
```

Order matters and it is counter-intuitive. If a long poll is in flight when the
upload arrives, the gateway answers

```
CAUTHNETProfile::Response(packet): BeginResponseGenerator return failed
```

instead of the envelope. Upload first, then poll: the gateway queues the reply
and serves it on the next `down` (it sends `Cache-Control: max-age=120`).

The `jsessionid` cookie is mandatory. Without it the gateway answers 404.

### Envelope

XStream XML, `content` before `header`, kebab-case fields, empty `version`, three
spaces of indentation, empty tags expanded:

```xml
<?xml version="1.0" encoding="UTF-8"?><envelope>
   <content class="com.quvii.qvweb.userauth.bean.request.LoginReqContent">
      <account>...</account>
      <auth-code></auth-code>
      <ip-region-id>1</ip-region-id>
      <password>...</password>
      <auth-type>0</auth-type>
   </content>
   <header>
      <client>
         <app>2021</app>
         <id>...</id>
         <oem>G0021</oem>
         <type>3</type>
      </client>
      <command>login</command>
      <flag>tdkcloud</flag>
      <seq>1</seq>
      <user-data></user-data>
      <version></version>
   </header>
</envelope>
```

`<session>` is added after login. Escaping is standard XML for `& < > " '`.

### Commands implemented

| command | content class | notes |
|---|---|---|
| `login` | `...LoginReqContent` | returns `account-id` and a session id nested as `<session><id>` |
| `get-device-list` | `...DevListReqContent` | `<device>` entries, possibly wrapped in `<devices>` |
| `get-device-token` | `...DevDynamicPwdGetReqContent` | `auth-code`, `data-encode-key`, `dynamic-password`, `password-expired` |

`result` in the header is `0` on success. Known errors: `100009` bad protocol
version, `100004` malformed XML, `100100002` unknown account, `100100003` wrong
password, `100151001` device not bound to the account.

## 2. UST discovery

```
POST {base}/mst/query
```

One-line envelope, `command` `query-hlrv2`, asking for `p2papp,natcheck,appinfo`.
The reply lists `<server>` entries with `server-type`, `url`, `uri` and an
optional `param`. The MQTT endpoint is `url + uri`, plus `/?{param}` when there
is a param.

`param` carries the broker credentials, encrypted with a key derived from our
client id (`srcid`) and three 4096-byte tables that ship in `assets/`. Derivation
is AES-256 key expansion with a custom S-box generated from the tables; see
`ust_crypto.go` and its tests, which pin the vectors.

Decoding the param: replace `&amp;` with `&`, split on `&`, split each pair on
the first `=`, then percent-decode **by hand**. Do not use a query unescaper:
it turns `+` into a space and corrupts the base64.

## 3. MQTT

`mqtts://mqttsr1.qvcloud.net:1884`, MQTT 3.1.1, clean session.

- Client id: `app_{clientId}_{userdata}_`, where `userdata` is a random
  11-digit number. The same `userdata` goes in every header.
- Will: `unregister` on `app/ust/json/{clientId}`.
- Subscribe to `app/ust/json/{clientId}` and `{clientId}/ust/json`. Never to
  `#`: the broker kicks the client (EOF 32109).
- Header of every message: `flag tdkcloud`, `version v3.2.0`,
  `command`, `client{id,type,oem,app}`, `userdata`, plus `seq` and `session`
  once logged in.

Sequence:

```
unregister -> 100 ms -> register -> wait register (10 s)
-> sub-device-state {devid:[device]} -> device-awaken {devid}
-> p2pconnect (loop, 12 s each) -> update-netinfo
```

`p2pconnect` asks for the device endpoints:

```json
{"devid":"...","session-flag":"...","requ-session-id":0,
 "force-trans":0,"kcpParam":{"mode":"normal"},
 "devTrans":{"monChn":-1},"devType":"normal","devSubState":"awakened"}
```

The reply carries `nettype`, `pub-ip`, `pub-udpport`, `loc-ip`,
`loc-udpport`, `utd-pub-ip`, `utd-pub-udpport` and `resp-session-id`. The server
truncates `session-flag` to 63 characters, so it must be matched by prefix in
both directions.

`aoffline` in `sub-device-state` is **not** trustworthy: the server reports the
bell asleep in the same minute another client gets candidates. Keep sending
`p2pconnect`.

## 4. NAT check

Same UDP socket that later carries the tunnel, so the mapping does not change.
112-byte packet to the `natcheck` server from discovery:

| offset | type | value |
|---|---|---|
| 0x00 | u32 | marker `0xFFABEFC1` |
| 0x1A | u16 | 0x70 (total length) |
| 0x1C | u32 | `0xFFFFFFFF` |
| 0x20 | u32 | 0x54 |
| 0x2D | u8 | 1, "give me my mapping"; with 0 the server only echoes |
| 0x30 | u16 | 1 |
| 0x32 | u8 | 0x11 |
| 0x40 | u16 | 0x2C |
| 0x44 | 4 B | constant tag the server echoes |

The reply repeats the packet and appends our public address: IP as a C string
at 0x4C, external port as u16 LE at 0x5C. Publish it with `update-netinfo` or
the bell answers to the wrong place.

## 5. Peer probe (0xA4)

164-byte UDP frame, little endian, no checksum. Marker at 0x00, type `0xA4` at
0x1A, `0xFFFFFFFF` at 0x1C, command `0x00120002` at 0x30, probe sequence at
0x38 (0 LAN, 1 P2P, 2 transit), session flag as a C string at 0x44, local UDP
port twice (0x86 and 0x8A), target IP at 0x8C, target port at 0x9C, test mode
at 0xA0.

Send it to each candidate in order LAN, P2P, transit, up to three times with
1.5 s between tries. A matching reply with flag 1 is a valid answer. A frame
with flag 0 is the bell probing **us**: acknowledge it and send the two active
packets to the sender, otherwise the NAT hole never opens. A private peer IP
ranks 3, loopback 2, anything else 1.

## 6. RB-UDP

Frames start with marker `0xFFABEFC1`. Control frames are 28 bytes, data
frames carry a 0x38-byte header. Status words live at 0x14; the low 16 bits
identify the kind (`0x0900`, `0x0D00`, `0x0500`, `0x1900`).

Header fields used: `word4` at 0x04, `word8` at 0x08, `local id` at 0x0C,
`remote id` at 0x10, status at 0x14, length at 0x1A. Wrapped frames mark
`0xFFFFFFFF` at 0x1C, carry an inner length at 0x20, an 8-byte tag at 0x24 and
the inner frame from 0x2C. The checksum is a 16-bit one's complement sum over
the first 0x1C bytes.

State machine, in the order it happens:

```
BOOTSTRAP (0xFFFF4100)
  <- SYN_ACK (0xFFFF4900)          lane takes remote id, we send ESTABLISHED
  -> ESTABLISHED (0xFFFF0900) + wrapped CONNECT
  <- PROGRESS (0xFFB40900)
  <- POST_CONNECT (0xFFA80900)     peer logic id at word4, ack +0x38
  <- CONNECT reply                 dest id for QUII, then QUII setup
  <- POST_SETUP (0xFED80900)       ack +0x30
  <- play ack                      status16 0x0900, remote id = lane id after play
  -> QUII play
  <- STREAM_READY (0xFF480900)     ack +0x3C, play sync
  <- PLAY_SYNC (0xFF780900)        five stages, each with its own ack
  media: status16 0x0500 or 0x1900, payload from 0x1C
```

The play acknowledgement has no constant of its own: it is recognised because
its status word ends in `0x0900`, the play was sent, it was not acknowledged
yet, and its remote id equals our lane id at the moment the play went out. Do
not answer it with offset acks; let the next ESTABLISHED carry the lane
forward, otherwise the ids go stale and the bell loops with `PLAY_LATE`.

Data framing: the payload may contain several KCP frames, each with the marker
at its start and its own length at +4. Only those frames are forwarded to the
QUII parser; a hole triggers a resync instead of freezing the stream.

## 7. QUII inside the tunnel

QUII is the same protocol the app speaks on the LAN. Packets are a 32-byte
header plus a body, AES-256-CBC with an IV of 16 zeros and no padding. The
SHA-256 of the clear header plus the body is appended to the body.

Setup is 32 clear bytes with `0xA9` first. The reply must have `0xA9` at 0, `0`
at 9, `2` at 10 and `1` at 11. Play uses user `adminapp` and path
`/mode=real&idc={channel}&ids={stream}&ap=2`, with the password from the token
(`dynamic-password`, 108 chars, not the auth code). The LAN session and the CGI
use `adminapp2` instead; only the play inside the tunnel uses `adminapp`.
Keepalives are type `0x00` with an empty body every 10 s.

The unlock is transparent order 4: byte 0 lock number, byte 1 zero, byte 2
channel, byte 3 `1`, then from byte 16 the SHA-256 hex of the PIN. Response
`0` means success, second byte `2` means wrong password (`-10029`).

## 8. Media

After the play, each transport payload is a QUII packet of type `0xA0..0xA3`.
The body is encrypted, but the declared size excludes the AES padding, so
decrypt down to a multiple of 16 and drop the tail. The clear body is a
"cpacket":

```
00 00 01 | marker (0xE0..0xEA) | payload length u32 | ... codec, fps, width, height ...
```

`0xE0`/`0xE1` are video (H.264 Annex-B, 352x288, 20 fps), `0xE3` is audio
(G.711A, 8 kHz, mono). The last cpacket of a QUII packet usually arrives
truncated by up to 15 bytes; keep it instead of dropping it.

The stream carries no PPS and no IDR has ever appeared in our captures: 3600
frames were discarded (about three minutes at 20 fps) with a NAL census of
`map[1:3455 7:145]`. A client joining mid-stream therefore has to inject SPS/PPS
and wait for a keyframe that may never come.

Resending the PLAY with a fresh sequence number does **not** force a keyframe on
this firmware. Measured with `WIBOX_PLAY_RESENDS=6` (one resend every 10 s over a
long session): the NAL census stayed at `map[1:3455 7:145]`, that is 3455 P
slices and 145 SPS, with no IDR and no PPS at all. There is no command in QUII,
in the CGI or in the vendor SDK to ask for one, so the keyframe is up to the
bell: either its own schedule, which in our captures did not arrive within
three minutes of stream, or a physical event. `/status` publishes
`video_pending`, the frames already received that cannot be used yet, which is
how you tell a stream carrying media without a picture from one that is silent.

## 9. Direct LAN mode

When the daemon shares a network with the bell, none of the above is needed:
QUII over TCP to port 34567, same crypto and same commands. `-discover` scans
the local /24s for a host that answers the setup handshake.
