package main

import (
	"testing"
	"time"
)

// connectFrame arma la respuesta de KCP connect tal como la manda el timbre:
// paquete largo con el identificador de destino en el ultimo campo.
func connectFrame(dest uint32) []byte {
	f := make([]byte, 0x4C)
	putU32(f, 0x00, 0xFFFFFFFF)
	putU32(f, 0x04, 0x4C)
	putU32(f, 0x10, 0)
	f[0x12] = 1
	putU32(f, 0x14, rbCmdConnect)
	putU32(f, 0x24, 0x38)
	putU32(f, 0x28, rbSrcIDBase)
	putU32(f, 0x4C-4, dest)
	return f
}

func TestTunnelLearnsTheQuiiDestination(t *testing.T) {
	tr, _ := newTestTunnel(t)
	tr.key = make([]byte, 32)
	tr.password = "clave"
	frame := connectFrame(0x0A0B0C0D)

	raw := make([]byte, 0x1C+len(frame))
	putU32(raw, 0x00, rbMarker)
	putU32(raw, 0x0C, 100)
	putU32(raw, 0x14, 0x00001900)
	copy(raw[0x1C:], frame)
	feedDatagram(tr, raw)

	if tr.quiiDestID != 0x0A0B0C0D {
		t.Fatalf("destino aprendido = 0x%08x", tr.quiiDestID)
	}
	if !tr.quiiSetupSent {
		t.Fatal("no se marco el setup como enviado")
	}
}

func TestTunnelWrappedDataReachesThePlayer(t *testing.T) {
	tr, _ := newTestTunnel(t)
	tr.key = make([]byte, 32)
	tr.quiiPlaySent = true

	inner := buildRbData([]byte{0x00, 0x00, 0x01, 0xE0, 0x42}, rbSrcIDBase, 0x04000006, 5)
	raw := make([]byte, 0x1C+len(inner))
	putU32(raw, 0x00, rbMarker)
	putU32(raw, 0x1C, 0xFFFFFFFF)
	putU32(raw, 0x20, uint32(len(inner)))
	putU32(raw, 0x14, 0x00000100)
	copy(raw[0x1C:], inner)

	feedDatagram(tr, raw)

	select {
	case got := <-tr.incoming():
		if len(got) == 0 {
			t.Fatal("payload vacio")
		}
	case <-time.After(500 * time.Millisecond):
		t.Fatal("el paquete envuelto no llego al reproductor")
	}
}
