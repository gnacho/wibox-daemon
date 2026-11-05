# wibox-daemon

*[Read this in English](README.md)*

Daemon en Go para Linux x86 que habla el protocolo del Fermax Wi-Box de
generacion cloud (modelo IDS7938, SDK Quvii/ubell) y expone una API HTTP
sencilla para Home Assistant u otras apps.

Sustituye al movil como pasarela: no hace falta tener la app abierta ni el
telefono a mano.

## Que hace hoy

- Login contra el cloud del fabricante (`r1-8.qvcloud.net`) con el canal
  duplex de userauth.
- Lista de dispositivos de la cuenta.
- Token del dispositivo (auth-code de 64 caracteres y clave de cifrado de 32).
- Apertura de puerta por QUII sobre TCP contra el timbre en la red local.
- **Apertura de puerta en remoto, en Go puro**: discovery `query-hlrv2`, broker
  MQTT UST, `p2pconnect`, sonda UDP de agujero NAT, tunel RB-UDP y QUII dentro
  del tunel. No usa el SDK del fabricante (que solo existe para arm64) ni el
  movil.
- Video remoto en H.264 Annex-B por stdout, para que go2rtc lo sirva a Home
  Assistant. Audio G.711A opcional, por stderr o muxeado con el video en un
  solo flujo MPEG-TS (`WIBOX_MPEGTS=1`).
- Descubrimiento del timbre en la red local (`-discover`): escanea el puerto
  QUII de las /24 de la maquina y hace el setup contra cada candidato.
- API HTTP con token: `/health`, `/devices`, `/open`.

## Que NO hace

- No sirve RTSP ni transcodifica: el video sale en H.264 Annex-B crudo y el
  empaquetado lo pone go2rtc.
- No es un NVR ni mantiene la puerta abierta: cada `-video` es una sesion.
- El timbre va a pilas y duerme. Despierta con actividad fisica o usando la app
  oficial, y la ventana es de unos 30 segundos. Mientras duerme, `p2pconnect`
  devuelve `aoffline` y el daemon reintenta hasta agotar `WIBOX_REMOTE_ATTEMPTS`.

## Compilar

```
go build -o wibox-daemon .
```

Probado con Go 1.27 en `linux/amd64`. Tambien compila para arm64:

```
GOARCH=arm64 go build -o wibox-daemon-arm64 .
```

## Configuracion

Todo por variables de entorno (o un fichero `EnvironmentFile` de systemd):

| Variable | Para que |
|---|---|
| `WIBOX_ACCOUNT` | Cuenta del timbre (la que devuelve aceptar la invitacion) |
| `WIBOX_PASSWORD` | Contrasena de esa cuenta |
| `WIBOX_CLIENT_ID` | Identificador de cliente de la app (por defecto `003-2021-87dad215f30f1f7e`) |
| `WIBOX_DEVICE_ID` | UDID del timbre (por defecto el que se use en `/open`) |
| `WIBOX_UNLOCK_PASSWORD` | Pin de apertura (sin valor por defecto, se pone en la configuracion) |
| `WIBOX_LAN_ADDR` | `ip:puerto` del timbre en la red local (puerto de stream, 34567 por defecto) |
| `WIBOX_LAN_PORT` | Puerto que escanea `-discover` (por defecto `34567`) |
| `WIBOX_API_TOKEN` | Token de la API HTTP. Vacio = sin autenticacion |
| `WIBOX_API_PORT` | Puerto de la API (por defecto `8087`) |
| `WIBOX_CHANNEL` / `WIBOX_STREAM` | Canal y stream de QUII (por defecto `1` y `2`) |
| `WIBOX_BASE_URL` | Cloud del fabricante |
| `WIBOX_REMOTE` | `0` desactiva la via remota (por defecto activada) |
| `WIBOX_REMOTE_ONLY` | `1` salta la via local aunque haya `WIBOX_LAN_ADDR` |
| `WIBOX_REMOTE_ATTEMPTS` | Intentos de `p2pconnect` (por defecto `20`, 12 s cada uno) |
| `WIBOX_AWAKE_POLL` | Segundos entre sondeos con el timbre dormido (por defecto `3`) |
| `WIBOX_VIDEO_WAIT` | Segundos sin frames antes de cortar el video (por defecto `20`) |
| `WIBOX_MPEGTS` | `1` muxea video y audio G.711A en MPEG-TS, para que un solo pipe `exec:` lleve los dos |
| `WIBOX_AUDIO_FILE` | Escribe el audio G.711A crudo en ese fichero en vez de stderr |
| `WIBOX_PLAY_RESENDS` | Veces que se reenvia el play esperando keyframe (por defecto `3`, `0` lo desactiva) |
| `WIBOX_CONTROL_SOCK` | Socket unix donde el proceso de video atiende las peticiones de apertura (por defecto `/tmp/wibox-video.sock`) |
| `WIBOX_LOG_FILE` | Escribe tambien el log en este fichero. Necesario dentro del add-on: go2rtc se queda con el stderr del hijo |
| `WIBOX_CLOUD_CHECK` | Segundos entre comprobaciones del login contra el cloud, publicadas en `/status` (por defecto `600`, `0` lo desactiva) |
| `WIBOX_VIDEO_MAX` | Tope duro de una sesion de video, en segundos (por defecto `900`, `0` lo desactiva) |
| `WIBOX_DEBUG` | Cualquier valor activa trazas HTTP y MQTT |

