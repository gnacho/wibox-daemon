# Verificar la apertura en la red del edificio

El daemon ya funciona en x86 contra el cloud del fabricante (login, lista de
dispositivos y token, verificado el 4-nov-2026). Lo unico que no se ha podido
probar es la apertura real contra el timbre, porque el timbre no esta en la red
de casa y el camino remoto depende del SDK propietario para arm64.

Este documento es el guion para cerrar ese hueco en una sola visita.

## Que hace falta

- Un equipo Linux en la misma red que el timbre (portatil, mini PC, contenedor).
  El daemon no necesita root, escucha en un puerto alto y solo abre conexiones
  hacia fuera.
- El binario `wibox-daemon-linux-amd64` (o `-arm64` si el equipo es ARM), de la
  release v0.1.0 del repositorio.
- Las credenciales de la cuenta del timbre y el pin de apertura (0925).
- El movil con la app **cerrada del todo**, no solo en segundo plano.

## Paso 0: liberar el canal

El timbre solo admite una sesion de video a la vez. En las pruebas de la app
Android el Opo dejaba el canal retenido y el timbre respondia BUSY a cualquier
intento posterior. Antes de probar el daemon, cerrar la app en el movil y
comprobar que no queda ninguna sesion abierta.

## Paso 1: descubrir el timbre

```
chmod +x wibox-daemon-linux-amd64
./wibox-daemon-linux-amd64 -discover
```

Salida esperada:

```
escaneando el puerto 34567 en las redes locales...
timbre encontrado en 192.168.1.60:34567
WIBOX_LAN_ADDR=192.168.1.60:34567
```

El escaneo recorre la /24 de cada interfaz de red, asi que cubre la red del
edificio si es una /24 normal. Si devuelve `no se encontro ningun timbre QUII en
la red local`, hay dos causas posibles: que la red sea mas grande que una /24, o
que el timbre este en otra subred. En ese caso se saca la IP de la app oficial o
de la tabla ARP del router y se pasa a mano en el paso 3.

El descubrimiento solo hace el handshake de 32 bytes de QUII. No abre la puerta
ni pide video.

## Paso 2: comprobar el acceso al cloud

```
WIBOX_ACCOUNT='...' WIBOX_PASSWORD='...' ./wibox-daemon-linux-amd64 -check
```

Salida esperada:

```
login ok account=...
device id=23123csfewyk name=ProbeOPO model=IDS7938
token authCode=64 chars key=32 bytes expires=...
```

El daemon pide un token nuevo en cada apertura, asi que el token caducado no es
un problema. Lo que si necesita es salida a internet: sin cloud no hay token y
sin token no hay QUII.

## Paso 3: abrir la puerta

Primera terminal:

```
WIBOX_ACCOUNT='...' WIBOX_PASSWORD='...' \
WIBOX_LAN_ADDR=192.168.1.60:34567 \
WIBOX_API_TOKEN=prueba \
./wibox-daemon-linux-amd64
```

Segunda terminal:

```
curl -H 'Authorization: Bearer prueba' \
  'http://127.0.0.1:8087/open?device=23123csfewyk'
```

Respuesta esperada, y la puerta debe abrirse:

```json
{"ok":true,"rc":0,"token_expires":"..."}
```

## Como leer los errores

| Respuesta o log | Que significa |
|---|---|
| `rc: -10029` y `contrasena de apertura rechazada` | El pin no vale. Revisar `WIBOX_UNLOCK_PASSWORD`. |
| `setup sin respuesta` o `setup status=3` | El puerto no es el de QUII, o el timbre esta ocupado por otra sesion. Volver al paso 0. |
| `sin WIBOX_LAN_ADDR no hay via local` | Falta la variable con la IP del timbre. |
| `timeout esperando "login"` | El cloud no contesta. Comprobar salida a internet del equipo. |
| `data-encode-key de N bytes, se esperaban 32` | El cloud devolvio un token raro. Repetir, y si persiste, anotarlo. |

## Que traer de vuelta

1. La salida literal de `-discover`, con la IP y el puerto reales del timbre.
2. La respuesta literal de `/open`.
3. Las lineas del log del daemon.
4. Si abrio a la primera o hubo que reintentar.
5. Si el timbre estaba ocupado por el movil en ese momento.

Con eso se cierra la verificacion y queda fijar `WIBOX_LAN_ADDR`, instalar la
unidad systemd y dejar el `rest_command` de Home Assistant apuntando al daemon.
