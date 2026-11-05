package main

import (
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

func TestStillURLEndsInTheSnapshot(t *testing.T) {
	auto := newCameraAuto(config{haStream: "rtsp://192.168.1.244:8556/wibox", apiPort: 8087})
	if got := auto.stillURL(); got != "http://192.168.1.244:8087/snapshot.jpg" {
		t.Fatalf("stillURL %q", got)
	}
}
