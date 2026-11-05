package main

import (
	"net"
	"testing"
	"time"
)

func readBellDatagram(t *testing.T, bell *net.UDPConn, timeout time.Duration) []byte {
	t.Helper()
	bell.SetReadDeadline(time.Now().Add(timeout))
	buf := make([]byte, 8192)
	n, _, err := bell.ReadFromUDP(buf)
	if err != nil {
		t.Fatalf("el timbre falso no recibio nada: %v", err)
	}
	return buf[:n]
}

func TestTunnelWrapsQuiiPayloads(t *testing.T) {
	tr, bell := newTestTunnel(t)
	tr.key = make([]byte, 32)
	tr.password = "clave"
	tr.quiiDestID = 0x04000006
	tr.quiiNextSeq = 2

	if err := tr.sendQuii([]byte("hola")); err != nil {
		t.Fatalf("sendQuii: %v", err)
	}
	raw := readBellDatagram(t, bell, time.Second)
	if got := readU32(raw, 0x00); got != rbMarker {
		t.Fatalf("marcador=0x%08x", got)
	}
	if got := readU32(raw, 0x14); got != rbWrappedStatus {
		t.Fatalf("status=0x%08x, se esperaba el de wrapped", got)
	}
	if got := readU32(raw, 0x1C); got != 0xFFFFFFFF {
		t.Fatalf("marca de la rama directa=0x%08x", got)
	}
	inner, _, err := parseRbWrapped(raw)
	if err != nil {
		t.Fatalf("parseRbWrapped: %v", err)
	}
	data, err := parseRbData(inner)
	if err != nil {
		t.Fatalf("parseRbData: %v", err)
	}
	if data.Seq != 2 {
		t.Fatalf("seq=%d, se esperaba 2", data.Seq)
	}
	if string(data.Payload) != "hola" {
		t.Fatalf("payload=%q", string(data.Payload))
	}
	if data.DestID != 0x04000006 {
		t.Fatalf("destino=0x%08x", data.DestID)
	}
}

func TestTunnelSendsDataAck(t *testing.T) {
	tr, bell := newTestTunnel(t)
	tr.laneLocalID = 10
	tr.laneRemoteID = 20
	tr.sendDataAck(320, 7)
	raw := readBellDatagram(t, bell, time.Second)
	inner, _, err := parseRbWrapped(raw)
	if err != nil {
		t.Fatalf("parseRbWrapped: %v", err)
	}
	if !isRbDataAck(inner) {
		t.Fatal("no lleva la marca de ack")
	}
	ack, err := parseRbData(inner)
	if err != nil {
		t.Fatalf("parseRbData: %v", err)
	}
	if ack.Seq != 7 || readU32(inner, 0x30) != 320 {
		t.Fatalf("seq=%d longitud=%d", ack.Seq, readU32(inner, 0x30))
	}
}

func TestTunnelAwaitQuiiDestination(t *testing.T) {
	tr, _ := newTestTunnel(t)
	tr.quiiDestID = 42
	got, err := tr.awaitQuiiDestID(200 * time.Millisecond)
	if err != nil {
		t.Fatalf("awaitQuiiDestID: %v", err)
	}
	if got != 42 {
		t.Fatalf("destino=%d", got)
	}
	other, _ := newTestTunnel(t)
	if _, err := other.awaitQuiiDestID(50 * time.Millisecond); err == nil {
		t.Fatal("sin destino deberia devolver error")
	}
}
