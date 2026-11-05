package main

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

func TestCameraAutoSeesAnExistingCamera(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/api/config/config_entries/entry":
			// Home Assistant nombra la entrada con el host, no con "wibox":
			// 192.168.1.244 se convierte en 192_168_1_244.
			io.WriteString(w, `[{"domain":"generic","title":"192_168_1_244"}]`)
		case "/api/states":
			io.WriteString(w, `[{"entity_id":"camera.192_168_1_244","attributes":{"friendly_name":"192_168_1_244"}}]`)
		default:
			t.Errorf("ruta inesperada %s", r.URL.Path)
		}
	}))
	defer srv.Close()

	ca := &cameraAuto{haURL: srv.URL, token: "tok", stream: "rtsp://192.168.1.244:8556/wibox", client: srv.Client()}
	if !ca.exists() {
		t.Fatal("deberia ver la entrada existente por el titulo")
	}
}

func TestCameraAutoCreatesTheEntry(t *testing.T) {
	var captured map[string]any
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.URL.Path == "/api/config/config_entries/entry":
			io.WriteString(w, `[]`)
		case r.URL.Path == "/api/states":
			io.WriteString(w, `[]`)
		case r.URL.Path == "/api/config/config_entries/flow" && r.Method == "POST":
			io.WriteString(w, `{"flow_id":"abc123","type":"form"}`)
		case r.URL.Path == "/api/config/config_entries/flow/abc123":
			body, _ := io.ReadAll(r.Body)
			json.Unmarshal(body, &captured)
			io.WriteString(w, `{"type":"create_entry","result":"ok"}`)
		default:
			t.Errorf("ruta inesperada %s %s", r.Method, r.URL.Path)
		}
	}))
	defer srv.Close()

	ca := &cameraAuto{haURL: srv.URL, token: "tok", stream: "rtsp://192.168.1.244:8556/wibox", apiPort: 8087, client: srv.Client()}
	if err := ca.ensure(); err != nil {
		t.Fatalf("ensure: %v", err)
	}
	if captured["stream_source"] != "rtsp://192.168.1.244:8556/wibox" {
		t.Fatalf("stream_source=%v", captured["stream_source"])
	}
	if captured["still_image_url"] != "http://192.168.1.244:8087/snapshot.jpg" {
		t.Fatalf("still_image_url=%v", captured["still_image_url"])
	}
	if _, ok := captured["advanced"]; !ok {
		t.Fatal("falta el bloque advanced")
	}
}

func TestWatchCameraEntryNeedsTokenAndStream(t *testing.T) {
	stop := make(chan struct{})
	close(stop)
	done := make(chan struct{})
	go func() {
		watchCameraEntry(config{}, time.Minute, stop)
		close(done)
	}()
	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("sin token ni stream deberia volver al momento")
	}
}

// Si el alta con stream no valida (el timbre no manda imagen), Home Assistant
// responde con un paso user_confirm y crea la entrada al confirmarlo.
func TestCameraAutoConfirmsWithoutStream(t *testing.T) {
	var confirmed bool
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.URL.Path == "/api/config/config_entries/entry":
			io.WriteString(w, `[]`)
		case r.URL.Path == "/api/states":
			io.WriteString(w, `[]`)
		case r.URL.Path == "/api/config/config_entries/flow" && r.Method == "POST":
			io.WriteString(w, `{"flow_id":"f1","type":"form"}`)
		case r.URL.Path == "/api/config/config_entries/flow/f1":
			body, _ := io.ReadAll(r.Body)
			var sent map[string]any
			json.Unmarshal(body, &sent)
			if v, ok := sent["confirmed_ok"].(bool); ok && v {
				confirmed = true
				io.WriteString(w, `{"type":"create_entry","result":"ok"}`)
				return
			}
			io.WriteString(w, `{"type":"form","step_id":"user_confirm","errors":null}`)
		default:
			t.Errorf("ruta inesperada %s %s", r.Method, r.URL.Path)
		}
	}))
	defer srv.Close()

	ca := &cameraAuto{haURL: srv.URL, token: "tok", stream: "rtsp://192.168.1.244:8556/wibox", apiPort: 8087, client: srv.Client()}
	if err := ca.ensure(); err != nil {
		t.Fatalf("ensure: %v", err)
	}
	if !confirmed {
		t.Fatal("no se confirmo el alta sin stream")
	}
}
