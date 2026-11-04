package main

import (
	"testing"
)

// Fixture sintetico: el servidor natcheck devuelve el mapeo externo del cliente.
// Se usan direcciones de documentacion (RFC 5737) para no publicar IPs reales.
func buildNatcheckReply(ip string, port int) []byte {
	pkt := make([]byte, natcheckQuerySize)
	putU32(pkt, 0x00, p2pMarker)
	putU16(pkt, 0x1A, uint16(natcheckQuerySize))
	putU32(pkt, 0x1C, 0xFFFFFFFF)
	putU32(pkt, 0x20, 0x54)
	pkt[0x2D] = 0x01
	pkt[0x2E] = 0x01
	putU32(pkt, 0x48, 1)
	copy(pkt[0x4C:], ip)
	putU16(pkt, 0x5C, uint16(port))
	putU16(pkt, 0x5E, 8300)
	copy(pkt[0x60:], "203.0.113.9")
	return pkt
}

func TestBuildNatcheckQueryHasMappingFlag(t *testing.T) {
	q := buildNatcheckQuery()
	if len(q) != natcheckQuerySize {
		t.Fatalf("tamano %d, se esperaban %d", len(q), natcheckQuerySize)
	}
	if got := readU32(q, 0x00); got != p2pMarker {
		t.Fatalf("marcador 0x%08x", got)
	}
	if q[0x2D] != 0x01 {
		t.Fatalf("falta el flag de mapeo en 0x2D: 0x%02x", q[0x2D])
	}
	if got := readU16(q, 0x1A); int(got) != natcheckQuerySize {
		t.Fatalf("longitud @0x1A = %d", got)
	}
}

func TestParseNatcheckResponseExtractsMapping(t *testing.T) {
	ip, port, ok := parseNatcheckResponse(buildNatcheckReply("198.51.100.7", 40000))
	if !ok {
		t.Fatal("respuesta valida rechazada")
	}
	if ip != "198.51.100.7" {
		t.Fatalf("ip=%q", ip)
	}
	if port != 40000 {
		t.Fatalf("puerto=%d", port)
	}
}

func TestParseNatcheckResponseRejectsGarbage(t *testing.T) {
	if _, _, ok := parseNatcheckResponse([]byte("CAUTHNETClient")); ok {
		t.Fatal("texto plano aceptado como respuesta natcheck")
	}
	short := buildNatcheckReply("198.51.100.7", 40000)[:64]
	if _, _, ok := parseNatcheckResponse(short); ok {
		t.Fatal("respuesta truncada aceptada")
	}
	empty := buildNatcheckReply("198.51.100.7", 0)
	if _, _, ok := parseNatcheckResponse(empty); ok {
		t.Fatal("puerto 0 aceptado como mapeo")
	}
}
