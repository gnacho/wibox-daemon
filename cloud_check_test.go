package main

import (
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestCheckCloudCachesTheDeviceList(t *testing.T) {
	g := &fakeGateway{session: "SESION"}
	srv := httptest.NewServer(http.HandlerFunc(g.serveHTTP))
	defer srv.Close()

	d := &daemon{cfg: config{account: "cuenta", password: "clave", deviceID: "bell1"}, auth: NewUserAuth(srv.URL, "003-2021-87dad215f30f1f7e")}
	d.checkCloud()

	ok, cerr, at := d.cloudState()
	if !ok || cerr != "" || at.IsZero() {
		t.Fatalf("cloud ok=%v err=%q at=%v", ok, cerr, at)
	}
	online, when := d.deviceOnlineState("bell1")
	if !online {
		t.Fatal("el timbre deberia aparecer en linea")
	}
	if when.IsZero() {
		t.Fatal("falta la marca de la lista de dispositivos")
	}
}

func TestCheckCloudReportsAFailedLogin(t *testing.T) {
	g := &fakeGateway{session: "SESION", result: 100100003}
	srv := httptest.NewServer(http.HandlerFunc(g.serveHTTP))
	defer srv.Close()

	d := &daemon{cfg: config{account: "cuenta", password: "clave"}, auth: NewUserAuth(srv.URL, "003-2021-87dad215f30f1f7e")}
	d.checkCloud()

	ok, cerr, _ := d.cloudState()
	if ok || cerr == "" {
		t.Fatalf("se esperaba fallo, ok=%v err=%q", ok, cerr)
	}
}
