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
		if r.URL.Path != "/api/states" {
			t.Errorf("ruta inesperada %s", r.URL.Path)
		}
		io.WriteString(w, `[{"entity_id":"camera.wibox","attributes":{"friendly_name":"Wibox"}}]`)
	}))
	defer srv.Close()

	ca := &cameraAuto{haURL: srv.URL, token: "tok", client: srv.Client()}
	if !ca.exists() {
		t.Fatal("deberia ver la camara existente")
	}
}

func TestCameraAutoCreatesTheEntry(t *testing.T) {
	var captured map[string]any
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
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
	if captured["still_image_url"] != "http://192.168.1.244:8087/health" {
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