La configuracion tambien se puede pasar con `-config <fichero>`. Asi es como lo
lanza go2rtc, ya que un proceso hijo no hereda el entorno de systemd:

## Descubrir el timbre en la red local

Si no sabes la IP del timbre, el daemon la busca:

```
./wibox-daemon -discover
```

Escanea el puerto QUII (34567 por defecto) en las /24 de las interfaces de la
maquina, hace el setup contra cada candidato y, si encuentra uno, imprime la
linea `WIBOX_LAN_ADDR=...` lista para pegar en la configuracion. No abre la
puerta ni pide video: solo el handshake.

Ojo: el timbre solo responde si el daemon corre en la misma red que el. Desde
otra red el escaneo no encuentra nada, y eso no es un fallo del daemon.

## Hablar al visitante (solo LAN)

El timbre tiene altavoz, pero solo en su red local: el tunel del cloud lleva
video y el audio del timbre, nunca audio hacia el.

```sh
ffmpeg -i mensaje.wav -ar 8000 -ac 1 -f wav -c:a pcm_s16le /tmp/mensaje8k.wav
WIBOX_LAN_ADDR=192.168.1.60:34567 ./wibox-daemon -talk /tmp/mensaje8k.wav
```

El fichero tiene que ser PCM16, mono, 8 kHz. El daemon abre una sesion de talk
en el mismo puerto TCP del video, manda el audio en tramas de 40 ms y cierra.

## Comprobar el acceso al cloud

```
./wibox-daemon -check
```

Hace login, lista los dispositivos y pide el token. Es la forma rapida de ver
si las credenciales siguen valiendo.

## API

```
GET  /health                      sin token
GET  /devices                     Authorization: Bearer <token>
GET  /open?device=<udid>          Authorization: Bearer <token>
GET  /status                      Authorization: Bearer <token>
```

`/status` informa de la version, el tiempo encendido, si hay una sesion de
video viva y el ultimo intento de apertura (via, codigo, error y hora).
Mientras hay sesion publica ademas `video_frames` y `video_keyframes`, que es
como se distingue un stream que solo lleva audio de uno que ya tiene imagen.

Ejemplos:

```
curl http://192.168.1.50:8087/health
curl -H "Authorization: Bearer $WIBOX_API_TOKEN" http://192.168.1.50:8087/devices
curl -H "Authorization: Bearer $WIBOX_API_TOKEN" \
     "http://192.168.1.50:8087/open?device=23123csfewyk"
```

El timbre solo admite una sesion QUII a la vez. Cuando go2rtc ya esta
emitiendo, la sesion la tiene el proceso de video, asi que la API le pide la
apertura por un socket unix local en vez de montar un segundo tunel (que solo
recibiria un BUSY). Si no hay stream en curso, `/open` monta su propio tunel
como siempre.

## Video en Home Assistant

