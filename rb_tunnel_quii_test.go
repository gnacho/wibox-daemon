package main

import (
	"bytes"
	"testing"
	"time"
)

// buildRbWrapped mete el tag en 0x24 y el cuerpo del frame a partir de 0x2C,
// asi que el payload de datos empieza en 0x54. No se usa parseRbWrapped porque
// un paquete construido por nosotros cae en su rama "direct" y devuelve el
// interior desplazado.
func wrappedQuiiPayload(t *testing.T, datagram []byte, key []byte) *packet {
	t.Helper()
	if len(datagram) < 0x60 {
		t.Fatalf("datagrama demasiado corto: %d", len(datagram))
	}
	payload := datagram[0x54:]
	pkt, used, err := decodeQuiiPacket(key, payload)
	if err != nil {
		t.Fatalf("quii: %v", err)
	}
	if used > len(payload) {
		t.Fatalf("se consumieron %d bytes de %d", used, len(payload))
	}
	return pkt
}

func TestTunnelSendsTheQuiiSetup(t *testing.T) {
	tr, bell := newTestTunnel(t)
	tr.quiiDestID = 0x04000006
	if err := tr.sendQuiiSetup(); err != nil {
		t.Fatalf("setup: %v", err)
	}
	// El setup va EN CLARO dentro del tunel: 32 bytes con 0xA9 delante, sin
	// cifrado QUII. Es lo que el timbre espera antes de aceptar el play.
	datagram := readBellDatagram(t, bell, time.Second)
	setup := datagram[0x54:]
	if len(setup) != 32 || setup[0] != 0xA9 {
		t.Fatalf("setup inesperado (%d bytes): % x", len(setup), setup)
	}
}

func TestTunnelSendsThePlayBody(t *testing.T) {
	tr, bell := newTestTunnel(t)
	tr.quiiDestID = 0x04000006
	tr.password = "secreto"
	if err := tr.sendQuiiPlay(); err != nil {
		t.Fatalf("play: %v", err)
	}
	pkt := wrappedQuiiPayload(t, readBellDatagram(t, bell, time.Second), tr.key)
	if pkt.Type != 0x01 {
		t.Fatalf("tipo %#x, se esperaba play", pkt.Type)
	}
	if pkt.B13 != 1 || pkt.B14 != 0 {
		t.Fatalf("canal %d/%d", pkt.B13, pkt.B14)
	}
	want := []byte("adminapp&&secreto\x00/mode=real&idc=1&ids=2&ap=2\x00")
	if !bytes.Equal(pkt.Command, want) {
		t.Fatalf("cuerpo del play:\n%q\nse esperaba\n%q", pkt.Command, want)
	}
	if !tr.quiiPlaySent {
		t.Fatal("no se marco el play como enviado")
	}
}

func TestTunnelSendsTheKeepalive(t *testing.T) {
	tr, bell := newTestTunnel(t)
	tr.quiiDestID = 0x04000006
	if err := tr.sendQuiiKeepalive(); err != nil {
		t.Fatalf("keepalive: %v", err)
	}
	datagram := readBellDatagram(t, bell, time.Second)
	pkt := wrappedQuiiPayload(t, datagram, tr.key)
	if pkt.Type != 0x00 {
		t.Fatalf("tipo %#x, se esperaba keepalive", pkt.Type)
	}
	if len(datagram) != 0x54+64 {
		t.Fatalf("keepalive de %d bytes, se esperaban 32 de cabecera y 32 de digest", len(datagram)-0x54)
	}
}
