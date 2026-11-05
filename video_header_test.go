package main

import (
	"bytes"
	"path/filepath"
	"testing"
	"time"
)

// La cabecera del MPEG-TS tiene que salir ANTES de conectar con el timbre:
// quien valida el stream (el config flow de Home Assistant) se rinde en
// segundos y el p2pconnect con el timbre dormido tarda decenas. Este test
// apunta el cloud a un puerto cerrado, asi que el tunel nunca se monta, y
// comprueba que aun asi hay bytes a la salida.
func TestStreamHeaderIsWrittenBeforeConnecting(t *testing.T) {
	// La cache de servicios es global: si otra prueba la dejo puesta, el
	// discovery se saltaria y esto se iria a la red real.
	servicesMu.Lock()
	servicesVal = nil
	servicesMu.Unlock()

	t.Setenv("WIBOX_MPEGTS", "1")

	cfg := config{
		baseURL:        "http://127.0.0.1:1",
		apiPort:        8087,
		channel:        1,
		stream:         2,
		remoteAttempts: 1,
		remotePoll:     time.Millisecond,
		remoteBudget:   200 * time.Millisecond,
		videoLock:      filepath.Join(t.TempDir(), "video.lock"),
	}

	var out bytes.Buffer
	err := runVideo(cfg, "sesion", bytes.Repeat([]byte{7}, 32), "clave", &out, nil)
	if err == nil {
		t.Fatal("sin tunel runVideo deberia devolver error")
	}
	if out.Len() == 0 {
		t.Fatal("no escribio ningun byte antes de conectar")
	}
	if first := out.Bytes()[0]; first != 0x47 {
		t.Fatalf("el primer byte no es un paquete TS: %#x", first)
	}
	if out.Len() < 188*3 {
		t.Fatalf("solo %d bytes: faltan PAT, PMT y el PES de parametros", out.Len())
	}
}
