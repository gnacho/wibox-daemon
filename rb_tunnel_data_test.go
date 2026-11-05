package main

import (
	"net"
	"testing"
	"time"
)

// newTestTunnel monta un tunel contra un socket local que hace de timbre y
// devuelve ambos extremos para poder inyectar datagramas a mano.
func newTestTunnel(t *testing.T) (*rbTunnel, *net.UDPConn) {
	t.Helper()
	bell, err := net.ListenUDP("udp4", &net.UDPAddr{IP: net.IPv4(127, 0, 0, 1)})
	if err != nil {
		t.Fatalf("socket del timbre: %v", err)
	}
	t.Cleanup(func() { bell.Close() })
	conn, err := net.ListenUDP("udp4", &net.UDPAddr{IP: net.IPv4(127, 0, 0, 1)})
	if err != nil {
		t.Fatalf("socket local: %v", err)
	}
	t.Cleanup(func() { conn.Close() })

	key := make([]byte, 32)
	for i := range key {
		key[i] = byte(i)
	}
	tr := newRbTunnel(conn, bell.LocalAddr().(*net.UDPAddr), "sesion", key, "clave", 1, 2)
	// Sin cerrar el tunel, las gorutinas que arrancan algunos tests (el play
	// sync) siguen escribiendo campos mientras corre el resto de la suite, y
	// el detector de carreras lo canta.
	t.Cleanup(func() { tr.close() })
	return tr, bell
}

func feedDatagram(tr *rbTunnel, raw []byte) {
	tr.mu.Lock()
	tr.handleDatagram(raw)
	tr.mu.Unlock()
}

func expectPayload(t *testing.T, tr *rbTunnel, want []byte) {
	t.Helper()
	select {
	case got := <-tr.incoming():
		if len(got) != len(want) {
			t.Fatalf("payload de %d bytes, se esperaban %d", len(got), len(want))
		}
		for i := range want {
			if got[i] != want[i] {
				t.Fatalf("payload byte %d = %02x, se esperaba %02x", i, got[i], want[i])
			}
		}
	case <-time.After(500 * time.Millisecond):
		t.Fatal("el tunel no emitio el payload")
	}
}

// Un datagrama de stream entrega el payload tal cual cuando ya se envio el play.
func TestTunnelEmitsStreamPayloadAfterPlay(t *testing.T) {
	tr, _ := newTestTunnel(t)
	tr.quiiPlaySent = true

	payload := []byte{0x00, 0x00, 0x01, 0xE0, 0x11, 0x22}
	raw := make([]byte, 0x1C+len(payload))
	putU32(raw, 0x00, rbMarker)
	putU32(raw, 0x14, 0x00000500)
	copy(raw[0x1C:], payload)

	feedDatagram(tr, raw)
	expectPayload(t, tr, payload)
}

// Antes del play el stream no se debe adelantar: el timbre todavia no ha
// aceptado la sesion y lo que llega no es imagen util.
func TestTunnelDropsStreamBeforePlay(t *testing.T) {
	tr, _ := newTestTunnel(t)

	raw := make([]byte, 0x1C+4)
	putU32(raw, 0x00, rbMarker)
	putU32(raw, 0x14, 0x00000500)
	copy(raw[0x1C:], []byte{0x00, 0x00, 0x01, 0xE0})

	feedDatagram(tr, raw)
	select {
	case got := <-tr.incoming():
		t.Fatalf("no deberia emitir antes del play: % x", got)
	case <-time.After(100 * time.Millisecond):
	}
}

// Un datagrama con un frame KCP de datos dentro se enmarca y se emite.
func TestTunnelFramesDataPayload(t *testing.T) {
	tr, _ := newTestTunnel(t)
	tr.quiiPlaySent = true

	payload := []byte{0x00, 0x00, 0x01, 0xE1, 0x33}
	frame := buildRbData(payload, rbSrcIDBase, 0x04000006, 5)

	raw := make([]byte, 0x1C+len(frame))
	putU32(raw, 0x00, rbMarker)
	putU32(raw, 0x0C, 100)
	putU32(raw, 0x14, 0x00001900)
	copy(raw[0x1C:], frame)

	feedDatagram(tr, raw)
	expectPayload(t, tr, payload)
}

// Un hueco en los identificadores resincroniza en vez de quedarse mudo.
func TestTunnelResyncsOnGap(t *testing.T) {
	tr, _ := newTestTunnel(t)
	tr.quiiPlaySent = true

	makeDatagram := func(localID uint32, payload []byte) []byte {
		frame := buildRbData(payload, rbSrcIDBase, 0x04000006, 7)
		raw := make([]byte, 0x1C+len(frame))
		putU32(raw, 0x00, rbMarker)
		putU32(raw, 0x0C, localID)
		putU32(raw, 0x14, 0x00001900)
		copy(raw[0x1C:], frame)
		return raw
	}

	first := []byte{0x00, 0x00, 0x01, 0xE0, 0x01}
	feedDatagram(tr, makeDatagram(100, first))
	expectPayload(t, tr, first)

	// El segundo salta de identificador: el tunel tiene que limpiar el buffer
	// y aceptar el que llega.
	second := []byte{0x00, 0x00, 0x01, 0xE0, 0x02}
	feedDatagram(tr, makeDatagram(900, second))
	expectPayload(t, tr, second)

	tr.mu.Lock()
	defer tr.mu.Unlock()
	if tr.rxStreamNextRemoteID != 900+uint32(len(buildRbData(second, rbSrcIDBase, 0x04000006, 7))) {
		t.Fatalf("identificador siguiente = %d, se esperaba el del ultimo datagrama", tr.rxStreamNextRemoteID)
	}
}

// Un control de POST_CONNECT fija el identificador logico del timbre.
func TestTunnelDispatchControlPostConnect(t *testing.T) {
	tr, _ := newTestTunnel(t)

	raw := buildRbControl(0, 0, 0, 0, ctrlPostConnect)
	putU32(raw, 0x04, 0x1234)

	feedDatagram(tr, raw)

	tr.mu.Lock()
	defer tr.mu.Unlock()
	if tr.peerLogicID != 0x1234 {
		t.Fatalf("peerLogicID = %#x, se esperaba 0x1234", tr.peerLogicID)
	}
}

// Un frame de sonda (164 bytes) que llega como wrapped se descarta.
func TestTunnelIgnoresProbeFrames(t *testing.T) {
	tr, _ := newTestTunnel(t)
	tr.quiiPlaySent = true

	raw := make([]byte, p2pFrameSize)
	putU32(raw, 0x00, rbMarker)
	putU32(raw, 0x14, 0x00001900)

	feedDatagram(tr, raw)
	select {
	case got := <-tr.incoming():
		t.Fatalf("no deberia emitir un frame de sonda: %d bytes", len(got))
	case <-time.After(100 * time.Millisecond):
	}
}
