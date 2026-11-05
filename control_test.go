package main

import (
	"encoding/json"
	"net"
	"os"
	"path/filepath"
	"testing"
	"time"
)

// El canal de apertura existe porque el timbre solo admite una sesion a la
// vez: mientras go2rtc tiene el video abierto, la API pide la apertura por
// aqui en vez de montar otro tunel.

func testSockPath(t *testing.T) string {
	t.Helper()
	return filepath.Join(t.TempDir(), "ctl.sock")
}

func TestControlServerUnlocks(t *testing.T) {
	path := testSockPath(t)
	called := 0
	srv, err := newControlServer(path, func() (int, error) {
		called++
		return 0, nil
	})
	if err != nil {
		t.Fatalf("servidor: %v", err)
	}
	defer srv.close()

	rc, handled, err := controlUnlock(path)
	if err != nil {
		t.Fatalf("apertura: %v", err)
	}
	if !handled {
		t.Fatal("debía haber atendido el socket")
	}
	if rc != 0 {
		t.Fatalf("rc=%d", rc)
	}
	if called != 1 {
		t.Fatalf("la apertura se ejecuto %d veces", called)
	}
}

func TestControlServerReportsRejection(t *testing.T) {
	path := testSockPath(t)
	srv, err := newControlServer(path, func() (int, error) {
		return -10029, errPinRejected
	})
	if err != nil {
		t.Fatalf("servidor: %v", err)
	}
	defer srv.close()

	rc, handled, err := controlUnlock(path)
	if !handled {
		t.Fatal("debía haber atendido el socket")
	}
	if err == nil {
		t.Fatal("esperaba error de rechazo")
	}
	if rc != -10029 {
		t.Fatalf("rc=%d", rc)
	}
}

func TestControlUnlockWithoutServer(t *testing.T) {
	rc, handled, err := controlUnlock(testSockPath(t))
	if handled {
		t.Fatal("sin servidor no debe darse por atendido")
	}
	if err != nil || rc != -1 {
		t.Fatalf("rc=%d err=%v", rc, err)
	}
}

func TestControlServerReplacesStaleSocket(t *testing.T) {
	path := testSockPath(t)
	if err := os.WriteFile(path, nil, 0o600); err != nil {
		t.Fatalf("socket viejo: %v", err)
	}
	srv, err := newControlServer(path, func() (int, error) { return 0, nil })
	if err != nil {
		t.Fatalf("debía sustituir el socket huerfano: %v", err)
	}
	defer srv.close()

	if _, _, err := controlUnlock(path); err != nil {
		t.Fatalf("apertura: %v", err)
	}
}

func TestControlServerRejectsUnknownCommand(t *testing.T) {
	path := testSockPath(t)
	srv, err := newControlServer(path, func() (int, error) { return 0, nil })
	if err != nil {
		t.Fatalf("servidor: %v", err)
	}
	defer srv.close()

	conn, err := net.Dial("unix", path)
	if err != nil {
		t.Fatalf("dial: %v", err)
	}
	defer conn.Close()
	conn.SetDeadline(time.Now().Add(5 * time.Second))
	if err := json.NewEncoder(conn).Encode(controlRequest{Command: "reboot"}); err != nil {
		t.Fatalf("envio: %v", err)
	}
	var resp controlResponse
	if err := json.NewDecoder(conn).Decode(&resp); err != nil {
		t.Fatalf("respuesta: %v", err)
	}
	if resp.OK || resp.Error == "" {
		t.Fatalf("respuesta inesperada: %+v", resp)
	}
}

var errPinRejected = &pinRejectedError{}

type pinRejectedError struct{}

func (e *pinRejectedError) Error() string { return "contrasena de apertura rechazada" }
