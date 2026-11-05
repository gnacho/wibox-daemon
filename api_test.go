package main

import (
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

func newTestDaemon(open func(string) (int, string, error)) *daemon {
	return &daemon{
		// Socket de control propio: en la maquina de desarrollo puede haber un
		// vigilante con el socket por defecto y el test lo confundiria con una
		// sesion de video.
		cfg:      config{apiToken: "secreto", deviceID: "dev-por-defecto", controlSock: "/tmp/wibox-test-sin-video.sock"},
		openFunc: open,
	}
}

func TestOpenRequiresToken(t *testing.T) {
	d := newTestDaemon(func(string) (int, string, error) {
		t.Fatal("no deberia abrir sin token")
		return 0, "", nil
	})
	rec := httptest.NewRecorder()
	d.routes().ServeHTTP(rec, httptest.NewRequest("GET", "/open", nil))
	if rec.Code != 401 {
		t.Fatalf("codigo %d, esperaba 401", rec.Code)
	}
}

// Camino feliz de /open: el doble de apertura devuelve rc=0 y la API responde
// ok. Es el contrato que consume Home Assistant.
func TestOpenSuccess(t *testing.T) {
	var seen string
	d := newTestDaemon(func(device string) (int, string, error) {
		seen = device
		return 0, "2026-11-05 12:14:42", nil
	})
	rec := httptest.NewRecorder()
	req := httptest.NewRequest("GET", "/open", nil)
	req.Header.Set("Authorization", "Bearer secreto")
	d.routes().ServeHTTP(rec, req)

	if rec.Code != 200 {
		t.Fatalf("codigo %d: %s", rec.Code, rec.Body.String())
	}
	var body map[string]any
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatalf("json: %v", err)
	}
	if body["ok"] != true {
		t.Fatalf("ok=%v", body["ok"])
	}
	if body["token_expires"] != "2026-11-05 12:14:42" {
		t.Fatalf("token_expires=%v", body["token_expires"])
	}
	if seen != "dev-por-defecto" {
		t.Fatalf("device=%q, esperaba el de configuracion", seen)
	}
}

func TestOpenPassesDeviceParam(t *testing.T) {
	var seen string
	d := newTestDaemon(func(device string) (int, string, error) {
		seen = device
		return 0, "", nil
	})
	rec := httptest.NewRecorder()
	req := httptest.NewRequest("GET", "/open?device=otro", nil)
	req.Header.Set("Authorization", "Bearer secreto")
	d.routes().ServeHTTP(rec, req)
	if seen != "otro" {
		t.Fatalf("device=%q", seen)
	}
}

func TestOpenReportsFailure(t *testing.T) {
	d := newTestDaemon(func(string) (int, string, error) {
		return -10029, "", errors.New("contrasena de apertura rechazada")
	})
	rec := httptest.NewRecorder()
	req := httptest.NewRequest("GET", "/open?token=secreto", nil)
	d.routes().ServeHTTP(rec, req)
	if rec.Code != 502 {
		t.Fatalf("codigo %d, esperaba 502", rec.Code)
	}
	var body map[string]any
	json.Unmarshal(rec.Body.Bytes(), &body)
	if body["ok"] != false || body["rc"] != float64(-10029) {
		t.Fatalf("cuerpo=%v", body)
	}
}

func TestHealthWithoutToken(t *testing.T) {
	d := newTestDaemon(nil)
	rec := httptest.NewRecorder()
	d.routes().ServeHTTP(rec, httptest.NewRequest("GET", "/health", nil))
	if rec.Code != 200 {
		t.Fatalf("codigo %d", rec.Code)
	}
}

func TestRootListsEndpoints(t *testing.T) {
	d := &daemon{cfg: config{apiToken: "t"}}
	rec := httptest.NewRecorder()
	d.routes().ServeHTTP(rec, httptest.NewRequest("GET", "/", nil))
	if rec.Code != 200 {
		t.Fatalf("codigo %d", rec.Code)
	}
	var body map[string]any
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatalf("json: %v", err)
	}
	if body["service"] != "wibox-daemon" {
		t.Fatalf("service=%v", body["service"])
	}
}

func TestUnknownPathIs404(t *testing.T) {
	d := &daemon{cfg: config{apiToken: "t"}}
	rec := httptest.NewRecorder()
	d.routes().ServeHTTP(rec, httptest.NewRequest("GET", "/nada", nil))
	if rec.Code != 404 {
		t.Fatalf("codigo %d, se esperaba 404", rec.Code)
	}
}

