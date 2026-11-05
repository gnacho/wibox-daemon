package main

import (
	"testing"
	"time"
)

func TestDevicesUsesTheFreshCache(t *testing.T) {
	d := newTestDaemon(nil)
	d.recordDevices([]deviceInfo{{ID: "bell1", Name: "Portal", Online: true}})

	got, err := d.devices()
	if err != nil {
		t.Fatalf("no deberia pagar un login con la cache fresca: %v", err)
	}
	if len(got) != 1 || got[0].ID != "bell1" {
		t.Fatalf("lista inesperada: %v", got)
	}
}

func TestDevicesIgnoresTheStaleCache(t *testing.T) {
	d := newTestDaemon(nil)
	d.recordDevices([]deviceInfo{{ID: "bell1"}})
	// El daemon de prueba no trae cliente de cloud: sin auth, login() revienta
	// en vez de devolver el error de credenciales que busca el test.
	d.auth = NewUserAuth("http://127.0.0.1:1", "003-2021-87dad215f30f1f7e")
	d.statusMu.Lock()
	d.devicesAt = time.Now().Add(-time.Hour)
	d.statusMu.Unlock()

	if _, err := d.devices(); err == nil {
		t.Fatal("una cache caducada debe volver al cloud, y sin credenciales falla")
	}
}

func TestDevicesCacheReturnsACopy(t *testing.T) {
	d := newTestDaemon(nil)
	d.recordDevices([]deviceInfo{{ID: "bell1"}})

	first, _ := d.devices()
	first[0].ID = "tocado"

	second, _ := d.devices()
	if second[0].ID != "bell1" {
		t.Fatalf("la cache se dejo mutar: %q", second[0].ID)
	}
}
