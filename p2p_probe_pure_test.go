package main

import (
	"strings"
	"testing"
)

func TestLittleEndianHelpersRoundTrip(t *testing.T) {
	buf := make([]byte, 8)
	putU32(buf, 0, 0xDEADBEEF)
	if got := readU32(buf, 0); got != 0xDEADBEEF {
		t.Fatalf("u32 ida y vuelta = %#x", got)
	}
	putU16(buf, 4, 0x1234)
	if got := readU16(buf, 4); got != 0x1234 {
		t.Fatalf("u16 ida y vuelta = %#x", got)
	}
	if buf[0] != 0xEF || buf[4] != 0x34 {
		t.Fatalf("el orden no es little endian: % x", buf)
	}
}

func TestCStringHelpersStopAtZeroAndMaximum(t *testing.T) {
	buf := make([]byte, 16)
	putCString(buf, 0, 16, "ion::Ope")
	if got := readCString(buf, 0, 16); got != "ion::Ope" {
		t.Fatalf("cstring = %q", got)
	}
	// Un texto mas largo que el hueco se recorta en el maximo. El buffer se
	// limpia antes porque putCString no escribe cero de cierre.
	for i := range buf {
		buf[i] = 0
	}
	// Con un hueco de 4 cabe el texto y el cero, pero la lectura pide 4 bytes
	// y se para en el cero: quedan tres caracteres.
	putCString(buf, 0, 4, "abcdef")
	if got := readCString(buf, 0, 4); got != "abc" {
		t.Fatalf("cstring recortada = %q", got)
	}
	// Sin cero de cierre la lectura se para en el maximo.
	raw := []byte("abcdefgh")
	if got := readCString(raw, 0, 3); got != "abc" {
		t.Fatalf("cstring sin cero = %q", got)
	}
	if got := readCString(raw, 6, 8); got != "gh" {
		t.Fatalf("cstring al final = %q", got)
	}
}

func TestProbeSeqForMapsEachKind(t *testing.T) {
	if probeSeqFor("lan") != p2pProbeSeqLAN {
		t.Fatal("lan")
	}
	if probeSeqFor("p2p") != p2pProbeSeqP2P {
		t.Fatal("p2p")
	}
	if probeSeqFor("trans") != p2pProbeSeqTrans {
		t.Fatal("trans")
	}
	// Cualquier otro nombre cae en trans, que es el camino del fabricante.
	if probeSeqFor("otra") != p2pProbeSeqTrans {
		t.Fatal("desconocido deberia caer en trans")
	}
}

func TestIsPrivateIPAndRank(t *testing.T) {
	privadas := []string{"10.1.2.3", "172.16.0.1", "172.31.255.254", "192.168.1.5"}
	for _, ip := range privadas {
		if !isPrivateIP(ip) {
			t.Fatalf("%s deberia ser privada", ip)
		}
		if got := rankOf(ip); got != 3 {
			t.Fatalf("rank de %s = %d, se esperaba 3", ip, got)
		}
	}
	publicas := []string{"8.8.8.8", "86.127.231.201", "172.32.0.1"}
	for _, ip := range publicas {
		if isPrivateIP(ip) {
			t.Fatalf("%s no es privada", ip)
		}
		if got := rankOf(ip); got != 1 {
			t.Fatalf("rank de %s = %d, se esperaba 1", ip, got)
		}
	}
	if rankOf("127.0.0.1") != 2 {
		t.Fatal("loopback deberia valer 2")
	}
	if rankOf("no-es-ip") != 1 {
		t.Fatal("una cadena que no es IP deberia valer 1")
	}
}

func TestFlagMatchesAcceptsTruncation(t *testing.T) {
	full := strings.Repeat("a", 63) + "1"
	truncated := strings.Repeat("a", 63)
	if !flagMatches(full, truncated) {
		t.Fatal("el servidor trunca el flag a 63: debe casar por prefijo")
	}
	if !flagMatches(truncated, full) {
		t.Fatal("el prefijo tambien tiene que casar en el otro sentido")
	}
	if !flagMatches("abc", "abc") {
		t.Fatal("iguales")
	}
	if flagMatches("abc", "abd") {
		t.Fatal("distintos no pueden casar")
	}
	if flagMatches("", "abc") || flagMatches("abc", "") {
		t.Fatal("vacio nunca casa")
	}
}

