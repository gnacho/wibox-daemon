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

// go2rtc sondea el pipe esperando un paquete de cada tipo que anuncie el PMT.
// Declarar la pista de audio y no mandar ni un paquete suyo deja el sondeo
// colgado, y con el su RTSP no contesta ni un DESCRIBE hasta que llega media
// real del timbre. La cabecera tiene que llevar ya el silencio de audio.
func TestStreamHeaderCarriesBothTracks(t *testing.T) {
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
	if err := runVideo(cfg, "sesion", bytes.Repeat([]byte{7}, 32), "clave", &out, nil); err == nil {
		t.Fatal("sin tunel runVideo deberia devolver error")
	}

	seen := map[uint16]int{}
	buf := out.Bytes()
	for i := 0; i+188 <= len(buf); i += 188 {
		if buf[i] != 0x47 {
			t.Fatalf("paquete TS desalineado en el byte %d", i)
		}
		pid := uint16(buf[i+1]&0x1F)<<8 | uint16(buf[i+2])
		seen[pid]++
	}
	for _, want := range []struct {
		pid  uint16
		name string
	}{
		{tsPIDPat, "PAT"},
		{tsPIDPmt, "PMT"},
		{tsPIDVideo, "video"},
		{tsPIDAudio, "audio"},
	} {
		if seen[want.pid] == 0 {
			t.Fatalf("la cabecera no lleva ningun paquete de %s (pid %#x)", want.name, want.pid)
		}
	}
}
