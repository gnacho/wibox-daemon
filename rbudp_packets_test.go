package main

import (
	"bytes"
	"testing"
)

func TestRbControlRoundTrip(t *testing.T) {
	built := buildRbControl(0x11111111, 0x22222222, 0x33333333, 0x44444444, 0xFFFF4900)
	if len(built) != 28 {
		t.Fatalf("control de %d bytes", len(built))
	}
	got, err := parseRbControl(built)
	if err != nil {
		t.Fatalf("parseRbControl: %v", err)
	}
	if got.Word4 != 0x11111111 || got.Word8 != 0x22222222 {
		t.Fatalf("palabras = %+v", got)
	}
	if got.LocalID != 0x33333333 || got.RemoteID != 0x44444444 {
		t.Fatalf("ids = %+v", got)
	}
	if got.StatusWord != 0xFFFF4900 {
		t.Fatalf("status = 0x%08x", got.StatusWord)
	}
	if _, err := parseRbControl(built[:20]); err == nil {
		t.Fatal("un control corto deberia dar error")
	}
}

func TestNativeChecksumIsStableAndSensitive(t *testing.T) {
	pkt := buildRbControl(1, 2, 3, 4, 0xFFFF0900)
	if got, again := nativeChecksum16(pkt), nativeChecksum16(pkt); got != again {
		t.Fatalf("checksum inestable: 0x%04x != 0x%04x", got, again)
	}
	// El byte de checksum es el que es; si se toca el status, cambia.
	other := buildRbControl(1, 2, 3, 5, 0xFFFF0900)
	if nativeChecksum16(pkt) == nativeChecksum16(other) {
		t.Fatal("dos paquetes distintos no deberian compartir checksum")
	}
	// El propio paquete lleva el checksum en 0x18 y debe cuadrar.
	if readU16(pkt, 0x18) != nativeChecksum16(pkt) {
		t.Fatal("el checksum guardado no coincide con el calculado")
	}
}

func TestRbWrappedCarriesTheInnerPacket(t *testing.T) {
	inner := buildRbData([]byte("hola"), rbSrcIDBase, 0x0B030000, 7)
	wrapped := buildRbWrapped(inner, 0xAAAAAAAA, 0xBBBBBBBB, 0xCCCCCCCC, 0xDDDDDDDD, rbWrappedStatus, rbDataTag)
	if got := readU32(wrapped, 0x1C); got != 0xFFFFFFFF {
		t.Fatalf("marca directa = 0x%08x", got)
	}
	out, tag, err := parseRbWrapped(wrapped)
	if err != nil {
		t.Fatalf("parseRbWrapped: %v", err)
	}
	if len(out) == 0 {
		t.Fatal("el inner no puede salir vacio")
	}
	// Un wrapped construido por nosotros cae en la rama directa: no arrastra
	// tag, aunque el constructor lo meta en el paquete.
	if len(tag) != 0 {
		t.Fatalf("tag = % x, la rama directa no lo recupera", tag)
	}
	if _, _, err := parseRbWrapped(wrapped[:8]); err == nil {
		t.Fatal("un wrapped corto deberia dar error")
	}
}

func TestKcpConnectAndResponse(t *testing.T) {
	connect := buildKcpConnect(rbSrcIDBase, 3, 0)
	if len(connect) != 0x4C {
		t.Fatalf("connect de %d bytes", len(connect))
	}
	if got := readU32(connect, 0x14); got != rbCmdConnect {
		t.Fatalf("command = 0x%08x", got)
	}
	if got := connect[0x40]; got != byte((3<<4)|0) {
		t.Fatalf("canal/connType = 0x%02x", got)
	}
	if _, err := parseKcpConnectResponse(connect[:8]); err == nil {
		t.Fatal("una respuesta corta deberia dar error")
	}

	// Respuesta larga: el dispositivo asigna connectID y destID.
	long := make([]byte, 0x4C)
	putU32(long, 0x04, 0x4C)
	long[0x12] = 1
	putU32(long, 0x14, rbCmdConnect)
	putU32(long, 0x28, rbSrcIDBase)
	putU32(long, 0x4C-4, 0x0B030000)
	resp, err := parseKcpConnectResponse(long)
	if err != nil {
		t.Fatalf("parse largo: %v", err)
	}
	if resp.ConnectID != rbSrcIDBase || resp.DestID != 0x0B030000 {
		t.Fatalf("respuesta larga = %+v", resp)
	}
	if resp.TypeFlag != 1 || resp.Command != rbCmdConnect {
		t.Fatalf("respuesta larga = %+v", resp)
	}
}