// Con el stream en marcha la unica sesion del timbre la tiene el proceso de
// video, asi que /open debe ir por su socket y no tocar el cloud.
func TestOpenUsesVideoSession(t *testing.T) {
	sock := testSockPath(t)
	calls := 0
	srv, err := newControlServer(sock, func() (int, error) {
		calls++
		return 0, nil
	}, nil)
	if err != nil {
		t.Fatalf("servidor de control: %v", err)
	}
	defer srv.close()

	d := &daemon{cfg: config{apiToken: "secreto", pin: "1379", controlSock: sock}}
	rec := httptest.NewRecorder()
	req := httptest.NewRequest("GET", "/open?device=23123csfewyk", nil)
	req.Header.Set("Authorization", "Bearer secreto")
	d.routes().ServeHTTP(rec, req)

	if rec.Code != 200 {
		t.Fatalf("codigo %d, cuerpo %s", rec.Code, rec.Body.String())
	}
	if calls != 1 {
		t.Fatalf("el servidor de control se llamo %d veces", calls)
	}
	var body map[string]any
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatalf("json: %v", err)
	}
	if body["ok"] != true {
		t.Fatalf("respuesta %v", body)
	}
}

// /status es lo que mira quien no ve el log: version, uptime, si hay sesion de
// video viva y como fue la ultima apertura.
func TestStatusReportsState(t *testing.T) {
	d := newTestDaemon(func(string) (int, string, error) { return 0, "", nil })
	d.started = time.Now().Add(-90 * time.Second)
	d.recordOpen("dev-por-defecto", "remote", -10029, errors.New("contrasena de apertura rechazada"))

	rec := httptest.NewRecorder()
	req := httptest.NewRequest("GET", "/status", nil)
	req.Header.Set("Authorization", "Bearer secreto")
	d.routes().ServeHTTP(rec, req)
	if rec.Code != 200 {
		t.Fatalf("codigo %d: %s", rec.Code, rec.Body.String())
	}
	var got struct {
		Service      string      `json:"service"`
		Version      string      `json:"version"`
		UptimeS      int         `json:"uptime_s"`
		VideoSession bool        `json:"video_session"`
		LastOpen     *openRecord `json:"last_open"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &got); err != nil {
		t.Fatalf("json: %v", err)
	}
	if got.Service != "wibox-daemon" {
		t.Fatalf("service=%q", got.Service)
	}
	if got.UptimeS < 90 {
		t.Fatalf("uptime=%d, esperaba al menos 90", got.UptimeS)
	}
	if got.VideoSession {
		t.Fatal("no hay proceso de video en el test")
	}
	if got.LastOpen == nil || got.LastOpen.Via != "remote" || got.LastOpen.RC != -10029 {
		t.Fatalf("last_open=%+v", got.LastOpen)
	}
	if got.LastOpen.Error == "" {
		t.Fatal("la ultima apertura fallida debe llevar el error")
	}
}

func TestStatusRequiresToken(t *testing.T) {
	d := newTestDaemon(func(string) (int, string, error) { return 0, "", nil })
	rec := httptest.NewRecorder()
	d.routes().ServeHTTP(rec, httptest.NewRequest("GET", "/status", nil))
	if rec.Code != 401 {
		t.Fatalf("codigo %d, esperaba 401", rec.Code)
	}
}

func TestStatusReportsVideoFrames(t *testing.T) {
	sock := t.TempDir() + "/video.sock"
	srv, err := newControlServer(sock, func() (int, error) { return 0, nil }, func() (int, int) { return 120, 4 })
	if err != nil {
		t.Fatalf("servidor: %v", err)
	}
	defer srv.close()

	d := newTestDaemon(nil)
	d.cfg.controlSock = sock
	srvHTTP := httptest.NewServer(d.routes())
	defer srvHTTP.Close()

	req, _ := http.NewRequest("GET", srvHTTP.URL+"/status", nil)
	req.Header.Set("Authorization", "Bearer secreto")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("status: %v", err)
	}
	defer resp.Body.Close()
	var got struct {
		VideoSession bool `json:"video_session"`
		VideoFrames  int  `json:"video_frames"`
		VideoKeys    int  `json:"video_keyframes"`
	}
	if derr := json.NewDecoder(resp.Body).Decode(&got); derr != nil {
		t.Fatalf("json: %v", derr)
	}
	if !got.VideoSession {
		t.Fatal("se esperaba sesion de video")
	}
	if got.VideoFrames != 120 || got.VideoKeys != 4 {
		t.Fatalf("frames=%d keys=%d", got.VideoFrames, got.VideoKeys)
	}
}
