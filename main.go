package main

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"log"
	"net/http"
	"os"
	"os/signal"
	"runtime"
	"strconv"
	"strings"
	"sync"
	"syscall"
	"time"
)

type config struct {
	account  string
	password string
	clientID string
	deviceID string
	pin      string
	apiToken string
	apiPort  int
	lanAddr  string
	lanPort  int
	channel  int
	stream   int
	baseURL  string
	// remote activa la cadena remota propia cuando no hay via local.
	remote bool
	// remoteOnly salta la via local aunque este configurada.
	remoteOnly bool
	// remoteAttempts son los intentos de p2pconnect (12 s cada uno).
	remoteAttempts int
	// remotePoll es la espera entre sondeos con el timbre dormido.
	remotePoll time.Duration
	// remoteBudget acota el tiempo total buscando el timbre. Tiene que caber
	// dentro del timeout del rest_command de Home Assistant.
	remoteBudget time.Duration

	// controlSock es el socket donde el proceso de video atiende peticiones
	// de apertura. Vacio = /tmp/wibox-video.sock.
	controlSock string
	// videoLock es el fichero de cerrojo del proceso de video: el timbre solo
	// admite una sesion QUII, asi que no puede haber dos a la vez en el equipo.
	videoLock string
	// openCooldown evita que una doble pulsacion mande dos ordenes al rele.
	// 0 lo desactiva.
	openCooldown time.Duration
	// cloudCheck es cada cuanto se comprueba el login y la lista contra el
	// cloud para publicarlo en /status. 0 lo desactiva.
	cloudCheck time.Duration
}

func env(key, def string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return def
}

func loadConfig() config {
	port, _ := strconv.Atoi(env("WIBOX_API_PORT", "8087"))
	channel, _ := strconv.Atoi(env("WIBOX_CHANNEL", "1"))
	stream, _ := strconv.Atoi(env("WIBOX_STREAM", "2"))
	lanPort, _ := strconv.Atoi(env("WIBOX_LAN_PORT", "34567"))
	attempts, _ := strconv.Atoi(env("WIBOX_REMOTE_ATTEMPTS", "20"))
	if attempts < 1 {
		attempts = 20
	}
	budget, _ := strconv.Atoi(env("WIBOX_REMOTE_BUDGET", "240"))
	if budget < 10 {
		budget = 240
	}
	poll, _ := strconv.Atoi(env("WIBOX_AWAKE_POLL", "3"))
	cloudEvery, _ := strconv.Atoi(env("WIBOX_CLOUD_CHECK", "600"))
	cooldown, _ := strconv.Atoi(env("WIBOX_OPEN_COOLDOWN", "3"))
	if poll < 1 {
		poll = 3
	}
	return config{
		account:        env("WIBOX_ACCOUNT", ""),
		password:       env("WIBOX_PASSWORD", ""),
		clientID:       env("WIBOX_CLIENT_ID", "003-2021-87dad215f30f1f7e"),
		deviceID:       env("WIBOX_DEVICE_ID", ""),
		pin:            env("WIBOX_UNLOCK_PASSWORD", ""),
		apiToken:       env("WIBOX_API_TOKEN", ""),
		apiPort:        port,
		lanAddr:        env("WIBOX_LAN_ADDR", ""),
		lanPort:        lanPort,
		channel:        channel,
		stream:         stream,
		baseURL:        env("WIBOX_BASE_URL", "https://r1-8.qvcloud.net"),
		remote:         env("WIBOX_REMOTE", "1") != "0",
		remoteOnly:     env("WIBOX_REMOTE_ONLY", "0") == "1",
		remoteAttempts: attempts,
		remotePoll:     time.Duration(poll) * time.Second,
		cloudCheck:     time.Duration(cloudEvery) * time.Second,
		openCooldown:   time.Duration(cooldown) * time.Second,
		videoLock:      env("WIBOX_VIDEO_LOCK", ""),
		remoteBudget:   time.Duration(budget) * time.Second,
		controlSock:    env("WIBOX_CONTROL_SOCK", ""),
	}
}

// openRecord es la ultima apertura intentada, para /status y para las trazas
// de HA cuando algo falla.
type openRecord struct {
	Device string    `json:"device"`
	Via    string    `json:"via"`
	RC     int       `json:"rc"`
	Error  string    `json:"error,omitempty"`
	At     time.Time `json:"at"`
}

