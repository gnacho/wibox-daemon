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

Hay un `Makefile` con esas mismas ordenes, para que la version y los binarios no
se desincronicen: `make check` pasa gofmt, vet y los tests, `make race` anade el
detector de carreras, `make cover` imprime la cobertura, `make build` deja el
binario local y `make release` deja las dos arquitecturas en `dist/` con la
version leida de `wibox/config.yaml`.

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
| `WIBOX_REMOTE_BUDGET` | Segundos que el daemon sigue buscando el timbre antes de rendirse (por defecto `240`) |
| `WIBOX_AWAKE_POLL` | Segundos entre sondeos con el timbre dormido (por defecto `3`) |
| `WIBOX_VIDEO_WAIT` | Segundos sin frames antes de cortar el video (por defecto `20`) |
| `WIBOX_MPEGTS` | `1` muxea video y audio G.711A en MPEG-TS, para que un solo pipe `exec:` lleve los dos |
| `WIBOX_AUDIO_FILE` | Escribe el audio G.711A crudo en ese fichero en vez de stderr |
| `WIBOX_PLAY_RESENDS` | Veces que se reenvia el play esperando keyframe (por defecto `3`, `0` lo desactiva) |
| `WIBOX_VIDEO_ALL_FRAMES` | `1` reenvia los frames de video anteriores al primer keyframe (por defecto apagado). Los colores salen mal hasta que llega un keyframe, pero hay imagen y el flujo es valido |
| `WIBOX_CONTROL_SOCK` | Socket unix donde el proceso de video atiende las peticiones de apertura (por defecto `/tmp/wibox-video.sock`) |
| `WIBOX_LOG_FILE` | Escribe tambien el log en este fichero. Necesario dentro del add-on: go2rtc se queda con el stderr del hijo |
| `WIBOX_CLOUD_CHECK` | Segundos entre comprobaciones del login contra el cloud, publicadas en `/status` (por defecto `600`, `0` lo desactiva) |
| `WIBOX_VIDEO_MAX` | Tope duro de una sesion de video, en segundos (por defecto `900`, `0` lo desactiva) |
| `WIBOX_VIDEO_LOCK` | Cerrojo que impide un segundo proceso de video sobre el timbre (por defecto `/tmp/wibox-video.lock`) |
| `WIBOX_OPEN_COOLDOWN` | Segundos en los que se ignora otra apertura tras una correcta (por defecto `3`, `0` lo desactiva) |
| `WIBOX_OPEN_SETTLE` | Segundos que el video espera tras una apertura correcta, porque el timbre mantiene ocupada su unica sesion ese rato (por defecto `75`, `0` lo desactiva) |
| `WIBOX_TRICKLE_MS` | Ritmo en milisegundos del relleno que mantiene el stream publicado mientras se monta el tunel (por defecto `20`, `0` lo desactiva) |
| `WIBOX_LOCK_WAIT` | Segundos que un proceso de video nuevo espera al cerrojo de sesion unica en vez de fallar de inmediato (por defecto `300`, `0` falla al momento) |
| `WIBOX_VIDEO_RETRY` | Espera base, en segundos, entre reconexiones de video antes de que el backoff doble (por defecto `5`) |
| `WIBOX_VIDEO_RETRY_MAX` | Techo del backoff de reconexiones de video, en segundos (por defecto `60`) |
| `WIBOX_VIDEO_START_TIMEOUT` | Segundos que go2rtc espera al productor de video antes de matarlo (por defecto `300`) |
| `WIBOX_HA_URL` | URL base de Home Assistant para el alta automatica de la camara (vacio la desactiva) |
| `WIBOX_HA_TOKEN` | Token de larga duracion de Home Assistant para crear la entrada de camara |
| `WIBOX_HA_STREAM` | URL RTSP que Home Assistant debe guardar para la camara (vacio lo desactiva) |
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

`./wibox-daemon -version` imprime la version compilada y la plataforma para la
que se construyo, que es la forma mas rapida de saber que corre un despliegue.
Dentro del add-on esta misma comprobacion se puede programar con la opcion
`cloud_check` (segundos, por defecto 600) y su resultado aparece en `/status`.

La sesion vive en dos conexiones duplex que pueden morir sin dar error, y
entonces cada llamada espera hasta agotar su plazo. Cuando pasa, `/status`
muestra `cloud_ok: false` con un `timeout` en `cloud_error` y `/open` devuelve
`rc: -1`. El daemon renueva la sesion y reintenta una vez ante ese tipo de
error, y reiniciar el add-on tambien lo arregla.

## API

```
GET  /health                      sin token
GET  /devices                     Authorization: Bearer <token>
GET  /open?device=<udid>          Authorization: Bearer <token>
GET  /open?device=<udid>&wait=0   Authorization: Bearer <token>
GET  /status                      Authorization: Bearer <token>
```

Con `wait=0` en `/open` la llamada responde al momento con un 503 si el ultimo
chequeo del cloud vio el timbre dormido, en vez de esperar hasta cuatro minutos
a que despierte. Util para automatizaciones que no pueden quedarse colgadas.