func TestP2pTestPacketRoundTrip(t *testing.T) {
	flag := strings.Repeat("b", 64)
	pkt := buildP2pTestPacket(p2pProbeSeqP2P, flag, 1234, 54450, "86.127.231.201", 54450, 0)
	if len(pkt) != p2pFrameSize {
		t.Fatalf("frame de %d bytes, se esperaban %d", len(pkt), p2pFrameSize)
	}
	if got := readU32(pkt, 0); got != p2pMarker {
		t.Fatalf("marcador = %#x", got)
	}
	resp, ok := parseP2pTestResponse(pkt)
	if !ok {
		t.Fatal("la respuesta deberia parsear")
	}
	// El paquete construido es un request (flag 0), asi que no es un exito.
	if resp.ok() {
		t.Fatalf("un request no puede darse por bueno: %+v", resp)
	}
	if !strings.HasPrefix(resp.SessionFlag, "b") {
		t.Fatalf("session flag = %q", resp.SessionFlag)
	}

	// Con el flag a 1 es la respuesta buena del timbre.
	putU16(pkt, 0x2E, 1)
	resp, ok = parseP2pTestResponse(pkt)
	if !ok || !resp.ok() {
		t.Fatalf("respuesta = %+v ok=%v", resp, ok)
	}
	if resp.Address != "86.127.231.201" || resp.Port != 54450 {
		t.Fatalf("direccion = %s:%d", resp.Address, resp.Port)
	}

	if _, ok := parseP2pTestResponse(make([]byte, 8)); ok {
		t.Fatal("un paquete corto no puede parsear")
	}
	// Un paquete largo sin el comando de test cae en el formato plano, que no
	// comprueba el marcador: se documenta el comportamiento real.
	flat, ok := parseP2pTestResponse(make([]byte, p2pFrameSize))
	if !ok || !flat.ok() || flat.Port != 0 {
		t.Fatalf("formato plano = %+v ok=%v", flat, ok)
	}
}

func TestP2pTransportFrameRoundTrip(t *testing.T) {
	flag := strings.Repeat("c", 60)
	pkt := buildP2pTransportPacket(flag, 7, 54450, "127.0.0.1", 999, 102, 0)
	if len(pkt) != p2pFrameSize {
		t.Fatalf("frame de %d bytes", len(pkt))
	}
	frame, ok := parseP2pTransportFrame(pkt)
	if !ok {
		t.Fatal("el frame deberia parsear")
	}
	if !frame.isRequest() {
		t.Fatalf("flag %d deberia ser request", frame.PacketTypeFlag)
	}
	if frame.RemoteIP != "127.0.0.1" || frame.RemotePort != 999 {
		t.Fatalf("remoto = %s:%d", frame.RemoteIP, frame.RemotePort)
	}
	if !strings.HasPrefix(frame.SessionFlag, "c") {
		t.Fatalf("session flag = %q", frame.SessionFlag)
	}
	if frame.TailCode != 102 {
		t.Fatalf("tail = %d", frame.TailCode)
	}

	// Una respuesta del timbre lleva flag 1 y no es request.
	resp := buildP2pTransportPacket(flag, 8, 54450, "127.0.0.1", 999, 200, 1)
	frame, ok = parseP2pTransportFrame(resp)
	if !ok || frame.isRequest() {
		t.Fatalf("frame de respuesta = %+v ok=%v", frame, ok)
	}
}

func TestNatcheckQueryAndResponse(t *testing.T) {
	q := buildNatcheckQuery()
	if len(q) != natcheckQuerySize {
		t.Fatalf("consulta de %d bytes, se esperaban %d", len(q), natcheckQuerySize)
	}
	if readU32(q, 0) != p2pMarker {
		t.Fatalf("marcador = %#x", readU32(q, 0))
	}
	// 0x2D a 1 es lo que pide el mapeo externo: sin ese flag el servidor solo
	// devuelve un eco y el timbre recibiria el endpoint equivocado.
	if q[0x2D] != 0x01 {
		t.Fatalf("flag 0x2D = %#x", q[0x2D])
	}

	resp := append([]byte(nil), q...)
	putCString(resp, 0x4C, 0x10, "79.112.63.227")
	putU16(resp, 0x5C, 35197)
	ip, port, ok := parseNatcheckResponse(resp)
	if !ok || ip != "79.112.63.227" || port != 35197 {
		t.Fatalf("mapeo = %s:%d ok=%v", ip, port, ok)
	}

	// Puerto a cero no es un mapeo valido.
	putU16(resp, 0x5C, 0)
	if _, _, ok := parseNatcheckResponse(resp); ok {
		t.Fatal("puerto 0 no deberia valer")
	}
	// Sin IP tampoco. Se limpia el hueco a mano: putCString no escribe cero
	// de cierre y la IP anterior seguiria ahi.
	putU16(resp, 0x5C, 1234)
	for i := 0x4C; i < 0x5C; i++ {
		resp[i] = 0
	}
	if _, _, ok := parseNatcheckResponse(resp); ok {
		t.Fatal("sin ip no deberia valer")
	}
	if _, _, ok := parseNatcheckResponse(make([]byte, 10)); ok {
		t.Fatal("una respuesta corta no deberia valer")
	}
}