type daemon struct {
	cfg  config
	auth *UserAuth
	mu   sync.Mutex

	// openFunc permite sustituir la apertura en los tests.
	openFunc func(deviceID string) (int, string, error)

	started  time.Time
	statusMu sync.Mutex
	lastOpen *openRecord

	// Ultimo chequeo de la cadena cloud. Se refresca en segundo plano para
	// que /status pueda avisar de unas credenciales caducadas sin pagar un
	// login en cada consulta.
	cloudOK  bool
	cloudErr string
	cloudAt  time.Time

	// Ultima lista de dispositivos que devolvio el cloud, para que /status
	// pueda decir si el timbre esta despierto sin preguntar en cada consulta.
	devicesCache []deviceInfo
	devicesAt    time.Time
}

func (d *daemon) recordOpen(deviceID, via string, rc int, err error) {
	d.statusMu.Lock()
	defer d.statusMu.Unlock()
	rec := &openRecord{Device: deviceID, Via: via, RC: rc, At: time.Now().UTC()}
	if err != nil {
		rec.Error = err.Error()
	}
	d.lastOpen = rec
}

func (d *daemon) recordCloud(ok bool, err error) {
	d.statusMu.Lock()
	defer d.statusMu.Unlock()
	d.cloudOK = ok
	d.cloudAt = time.Now().UTC()
	if err != nil {
		d.cloudErr = err.Error()
	} else {
		d.cloudErr = ""
	}
}

// devicesCached devuelve una copia de la ultima lista si todavia esta fresca.
func (d *daemon) devicesCached(maxAge time.Duration) ([]deviceInfo, bool) {
	d.statusMu.Lock()
	defer d.statusMu.Unlock()
	if d.devicesAt.IsZero() || time.Since(d.devicesAt) > maxAge {
		return nil, false
	}
	out := make([]deviceInfo, len(d.devicesCache))
	copy(out, d.devicesCache)
	return out, true
}

func (d *daemon) cloudState() (bool, string, time.Time) {
	d.statusMu.Lock()
	defer d.statusMu.Unlock()
	return d.cloudOK, d.cloudErr, d.cloudAt
}

// watchCloud comprueba cada intervalo que el login y la lista de dispositivos
// siguen respondiendo. Si no, /status lo dice y Home Assistant puede avisar.
// recordDevices guarda la ultima lista del cloud. Va con la marca de tiempo
// para que /status pueda decir cuando se miro.
func (d *daemon) recordDevices(list []deviceInfo) {
	d.statusMu.Lock()
	d.devicesCache = list
	d.devicesAt = time.Now().UTC()
	d.statusMu.Unlock()
}

// deviceOnlineState dice si ese dispositivo venia marcado como online en la
// ultima lista y cuando se consulto. Sin lista devuelve false y marca cero.
func (d *daemon) deviceOnlineState(deviceID string) (bool, time.Time) {
	d.statusMu.Lock()
	defer d.statusMu.Unlock()
	if d.devicesAt.IsZero() {
		return false, time.Time{}
	}
	for _, dev := range d.devicesCache {
		if dev.ID == deviceID {
			return dev.Online, d.devicesAt
		}
	}
	return false, d.devicesAt
}

// checkCloud hace una vuelta completa: login, lista de dispositivos y
// publicacion del resultado. Se separo del bucle para poder probarlo sin
// esperar.
func (d *daemon) checkCloud() {
	if err := d.login(); err != nil {
		d.recordCloud(false, err)
		return
	}
	resp, err := d.auth.DeviceList()
	if err != nil {
		d.recordCloud(false, err)
		return
	}
	d.recordDevices(deviceInfosFrom(resp))
	d.recordCloud(true, nil)
}

func (d *daemon) watchCloud(interval time.Duration) {
	for {
		d.checkCloud()
		time.Sleep(interval)
	}
}

func (d *daemon) lastOpenCopy() *openRecord {
	d.statusMu.Lock()
	defer d.statusMu.Unlock()
	if d.lastOpen == nil {
		return nil
	}
	cp := *d.lastOpen
	return &cp
}