`/status` informa de la version, el tiempo encendido, si hay una sesion de
video viva y el ultimo intento de apertura (via, codigo, error y hora).
Publica ademas `video_frames`, `video_keyframes` y `video_pending`, siempre
presentes y a cero cuando no hay stream: el ultimo cuenta los frames ya
recibidos que no se pueden usar hasta que llegue un keyframe, que es como se
distingue un stream que lleva media sin imagen de uno que no manda nada. `device_online` dice si el timbre aparecia en linea la ultima vez que el daemon pregunto al cloud, y `devices_checked` cuando fue.

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
El daemon crea la entrada por si mismo cuando tiene `WIBOX_HA_URL`,
`WIBOX_HA_TOKEN` y `WIBOX_HA_STREAM` (en el add-on, las opciones `ha_url`,
`ha_token` y `ha_stream`). Mira `/status` cada minuto y crea la entrada si
falta, sin nada mas corriendo.

Hay dos caminos y prueba los dos: con `stream_source` cuando el stream ya
entrega media, y sin el, donde el flujo no puede validar y en vez de fallar
pide confirmacion (`confirmed_ok`), que crea la entrada apoyada en la imagen
fija. El segundo es el que funciona mientras el timbre no manda keyframe, asi
que la camara esta desde el principio y solo la imagen espera.
`deploy/create-camera-when-ready.sh` sigue como respaldo y, sobre todo, por su
tirada RTSP: mientras espera mantiene un `ffmpeg` corto enganchado (salvo
`PULL=0`), porque sin consumidor go2rtc no arranca el productor y el timbre no
tiene a quien mandar frames.

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

Opciones: cuenta, contrasena, client id, id del dispositivo, pin de apertura y
token de la API. Los tiempos se ajustan desde la propia pagina del add-on:
`attempts`, `awake_poll`, `video_wait`, `video_max`, `play_resends`,
`cloud_check`, `open_cooldown`, `video_retry`, `video_retry_max`,
`remote_budget` y `video_start_timeout`. El add-on fuerza la cadena
remota, asi que no se configura direccion local.

Desde Home Assistant:

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

El primer comando espera: si el timbre duerme, el daemon reintenta hasta
conseguir un peer y solo entonces abre, y `attempts` acota esa busqueda. El
segundo, con `wait=0`, responde 503 al momento cuando el ultimo chequeo del
cloud vio el timbre dormido, asi que una automatizacion no se queda colgada.
`open_cooldown` evita que una doble pulsacion mande la orden dos veces.

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

## Diagnostico

Todo lo que sabe el daemon esta en `/status`. Estas son las lecturas habituales:

| Lo que ves | Que significa | Que hacer |
|---|---|---|
| `device_online: false` | El cloud no ha visto el timbre desde `devices_checked`. Va a pilas. | Esperar a que alguien pase por el portal, o abrir con `?wait=0` y tratar el 503 inmediato. |
| `cloud_ok: false` con `cloud_error` | Fallo el login contra el cloud del fabricante. | Revisar `WIBOX_ACCOUNT`/`WIBOX_PASSWORD` y que el gateway responda. |
| `video_pending` subiendo y `video_keyframes: 0` | Llega media y falta el keyframe. El stream esta vivo. | Nada que arreglar: el timbre decide cuando manda un IDR. |
| `video_lock_free: false` | Otro proceso de video del mismo equipo tiene el cerrojo (`/tmp/wibox-video.lock`) y la sesion local la lleva el. | Nada que arreglar: el cerrojo esta haciendo su trabajo. |
| `video_lock_pid` presente | Es el proceso de dentro del contenedor que tiene el cerrojo; con el add-on suele ser el hijo `-video -video-audio` que lanzo go2rtc. | Compararlo con `docker exec <contenedor> ps` antes de matar nada. |
| `video_session: true` con contadores a cero | Hay un reproductor conectado pero el timbre aun no manda nada. | Esperar, se llenara cuando despierte. |
| `last_open.rc: 0` | La puerta se abrio. | Nada. |
| `last_open.rc: -10029` | El PIN de apertura fue rechazado. | Corregir `WIBOX_UNLOCK_PASSWORD`. |
| La llamada responde 429 | El cooldown esta activo: la ultima apertura salio bien hace un momento. El cuerpo trae `retry_after_s` con los segundos que faltan. | Esperar ese tiempo; la segunda pulsacion se ignora a proposito. |
| `last_open.rc: -1` con timeout de p2pconnect | El timbre volvio a dormirse antes de montar el tunel. | Reintentar en una ventana de despertar. |

## Despliegue

Ver `deploy/wibox-daemon.service` (systemd) y `deploy/haos-wibox.yaml` (Home
Assistant). El daemon no necesita root: escucha en un puerto alto y solo habla
hacia fuera.

Las comprobaciones de salud, el procedimiento de publicacion y los limites
conocidos estan en [docs/operations.md](docs/operations.md).

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

Cada release con sus notas esta en [CHANGELOG.md](CHANGELOG.md).

## Proyectos relacionados

- [wibox-android](https://github.com/gnacho/wibox-android): app Android (Kotlin)
  del mismo ecosistema. Las notas del protocolo estan en su `docs/protocol.md`.
