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
- Descubrimiento del timbre en la red local (`-discover`): escanea el puerto
  QUII de las /24 de la maquina y hace el setup contra cada candidato.
- API HTTP con token: `/health`, `/devices`, `/open`.

## Que NO hace

- **No abre la puerta en remoto.** El camino remoto del fabricante pasa por un
  tunel P2P que implementa su SDK propietario, que solo existe compilado para
  arm64 (`libqv-p2p-v2.so`). Sin ese binario no hay tunel. El daemon funciona
  por tanto cuando esta en la misma red que el timbre.
- No manda video ni audio. Solo abre la puerta y consulta estado.

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
| `WIBOX_UNLOCK_PASSWORD` | Pin de apertura (por defecto `0925`) |
| `WIBOX_LAN_ADDR` | `ip:puerto` del timbre en la red local (puerto de stream, 34567 por defecto) |
| `WIBOX_LAN_PORT` | Puerto que escanea `-discover` (por defecto `34567`) |
| `WIBOX_API_TOKEN` | Token de la API HTTP. Vacio = sin autenticacion |
| `WIBOX_API_PORT` | Puerto de la API (por defecto `8087`) |
| `WIBOX_CHANNEL` / `WIBOX_STREAM` | Canal y stream de QUII (por defecto `1` y `1`) |
| `WIBOX_BASE_URL` | Cloud del fabricante |
| `WIBOX_DEBUG` | Cualquier valor activa trazas HTTP |

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
```

Ejemplos:

```
curl http://192.168.1.50:8087/health
curl -H "Authorization: Bearer $WIBOX_API_TOKEN" http://192.168.1.50:8087/devices
curl -H "Authorization: Bearer $WIBOX_API_TOKEN" \
     "http://192.168.1.50:8087/open?device=23123csfewyk"
```

## Verificar la apertura en el edificio

`docs/building-verification.md` es el guion para la visita: descubrir el timbre,
comprobar el cloud, abrir la puerta y leer los errores. La apertura real solo se
puede probar en la misma red que el timbre.

## Despliegue

Ver `deploy/wibox-daemon.service` (systemd) y `deploy/haos-wibox.yaml` (Home
Assistant). El daemon no necesita root: escucha en un puerto alto y solo habla
hacia fuera.

## Protocolo

El detalle esta en `docs/protocol.md` del repo de la app Android
(`gnacho/wibox-android`). Resumen de lo que implementa este daemon:

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
