package main

import (
	"encoding/json"
	"errors"
	"net/http/httptest"
	"testing"
)

func newTestDaemon(open func(string) (int, string, error)) *daemon {
	return &daemon{
		cfg:      config{apiToken: "secreto", deviceID: "dev-por-defecto"},
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
