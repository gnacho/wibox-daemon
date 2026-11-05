# Verifying the door opening on the building network

The daemon already works on x86 against the manufacturer cloud (login, device
list and token, verified on 4-Nov-2026). The only thing that has not been tested
is the real opening against the doorbell, because the doorbell is not on the home
network and the remote path depends on the proprietary SDK for arm64.

This document is the script to close that gap in a single visit.

## What you need

- A Linux machine on the same network as the doorbell (laptop, mini PC,
  container). The daemon does not need root, it listens on a high port and only
  opens outbound connections.
- The `wibox-daemon-linux-amd64` binary (or `-arm64` if the machine is ARM),
  from release v0.1.0 of the repository.
- The credentials of the doorbell account and the opening PIN (1379).
- The phone with the app **fully closed**, not just in the background.

## Step 0: release the channel

The doorbell accepts only one video session at a time. In the Android app tests
the Opo left the channel held and the doorbell answered BUSY to any later
attempt. Before testing the daemon, close the app on the phone and check that no
session is left open.

## Step 1: discover the doorbell

```
chmod +x wibox-daemon-linux-amd64
./wibox-daemon-linux-amd64 -discover
```

Expected output:

```
escaneando el puerto 34567 en las redes locales...
timbre encontrado en 192.168.1.60:34567
WIBOX_LAN_ADDR=192.168.1.60:34567
```

The scan walks the /24 of every network interface, so it covers the building
network if it is a normal /24. If it returns
`no se encontro ningun timbre QUII en la red local`, there are two possible
causes: the network is larger than a /24, or the doorbell is on another subnet.
In that case take the IP from the official app or from the router ARP table and
pass it by hand in step 3.

Discovery only does the 32-byte QUII handshake. It does not open the door or
request video.

## Step 2: check cloud access

```
WIBOX_ACCOUNT='...' WIBOX_PASSWORD='...' ./wibox-daemon-linux-amd64 -check
```

Expected output:

```
login ok account=...
device id=23123csfewyk name=ProbeOPO model=IDS7938
token authCode=64 chars key=32 bytes expires=...
```

The daemon requests a new token on every opening, so an expired token is not a
problem. What it does need is internet access: without cloud there is no token
and without token there is no QUII.

## Step 3: open the door

First terminal:

```
WIBOX_ACCOUNT='...' WIBOX_PASSWORD='...' \
WIBOX_LAN_ADDR=192.168.1.60:34567 \
WIBOX_API_TOKEN=test \
./wibox-daemon-linux-amd64
```

Second terminal:

```
curl -H 'Authorization: Bearer test' \
  'http://127.0.0.1:8087/open?device=23123csfewyk'
```

Expected response, and the door must open:

```json
{"ok":true,"rc":0,"token_expires":"..."}
```

## How to read the errors

| Response or log | What it means |
|---|---|
| `rc: -10029` and `contrasena de apertura rechazada` | The PIN is wrong. Check `WIBOX_UNLOCK_PASSWORD`. |
| `setup sin respuesta` or `setup status=3` | The port is not the QUII one, or the doorbell is busy with another session. Go back to step 0. |
| `sin WIBOX_LAN_ADDR no hay via local` | The variable with the doorbell IP is missing. |
| `timeout esperando "login"` | The cloud does not answer. Check the machine internet access. |
| `data-encode-key de N bytes, se esperaban 32` | The cloud returned a strange token. Retry, and if it persists, write it down. |

## What to bring back

1. The literal output of `-discover`, with the real doorbell IP and port.
2. The literal response of `/open`.
3. The daemon log lines.
4. Whether it opened on the first try or a retry was needed.
5. Whether the doorbell was busy with the phone at that moment.

With that the verification is closed and what remains is to set `WIBOX_LAN_ADDR`,
install the systemd unit and point the Home Assistant `rest_command` at the
daemon.
