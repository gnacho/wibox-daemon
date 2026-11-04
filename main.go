package main

import (
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"log"
	"net/http"
	"os"
	"strconv"
	"strings"
	"sync"
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
	stream, _ := strconv.Atoi(env("WIBOX_STREAM", "1"))
	lanPort, _ := strconv.Atoi(env("WIBOX_LAN_PORT", "34567"))
	attempts, _ := strconv.Atoi(env("WIBOX_REMOTE_ATTEMPTS", "20"))
	if attempts < 1 {
		attempts = 20
	}
	poll, _ := strconv.Atoi(env("WIBOX_AWAKE_POLL", "3"))
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
	}
}

type daemon struct {
	cfg  config
	auth *UserAuth
	mu   sync.Mutex

	// openFunc permite sustituir la apertura en los tests.
	openFunc func(deviceID string) (int, string, error)
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

func (d *daemon) devices() ([]deviceInfo, error) {
	if err := d.login(); err != nil {
		return nil, err
	}
	resp, err := d.auth.DeviceList()
	if err != nil {
		return nil, err
	}
	out := make([]deviceInfo, 0, len(resp.Devices))
	for _, dev := range resp.Devices {
		out = append(out, deviceInfo{
			ID:     firstOf(dev, "id", "device-id"),
			Name:   dev["name"],
			Model:  dev["model"],
			Online: dev["online"] == "1" || dev["online-state"] == "1",
		})
	}
	return out, nil
}

// openDoor pide el token del dispositivo y abre la puerta por la via local.
func (d *daemon) openDoor(deviceID string) (int, string, error) {
	if d.cfg.pin == "" {
		return -1, "", fmt.Errorf("falta WIBOX_UNLOCK_PASSWORD: el pin de apertura no se adivina")
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
			return rc, token.Expired, err
		}
		log.Printf("lan no: %v, se prueba la via remota", err)
	}
	if !d.cfg.remote {
		return -1, "", fmt.Errorf("sin via local y la remota esta desactivada (WIBOX_REMOTE=0)")
	}
	rc, err := remoteOpen(d.cfg, d.auth.session, key, password)
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

func (d *daemon) routes() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("/health", func(w http.ResponseWriter, r *http.Request) {
		d.writeJSON(w, 200, map[string]any{"ok": true})
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
	discover := flag.Bool("discover", false, "escanea la red local en busca del timbre y sale")
	video := flag.Bool("video", false, "abre el video remoto y escribe H264 Annex-B por stdout")
	videoAudio := flag.Bool("video-audio", false, "con -video, escribe tambien el audio G711A por stderr")
	ust := flag.Bool("ust", false, "recorre la cadena remota hasta los candidatos de p2pconnect y sale")
	remote := flag.Bool("remote", false, "abre la puerta por la cadena remota propia, sin movil ni SDK")
	configPath := flag.String("config", "", "fichero KEY=VALOR con la configuracion (para lanzarlo desde go2rtc u otro padre)")
	flag.Parse()

	// Se aplica antes de loadConfig: lo que no venga ya en el entorno se toma del
	// fichero. Asi go2rtc puede lanzar el daemon con -config y sin EnvironmentFile.
	if *configPath != "" {
		if err := loadConfigFile(*configPath); err != nil {
			log.Fatalf("config: %v", err)
		}
	}

	cfg := loadConfig()
	clientID = cfg.clientID
	d := &daemon{cfg: cfg, auth: NewUserAuth(cfg.baseURL, cfg.clientID)}

	if *video {
		if cfg.deviceID == "" {
			log.Fatal("video: falta WIBOX_DEVICE_ID")
		}
		if err := d.login(); err != nil {
			log.Fatalf("login: %v", err)
		}
		tok, err := d.auth.DeviceToken(cfg.deviceID)
		if err != nil {
			log.Fatalf("token: %v", err)
		}
		var audioOut io.Writer
		if *videoAudio {
			audioOut = os.Stderr
		}
		err = runVideo(cfg, d.auth.session, []byte(tok.DataEncodeKey), encodeDevicePassword(tok.AuthCode), os.Stdout, audioOut)
		if err != nil {
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
		rc, err := remoteOpen(cfg, d.auth.session, key, encodeDevicePassword(tok.AuthCode))
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
			fmt.Printf("token authCode=%d chars key=%d bytes expires=%s\n",
				len(token.AuthCode), len(token.DataEncodeKey), token.Expired)
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

	addr := fmt.Sprintf("0.0.0.0:%d", cfg.apiPort)
	log.Printf("wibox-daemon escuchando en %s (token %s)", addr, maskToken(cfg.apiToken))
	log.Printf("via local: %s", orNone(cfg.lanAddr))
	if err := http.ListenAndServe(addr, d.routes()); err != nil {
		log.Fatal(err)
	}
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