func (d *daemon) open(deviceID string) (int, string, error) {
	if d.openFunc != nil {
		return d.openFunc(deviceID)
	}
	return d.openDoor(deviceID)
}

func (d *daemon) login() error {
	d.mu.Lock()
	defer d.mu.Unlock()
	if d.auth.session != "" {
		return nil
	}
	if d.cfg.account == "" || d.cfg.password == "" {
		return fmt.Errorf("faltan WIBOX_ACCOUNT y WIBOX_PASSWORD")
	}
	if _, err := d.auth.Login(d.cfg.account, d.cfg.password); err != nil {
		return err
	}
	log.Printf("login ok account=%s", d.cfg.account)
	return nil
}

type deviceInfo struct {
	ID     string `json:"id"`
	Name   string `json:"name"`
	Model  string `json:"model"`
	Online bool   `json:"online"`
}

// deviceCacheTTL es cuanto vale la lista que trae el vigilante de cloud.
// Home Assistant sondea /devices cada dos minutos y no tiene sentido pagar un
// login al fabricante por cada sondeo.
const deviceCacheTTL = 90 * time.Second

func (d *daemon) devices() ([]deviceInfo, error) {
	if list, ok := d.devicesCached(deviceCacheTTL); ok {
		return list, nil
	}
	if err := d.login(); err != nil {
		return nil, err
	}
	resp, err := d.auth.DeviceList()
	if err != nil {
		return nil, err
	}
	return deviceInfosFrom(resp), nil
}

// deviceInfosFrom convierte la respuesta del cloud en la lista que usa la API.
func deviceInfosFrom(resp *Response) []deviceInfo {
	out := make([]deviceInfo, 0, len(resp.Devices))
	for _, dev := range resp.Devices {
		out = append(out, deviceInfo{
			ID:     firstOf(dev, "id", "device-id"),
			Name:   dev["name"],
			Model:  dev["model"],
			Online: dev["online"] == "1" || dev["online-state"] == "1",
		})
	}
	return out
}

// openDoor pide el token del dispositivo y abre la puerta por la via local.
func (d *daemon) openDoor(deviceID string) (int, string, error) {
	if d.cfg.pin == "" {
		return -1, "", fmt.Errorf("falta WIBOX_UNLOCK_PASSWORD: el pin de apertura no se adivina")
	}
	// Si el proceso de video tiene la sesion abierta, el timbre no admite una
	// segunda: se le pide la apertura por su socket. Va antes que el login y
	// el token porque no hacen falta para nada en ese camino.
	if rc, handled, cerr := controlUnlock(d.cfg.controlSock); handled && cerr == nil {
		log.Printf("open %s rc=0 por la sesion de video", deviceID)
		d.recordOpen(deviceID, "video", rc, nil)
		return rc, "", nil
	} else if handled {
		// La sesion de video existe pero no consiguio abrir (tunel muerto,
		// timeout). Un tunel nuevo si puede, asi que no se descarta.
		log.Printf("open: la sesion de video no pudo abrir (%v), se prueba la via directa", cerr)
	}

	if err := d.login(); err != nil {
		return -1, "", err
	}
	token, err := d.auth.DeviceToken(deviceID)
	if err != nil {
		return -1, "", err
	}
	key := []byte(token.DataEncodeKey)
	if len(key) != 32 {
		return -1, "", fmt.Errorf("data-encode-key de %d bytes, se esperaban 32", len(key))
	}
	password := encodeDevicePassword(token.AuthCode)

	// La via local va primero: es mas rapida y el video no sale del edificio.
	if d.cfg.lanAddr != "" && !d.cfg.remoteOnly {
		client, err := DialQuii(d.cfg.lanAddr, key, password, d.cfg.channel, d.cfg.stream, 5*time.Second)
		if err == nil {
			defer client.Close()
			rc, err := client.Unlock(d.cfg.pin, 1, 1)
			d.recordOpen(deviceID, "lan", rc, err)
			return rc, token.Expired, err
		}
		log.Printf("lan no: %v, se prueba la via remota", err)
	}
	if !d.cfg.remote {
		return -1, "", fmt.Errorf("sin via local y la remota esta desactivada (WIBOX_REMOTE=0)")
	}
	rc, err := remoteOpen(d.cfg, d.auth.session, key, password)
	d.recordOpen(deviceID, "remote", rc, err)
	return rc, token.Expired, err
}

