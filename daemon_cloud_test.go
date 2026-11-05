package main

import (
	"net/http"
	"net/http/httptest"
	"testing"
)

// login guarda la sesion y devices la reutiliza: contra el gateway falso se
// comprueba la lista y que la segunda llamada no vuelve a pedir login.
func TestDaemonLogsInOnceAndListsDevices(t *testing.T) {
	g := &fakeGateway{session: "SESION"}
	srv := httptest.NewServer(http.HandlerFunc(g.serveHTTP))
	defer srv.Close()

	d := &daemon{
		cfg:  config{account: "cuenta", password: "clave"},
		auth: NewUserAuth(srv.URL, "003-2021-87dad215f30f1f7e"),
	}
	if err := d.login(); err != nil {
		t.Fatalf("login: %v", err)
	}
	list, err := d.devices()
	if err != nil {
		t.Fatalf("devices: %v", err)
	}
	if len(list) != 1 || list[0].ID != "bell1" || list[0].Name != "Portal" {
		t.Fatalf("dispositivos: %+v", list)
	}

	before := len(g.commands)
	if err := d.login(); err != nil {
		t.Fatalf("segundo login: %v", err)
	}
	if len(g.commands) != before {
		t.Fatalf("el segundo login volvio a hablar con el gateway: %v", g.commands)
	}
}

func TestDaemonLoginNeedsCredentials(t *testing.T) {
	d := &daemon{cfg: config{}, auth: NewUserAuth("http://127.0.0.1:1", "c")}
	if err := d.login(); err == nil {
		t.Fatal("sin credenciales el login debe fallar")
	}
}