func TestRbDataRoundTrip(t *testing.T) {
	payload := []byte("0123456789abcdef")
	pkt := buildRbData(payload, rbSrcIDBase, 0x0B030000, 42)
	if got := readU32(pkt, 0x14); got != rbCmdData {
		t.Fatalf("command = 0x%08x", got)
	}
	parsed, err := parseRbData(pkt)
	if err != nil {
		t.Fatalf("parseRbData: %v", err)
	}
	if parsed.Seq != 42 {
		t.Fatalf("seq = %d", parsed.Seq)
	}
	if parsed.SrcID != rbSrcIDBase || parsed.DestID != 0x0B030000 {
		t.Fatalf("ids = %+v", parsed)
	}
	if !bytes.Equal(parsed.Payload, payload) {
		t.Fatalf("payload = % x", parsed.Payload)
	}
	if isRbDataAck(pkt) {
		t.Fatal("un paquete de datos no es un ack")
	}
	if _, err := parseRbData(pkt[:0x20]); err == nil {
		t.Fatal("un paquete corto deberia dar error")
	}
}

func TestRbDataAckRoundTrip(t *testing.T) {
	ack := buildRbDataAck(320, 7, 0x12345678, 0x9ABCDEF0)
	if len(ack) != 0x38 {
		t.Fatalf("ack de %d bytes", len(ack))
	}
	if !isRbDataAck(ack) {
		t.Fatal("deberia reconocerse como ack")
	}
	parsed, err := parseRbData(ack)
	if err != nil {
		t.Fatalf("parseRbData(ack): %v", err)
	}
	// El campo de longitud de la cabecera es el tamano del cuerpo (0x10) y el
	// payload reconocido va en 0x30, que es lo que el emisor rellena con la
	// longitud del paquete al que responde.
	if parsed.PayloadLength != 0x10 || parsed.Seq != 7 {
		t.Fatalf("ack = %+v", parsed)
	}
	if got := readU32(ack, 0x30); got != 320 {
		t.Fatalf("longitud reconocida = %d", got)
	}
	if parsed.SrcID != 0x12345678 || parsed.DestID != 0x9ABCDEF0 {
		t.Fatalf("ids del ack = %+v", parsed)
	}
	if isRbDataAck(ack[:4]) {
		t.Fatal("cuatro bytes no son un ack")
	}
}

func TestKcpDisconnect(t *testing.T) {
	pkt := buildKcpDisconnect(rbSrcIDBase, 0x0B030000)
	if len(pkt) != 0x30 {
		t.Fatalf("disconnect de %d bytes", len(pkt))
	}
	if got := readU32(pkt, 0x14); got != rbCmdDisconnect {
		t.Fatalf("command = 0x%08x", got)
	}
	if got := readU32(pkt, 0x28); got != 0x0B030000 {
		t.Fatalf("dest = 0x%08x", got)
	}
	if got := readU32(pkt, 0x2C); got != rbSrcIDBase {
		t.Fatalf("src = 0x%08x", got)
	}
}

func TestMustHex(t *testing.T) {
	got := mustHex("696f6e3a3a4f7065")
	want := []byte("ion::Ope")
	if !bytes.Equal(got, want) {
		t.Fatalf("mustHex = % x", got)
	}
}