func (d *daemon) authOK(r *http.Request) bool {
	if d.cfg.apiToken == "" {
		return true
	}
	if r.Header.Get("Authorization") == "Bearer "+d.cfg.apiToken {
		return true
	}
	return r.URL.Query().Get("token") == d.cfg.apiToken
}

func (d *daemon) writeJSON(w http.ResponseWriter, code int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(code)
	json.NewEncoder(w).Encode(v)
}

// version la fija el build con -ldflags "-X main.version=...".
var version = "dev"

func (d *daemon) routes() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/" {
			http.NotFound(w, r)
			return
		}
		d.writeJSON(w, 200, map[string]any{
			"service": "wibox-daemon",
			"version": version,
			"endpoints": map[string]string{
				"/health":  "GET, sin token",
				"/devices": "GET, Authorization: Bearer <token>",
				"/open":    "GET ?device=<id>, Authorization: Bearer <token>",
			},
		})
	})
	mux.HandleFunc("/health", func(w http.ResponseWriter, r *http.Request) {
		d.writeJSON(w, 200, map[string]any{"ok": true})
	})
	mux.HandleFunc("/status", func(w http.ResponseWriter, r *http.Request) {
		if !d.authOK(r) {
			d.writeJSON(w, 401, map[string]any{"error": "unauthorized"})
			return
		}
		// Una sola consulta al canal de control: dice si hay sesion viva y
		// cuanto video lleva, para que Home Assistant sepa si ya puede
		// decodificar algo.
		frames, keys, pending, session := controlStats(d.cfg.controlSock)
		out := map[string]any{
			"service":       "wibox-daemon",
			"version":       version,
			"uptime_s":      int(time.Since(d.started).Seconds()),
			"video_session": session,
			"last_open":     d.lastOpenCopy(),
		}
		// Los contadores van siempre, a cero cuando no hay sesion, para que
		// Home Assistant tenga los atributos fijos y una automatizacion no
		// dependa de que el sensor cambie de forma.
		out["video_frames"] = frames
		out["video_keyframes"] = keys
		// Ultima lista del cloud: asi Home Assistant sabe si el timbre esta
		// despierto sin gastar una consulta por cada refresco del sensor.
		online, devAt := d.deviceOnlineState(d.cfg.deviceID)
		out["device_online"] = online
		if !devAt.IsZero() {
			out["devices_checked"] = devAt
		}
		// Frames ya recibidos que se descartan porque aun no llego el keyframe:
		// asi se distingue "hay media pero falta IDR" de "no llega nada".
		out["video_pending"] = pending
		// Si otro proceso de video tiene el cerrojo, la sesion local la lleva
		// el y este proceso no puede abrir un tunel propio. Su pid va aparte
		// para poder mirarlo sin entrar en una shell.
		free, holder := videoLockState(d.cfg.videoLock)
		out["video_lock_free"] = free
		if !free && holder > 0 {
			out["video_lock_pid"] = holder
		}
		ok, cerr, at := d.cloudState()
		out["cloud_ok"] = ok
		// Vacio cuando no hay error, para que el atributo exista siempre y
		// una automatizacion no dependa de que el sensor cambie de forma.
		out["cloud_error"] = cerr
		if !at.IsZero() {
			out["cloud_checked"] = at
		}
		d.writeJSON(w, 200, out)
	})
	mux.HandleFunc("/devices", func(w http.ResponseWriter, r *http.Request) {
		if !d.authOK(r) {
			d.writeJSON(w, 401, map[string]any{"error": "unauthorized"})
			return
		}
		list, err := d.devices()
		if err != nil {
			d.writeJSON(w, 502, map[string]any{"error": err.Error()})
			return
		}
		d.writeJSON(w, 200, map[string]any{"devices": list})
	})
	mux.HandleFunc("/open", func(w http.ResponseWriter, r *http.Request) {
		if !d.authOK(r) {
			d.writeJSON(w, 401, map[string]any{"error": "unauthorized"})
			return
		}
		device := r.URL.Query().Get("device")
		if device == "" {
			device = d.cfg.deviceID
		}
		if device == "" {
			d.writeJSON(w, 400, map[string]any{"error": "falta device"})
			return
		}
		// Una doble pulsacion no debe mandar dos ordenes al rele: si la
		// ultima apertura salio bien hace un momento, se corta aqui.
		if cd := d.cfg.openCooldown; cd > 0 {
			if rec := d.lastOpenCopy(); rec != nil && rec.RC == 0 && time.Since(rec.At) < cd {
				// Cuanto falta para poder reintentar, redondeando hacia
				// arriba: un cero aqui invitaria a reintentar de inmediato.
				remaining := int((cd - time.Since(rec.At)).Seconds()) + 1
				d.writeJSON(w, 429, map[string]any{
					"ok":            false,
					"error":         "se acaba de abrir: espera unos segundos",
					"retry_after_s": remaining,
				})
				return
			}
		}

		// Con wait=0 la llamada no espera a que el timbre despierte: si el
		// ultimo chequeo del cloud lo vio dormido, se responde ya. Sirve para
		// automatizaciones que no pueden quedarse cuatro minutos colgadas.
		if r.URL.Query().Get("wait") == "0" {
			if online, at := d.deviceOnlineState(device); !at.IsZero() && !online {
				d.writeJSON(w, 503, map[string]any{
					"ok":    false,
					"error": "el timbre esta dormido segun el ultimo chequeo del cloud",
				})
				return
			}
		}
		rc, expired, err := d.open(device)
		if err != nil {
			log.Printf("open %s rc=%d err=%v", device, rc, err)
			d.writeJSON(w, 502, map[string]any{"ok": false, "rc": rc, "error": err.Error()})
			return
		}
		log.Printf("open %s rc=0", device)
		d.writeJSON(w, 200, map[string]any{"ok": true, "rc": rc, "token_expires": expired})
	})
	return mux
}