El daemon no implementa RTSP. Escribe H.264 Annex-B crudo por stdout y deja que
[go2rtc](https://github.com/AlexxIT/go2rtc) haga el empaquetado, que es ademas el
motor de streaming que ya trae Home Assistant desde 2024.11.

El transporte `pipe` de la fuente `exec:` lee exactamente eso: un bitstream
H.264/H.265 crudo por stdout.

```yaml
# deploy/go2rtc.yaml
streams:
  wibox: exec:/usr/local/bin/wibox-daemon -config /etc/wibox-daemon.conf -video#killsignal=2#killtimeout=5

api:
  listen: "127.0.0.1:1984"
rtsp:
  listen: "127.0.0.1:8554"
```

Despues se anade a Home Assistant como camara generica apuntando a
`rtsp://127.0.0.1:8554/wibox`, o como stream de go2rtc para WebRTC. Dos avisos:

- Cada `-video` es una sesion contra el timbre. Con el timbre dormido el proceso
  se pasa el rato en el bucle de reintentos de `p2pconnect`, asi que conviene
  abrir el stream bajo demanda (un toque en el panel) y no dejarlo siempre
  encendido.
- El audio es G.711A (PCMA). Con `WIBOX_MPEGTS=1` el daemon escribe MPEG-TS con
  las dos pistas por stdout (H.264 con stream type `0x1B`, A-law con `0x90`), que
  es lo que usa el add-on: go2rtc mapea `0x90` a su codec PCMA. Sin el flag
  `-video-audio` escribe el audio por stderr y haria falta una segunda fuente
  `ffmpeg`, porque go2rtc trata dos fuentes `exec:` como failover y no las mezcla.
  Ojo: `ffprobe` no reconoce A-law dentro de MPEG-TS (el propio ffmpeg lo escribe
  como stream type `0x06`), asi que no sirve como gate de esa pista.

## Camara en Home Assistant

Home Assistant ya no acepta una camara generica por YAML y el config flow valida
abriendo el stream, asi que la entrada solo se puede crear cuando el stream
lleva un keyframe. `deploy/camera-creation.md` tiene las llamadas exactas,
incluido el bloque `advanced` obligatorio.

## Add-on de Home Assistant

Home Assistant OS no puede ejecutar un binario suelto, asi que el daemon tambien
se distribuye como add-on. Corre dentro de HAOS, habla con el cloud del
fabricante y expone la API HTTP en el puerto 8087, o sea que la puerta se abre
desde Home Assistant sin el movil y sin estar en la red del edificio.

```
Ajustes -> Add-ons -> Tienda de add-ons -> Repositorios
   anadir https://github.com/gnacho/wibox-daemon
instalar "Wibox Doorbell"
```

Opciones: cuenta, contrasena, client id, id del dispositivo, pin de apertura,
token de la API, intentos de p2pconnect, espera entre sondeos y espera de video.
El add-on fuerza la cadena remota, asi que no se configura direccion local.

Desde Home Assistant:

```yaml
rest_command:
  wibox_open:
    url: "http://homeassistant.local:8087/open"
    method: GET
    headers:
      Authorization: "Bearer <api_token>"
    timeout: 120
```

El timeout tiene que ser generoso: si el timbre duerme, el daemon reintenta hasta
conseguir un peer y solo entonces abre. `WIBOX_REMOTE_ATTEMPTS` y la opcion
`attempts` acotan esa espera.

### Video a traves del add-on

El add-on lleva su propio [go2rtc](https://github.com/AlexxIT/go2rtc), asi que la
camara funciona sin instalar nada mas. go2rtc es el proceso principal y el daemon
es su fuente `exec:`, o sea que la sesion de video solo se abre cuando alguien
mira la camara y se corta al terminar.

```
rtsp://<ip-del-haos>:8556/wibox
```

Se anade en Home Assistant como camara generica (RTSP) o con la integracion de
go2rtc. `starttimeout` esta en 600 s porque el timbre duerme y puede tardar
minutos en responder: mejor abrir la camara bajo demanda que dejarla siempre
encendida, y la primera imagen tarda cuando nadie ha llamado.

## Verificar la apertura en el edificio

`docs/building-verification.md` es el guion para la visita: descubrir el timbre,
comprobar el cloud, abrir la puerta y leer los errores. La apertura real solo se
puede probar en la misma red que el timbre.

## Despliegue

Ver `deploy/wibox-daemon.service` (systemd) y `deploy/haos-wibox.yaml` (Home
Assistant). El daemon no necesita root: escucha en un puerto alto y solo habla
hacia fuera.

## Protocolo

El protocolo completo esta en [`docs/protocol.md`](docs/protocol.md). Resumen de
lo que implementa este daemon:

1. **userauth duplex**: `GET /auth/nologin;jus_duplex=down` solo para obtener la
   cookie `jsessionid`, `POST /auth/nologin;jus_duplex=up` con el envelope XML,
   y despues `GET .../down` para recoger la respuesta encolada. El orden importa:
   si el long-poll esta en vuelo cuando llega la subida, el gateway contesta
   `CAUTHNETClient::Response(packet): BeginResponseGenerator return failed`.
2. **Envelope XStream**: `content` antes de `header`, campos en kebab-case,
   `version` vacia, indentacion de 3 espacios.
3. **QUII sobre TCP**: setup de 32 bytes con `0xA9` en claro, PLAY con usuario
   `adminapp2`, y ordenes transparentes con tipo `0xFE`. Cifrado AES-256-CBC con
   IV de ceros y SHA-256 de (cabecera clara + cuerpo) pegado al cuerpo.
4. **Apertura**: orden transparente 4, cuerpo con numero de cerradura, canal, y
   el SHA-256 hexadecimal del pin a partir del byte 16.

## Verificacion

```
go test ./...
```

22 tests: envelope XStream byte a byte contra el formato del cliente oficial,
parseo de respuestas reales capturadas del gateway, vector AES-256-CBC calculado
aparte con openssl, disposicion de la cabecera QUII, cola SHA-256 del cuerpo,
cuerpo de apertura (orden 4, pin en el byte 16), apertura completa sobre un
socket de loopback que hace de timbre, rechazo de contrasena, setup con status
malo, descubrimiento en LAN, y contrato HTTP de `/open` (token obligatorio, 200
con `ok`, 502 con el codigo de error).

## Proyectos relacionados

- [wibox-android](https://github.com/gnacho/wibox-android): app Android (Kotlin)
  del mismo ecosistema. Las notas del protocolo estan en su `docs/protocol.md`.
