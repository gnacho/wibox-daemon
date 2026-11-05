package main

import (
	"strings"
	"testing"
	"time"
)

// El tunel entrega el setup QUII como un flujo: puede llegar en un trozo o en
// varios. waitQuiiSetup solo junta 32 bytes; quien lo llama valida el
// contenido, asi que aqui se comprueba el troceado y los dos cortes.
func TestWaitQuiiSetupJoinsChunks(t *testing.T) {
	in := make(chan []byte, 2)
	setup := make([]byte, 32)
	setup[0] = 0xA9
	setup[9] = 0
	setup[10] = 2
	setup[11] = 1
	in <- setup[:10]
	in <- setup[10:]

	got, err := waitQuiiSetup(in, time.Second)
	if err != nil {
		t.Fatalf("waitQuiiSetup: %v", err)
	}
	if len(got) != 32 || got[0] != 0xA9 {
		t.Fatalf("setup devuelto raro: % x", got)
	}
}

func TestWaitQuiiSetupFailsWhenTheTunnelCloses(t *testing.T) {
	in := make(chan []byte)
	close(in)

	_, err := waitQuiiSetup(in, time.Second)
	if err == nil || !strings.Contains(err.Error(), "se cerro") {
		t.Fatalf("error esperado de tunel cerrado, obtuve %v", err)
	}
}

func TestWaitQuiiSetupTimesOut(t *testing.T) {
	in := make(chan []byte)
	_, err := waitQuiiSetup(in, 20*time.Millisecond)
	if err == nil || !strings.Contains(err.Error(), "timeout") {
		t.Fatalf("error esperado de timeout, obtuve %v", err)
	}
}