func main() {
	check := flag.Bool("check", false, "prueba login, lista de dispositivos y token contra el cloud y sale")
	showVersion := flag.Bool("version", false, "imprime la version y la plataforma y sale")
	discover := flag.Bool("discover", false, "escanea la red local en busca del timbre y sale")
	video := flag.Bool("video", false, "abre el video remoto y escribe H264 Annex-B por stdout")
	videoAudio := flag.Bool("video-audio", false, "con -video, escribe tambien el audio G711A por stderr")
	ust := flag.Bool("ust", false, "recorre la cadena remota hasta los candidatos de p2pconnect y sale")
	remote := flag.Bool("remote", false, "abre la puerta por la cadena remota propia, sin movil ni SDK")
	configPath := flag.String("config", "", "fichero KEY=VALOR con la configuracion (para lanzarlo desde go2rtc u otro padre)")
	talkPath := flag.String("talk", "", "manda un WAV PCM16 mono 8 kHz al altavoz del timbre por LAN (necesita WIBOX_LAN_ADDR)")
	decodePath := flag.String("decode", "", "analiza un volcado hecho con WIBOX_RAW_DUMP usando la clave del token")
	flag.Parse()
	if *showVersion {
		fmt.Printf("wibox-daemon %s %s/%s\n", version, runtime.GOOS, runtime.GOARCH)
		return
	}

	// Se aplica antes de loadConfig: lo que no venga ya en el entorno se toma del
	// fichero. Asi go2rtc puede lanzar el daemon con -config y sin EnvironmentFile.
	if *configPath != "" {
		if err := loadConfigFile(*configPath); err != nil {
			log.Fatalf("config: %v", err)
		}
	}

	cfg := loadConfig()
	if done, lerr := setupLogFile(os.Getenv("WIBOX_LOG_FILE")); lerr != nil {
		log.Printf("log a fichero: %v", lerr)
	} else {
		defer done()
	}
	clientID = cfg.clientID
	d := &daemon{cfg: cfg, auth: NewUserAuth(cfg.baseURL, cfg.clientID), started: time.Now()}

	if *decodePath != "" {
		if cfg.deviceID == "" {
			log.Fatal("decode: falta WIBOX_DEVICE_ID")
		}
		key, err := readKeyFromCheck(cfg, d)
		if err != nil {
			log.Fatalf("decode: %v", err)
		}
		limit := 6
		if v := os.Getenv("WIBOX_DECODE_LIMIT"); v != "" {
			if n, e := strconv.Atoi(v); e == nil {
				limit = n
			}
		}
		if err := decodeDump(*decodePath, key, limit); err != nil {
			log.Fatalf("decode: %v", err)
		}
		return
	}

	if *video {
		if cfg.deviceID == "" {
			log.Fatal("video: falta WIBOX_DEVICE_ID")
		}
		if err := d.login(); err != nil {
			log.Fatalf("login: %v", err)
		}
		var audioOut io.Writer
		if *videoAudio {
			audioOut = os.Stderr
		}
		// Con WIBOX_AUDIO_FILE el audio G711A va a un fichero en vez de a
		// stderr: asi se puede comprobar el formato con ffprobe sin mezclarlo
		// con las trazas.
		if path := os.Getenv("WIBOX_AUDIO_FILE"); path != "" {
			f, ferr := os.Create(path)
			if ferr != nil {
				log.Fatalf("audio: %v", ferr)
			}
			defer f.Close()
			audioOut = f
		}
		// go2rtc solo lanza el productor mientras hay alguien mirando, pero
		// si el timbre se duerme a mitad de sesion el proceso muere y el
		// espectador se queda sin imagen. El bucle reintenta con credenciales
		// frescas hasta que lo paren.
		// El backoff base del bucle de video es configurable: con un timbre
		// que duerme horas, un valor alto ahorra tuneles y logins.
		if secs, aerr := strconv.Atoi(os.Getenv("WIBOX_VIDEO_RETRY")); aerr == nil && secs > 0 {
			videoRetryDelay = time.Duration(secs) * time.Second
		}
		// El techo del backoff tambien se ajusta: con el timbre durmiendo una
		// tarde entera interesa espaciar los intentos sin tocar el codigo.
		if secs, aerr := strconv.Atoi(os.Getenv("WIBOX_VIDEO_RETRY_MAX")); aerr == nil && secs > 0 {
			videoRetryMax = time.Duration(secs) * time.Second
		}
		sigs := make(chan os.Signal, 1)
		signal.Notify(sigs, syscall.SIGINT, syscall.SIGTERM)
		stop := make(chan struct{})
		go func() {
			<-sigs
			close(stop)
		}()
		fetch := func() (*videoCreds, error) {
			if err := d.login(); err != nil {
				return nil, err
			}
			fresh, terr := d.auth.DeviceToken(cfg.deviceID)
			if terr != nil {
				return nil, terr
			}
			if len(fresh.DataEncodeKey) != 32 {
				return nil, fmt.Errorf("data-encode-key de %d bytes, se esperaban 32", len(fresh.DataEncodeKey))
			}
			return &videoCreds{
				Session:  d.auth.session,
				Key:      []byte(fresh.DataEncodeKey),
				Password: remotePlayPassword(fresh),
			}, nil
		}
		if err := runVideoLoop(cfg, fetch, os.Stdout, audioOut, stop); err != nil {
			log.Fatalf("video: %v", err)
		}
		return
	}

	if *ust {
		if cfg.deviceID == "" {
			log.Fatal("falta WIBOX_DEVICE_ID")
		}
		if err := d.login(); err != nil {
			log.Fatalf("login: %v", err)
		}
		if err := probeRemote(cfg, d.auth.session); err != nil {
			log.Fatalf("ust: %v", err)
		}
		return
	}

	if *talkPath != "" {
		if cfg.deviceID == "" {
			log.Fatal("falta WIBOX_DEVICE_ID")
		}
		if err := d.login(); err != nil {
			log.Fatalf("login: %v", err)
		}
		tok, err := d.auth.DeviceToken(cfg.deviceID)
		if err != nil {
			log.Fatalf("token: %v", err)
		}
		// El altavoz vive en la sesion de LAN: la contrasena es la del QUII
		// local, no el dynamic password del cloud.
		if err := runTalk(cfg, []byte(tok.DataEncodeKey), encodeDevicePassword(tok.AuthCode), *talkPath); err != nil {
			log.Fatalf("talk: %v", err)
		}
		return
	}

	if *remote {
		if cfg.deviceID == "" {
			log.Fatal("falta WIBOX_DEVICE_ID")
		}
		if err := d.login(); err != nil {
			log.Fatalf("login: %v", err)
		}
		tok, err := d.auth.DeviceToken(cfg.deviceID)
		if err != nil {
			log.Fatalf("token: %v", err)
		}
		key := []byte(tok.DataEncodeKey)
		rc, err := remoteOpen(cfg, d.auth.session, key, remotePlayPassword(tok))
		if err != nil {
			log.Fatalf("remote: rc=%d err=%v", rc, err)
		}
		fmt.Printf("apertura remota ok rc=%d\n", rc)
		return
	}

	if *discover {
		log.Printf("escaneando el puerto %d en las redes locales...", cfg.lanPort)
		found := discoverDoorbell(cfg.lanPort, 700*time.Millisecond)
		if len(found) == 0 {
			fmt.Println("no se encontro ningun timbre QUII en la red local")
			return
		}
		for _, h := range found {
			fmt.Printf("timbre encontrado en %s\nWIBOX_LAN_ADDR=%s\n", h, h)
		}
		return
	}

	if *check {
		if err := d.login(); err != nil {
			log.Fatalf("login: %v", err)
		}
		list, err := d.devices()
		if err != nil {
			log.Fatalf("devices: %v", err)
		}
		for _, dev := range list {
			fmt.Printf("device id=%s name=%s model=%s\n", dev.ID, dev.Name, dev.Model)
		}
		if cfg.deviceID != "" {
			token, err := d.auth.DeviceToken(cfg.deviceID)
			if err != nil {
				log.Fatalf("token: %v", err)
			}
			fmt.Printf("token authCode=%d chars key=%d bytes dynamic=%d chars expires=%s\n",
				len(token.AuthCode), len(token.DataEncodeKey), len(token.DynamicPassword), token.Expired)
		}
		return
	}

	// Precalentado: la sesion y el discovery se piden al arrancar para que la
	// primera apertura no pague login mas query-hlrv2 en el camino critico.
	go func() {
		if err := d.login(); err != nil {
			log.Printf("precalentado: login fallido: %v", err)
			return
		}
		if _, err := cachedUstServices(cfg); err != nil {
			log.Printf("precalentado: discovery fallido: %v", err)
			return
		}
		log.Printf("precalentado: sesion y servicios listos")
	}()

	if cfg.cloudCheck > 0 {
		go d.watchCloud(cfg.cloudCheck)
	}

	addr := fmt.Sprintf("0.0.0.0:%d", cfg.apiPort)
	log.Printf("wibox-daemon escuchando en %s (token %s)", addr, maskToken(cfg.apiToken))
	log.Printf("via local: %s", orNone(cfg.lanAddr))

	srv := newHTTPServer(addr, d.routes())
	sigs := make(chan os.Signal, 1)
	signal.Notify(sigs, syscall.SIGINT, syscall.SIGTERM)
	go func() {
		<-sigs
		// Cierre ordenado: el supervisor manda SIGTERM al actualizar y no
		// conviene cortar una apertura a medias.
		ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
		defer cancel()
		if err := srv.Shutdown(ctx); err != nil {
			log.Printf("cierre del API: %v", err)
		}
	}()
	if err := srv.ListenAndServe(); err != nil && err != http.ErrServerClosed {
		log.Fatal(err)
	}
}

// remotePlayPassword es la contrasena que va en el PLAY del camino remoto. La app
// oficial manda el dynamic-password del token tal cual. El auth-code solo sirve de
// respaldo cuando el token no lo trae.
func remotePlayPassword(tok *DeviceToken) string {
	if tok.DynamicPassword != "" {
		return tok.DynamicPassword
	}
	return encodeDevicePassword(tok.AuthCode)
}

func maskToken(t string) string {
	if t == "" {
		return "desactivado"
	}
	if len(t) <= 6 {
		return "***"
	}
	return t[:6] + strings.Repeat("*", len(t)-6)
}

func orNone(v string) string {
	if v == "" {
		if env("WIBOX_REMOTE", "1") != "0" {
			return "no configurada, se usa la via remota"
		}
		return "no configurada y la via remota esta desactivada"
	}
	return v
}
