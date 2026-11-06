package main

import (
	"bytes"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestSnapshotServesAJPEG(t *testing.T) {
	d := newTestDaemon(nil)
	req := httptest.NewRequest("GET", "/snapshot.jpg", nil)
	rec := httptest.NewRecorder()
	d.routes().ServeHTTP(rec, req)

	if rec.Code != 200 {
		t.Fatalf("codigo %d, se esperaba 200", rec.Code)
	}
	if ct := rec.Header().Get("Content-Type"); ct != "image/jpeg" {
		t.Fatalf("content-type %q", ct)
	}
	body := rec.Body.Bytes()
	if len(body) < 2 || body[0] != 0xFF || body[1] != 0xD8 {
		t.Fatalf("no parece un JPEG: % x", body[:min(4, len(body))])
	}
}

// El relleno solo tiene sentido mientras no hay imagen. En cuanto go2rtc
// puede sacar un fotograma, la camara tiene que servir ese y no el dibujo.
func TestSnapshotServesTheLiveFrameWhenGo2rtcHasOne(t *testing.T) {
	live := []byte{0xFF, 0xD8, 0xFF, 0xE0, 0x00, 0x10, 'J', 'F', 'I', 'F', 0x00}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "image/jpeg")
		w.Write(live)
	}))
	defer srv.Close()
	t.Setenv("WIBOX_SNAPSHOT_URL", srv.URL+"/api/frame.jpeg?src=wibox")

	d := newTestDaemon(nil)
	rec := httptest.NewRecorder()
	d.routes().ServeHTTP(rec, httptest.NewRequest("GET", "/snapshot.jpg", nil))

	if got := rec.Body.Bytes(); !bytes.Equal(got, live) {
		t.Fatalf("cuerpo % x, se esperaba el fotograma vivo % x", got, live)
	}
}

func TestSnapshotFallsBackToThePlaceholder(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, "ffmpeg ausente", http.StatusInternalServerError)
	}))
	defer srv.Close()
	t.Setenv("WIBOX_SNAPSHOT_URL", srv.URL+"/api/frame.jpeg?src=wibox")

	d := newTestDaemon(nil)
	rec := httptest.NewRecorder()
	d.routes().ServeHTTP(rec, httptest.NewRequest("GET", "/snapshot.jpg", nil))

	if body := rec.Body.Bytes(); !bytes.Equal(body, placeholderJPEG()) {
		t.Fatalf("no cayo al relleno: %d bytes", len(body))
	}
}

func TestStillURLEndsInTheSnapshot(t *testing.T) {
	auto := newCameraAuto(config{haStream: "rtsp://192.168.1.244:8556/wibox", apiPort: 8087})
	if got := auto.stillURL(); got != "http://192.168.1.244:8087/snapshot.jpg" {
		t.Fatalf("stillURL %q", got)
	}
}
