package main

import (
	"strings"
	"testing"
)

// buildTestRbFrame arma un frame RB-UDP de datos con el payload indicado.
func buildTestRbFrame(payload []byte, seq uint32) []byte {
	total := 0x38 + len(payload)
	pkt := make([]byte, total)
	putU32(pkt, 0x00, 0xFFFFFFFF)
	putU32(pkt, 0x04, uint32(total))
	putU32(pkt, 0x14, rbCmdData)
	putU32(pkt, 0x18, seq)
	putU32(pkt, 0x24, uint32(total-0x28))
	putU32(pkt, 0x28, 0x04000006)
	putU32(pkt, 0x2C, 0x39500000)
	copy(pkt[0x38:], payload)
	return pkt
}

func TestWalkRbFramesReadsEveryFrame(t *testing.T) {
	raw := append(buildTestRbFrame([]byte("hola"), 7), buildTestRbFrame([]byte("adios"), 9)...)
	frames := walkRbFrames(raw)
	if len(frames) != 2 {
		t.Fatalf("frames=%d, se esperaban 2", len(frames))
	}
	if string(frames[0].Payload) != "hola" || string(frames[1].Payload) != "adios" {
		t.Fatalf("payloads=%q %q", frames[0].Payload, frames[1].Payload)
	}
	if frames[0].Seq != 7 || frames[1].Seq != 9 {
		t.Fatalf("seqs=%d %d", frames[0].Seq, frames[1].Seq)
	}
	if frames[0].Command != rbCmdData {
		t.Fatalf("command=0x%08x", frames[0].Command)
	}
}

func TestWalkRbFramesSkipsGarbageBetweenFrames(t *testing.T) {
	raw := []byte("basura")
	raw = append(raw, buildTestRbFrame([]byte("x"), 1)...)
	frames := walkRbFrames(raw)
	if len(frames) != 1 || string(frames[0].Payload) != "x" {
		t.Fatalf("frames=%d", len(frames))
	}
}

func TestWalkRbFramesIgnoresTruncatedFrame(t *testing.T) {
	full := buildTestRbFrame([]byte("hola"), 1)
	if frames := walkRbFrames(full[:0x30]); len(frames) != 0 {
		t.Fatalf("frames=%d con un frame truncado", len(frames))
	}
	empty := buildTestRbFrame([]byte("x"), 1)
	putU32(empty, 0x14, 0x00120101)
	if frames := walkRbFrames(empty); len(frames) != 0 {
		t.Fatalf("frames=%d con un comando que no es de datos", len(frames))
	}
}

func TestCountH264StartCodes(t *testing.T) {
	tresBytes := []byte{0, 0, 1, 0x65, 0, 0, 1, 0x41, 0}
	if got := countH264StartCodes(tresBytes); got != 2 {
		t.Fatalf("start codes=%d, se esperaban 2", got)
	}
	cuatroBytes := []byte{0, 0, 0, 1, 0x67}
	if got := countH264StartCodes(cuatroBytes); got != 1 {
		t.Fatalf("start codes=%d con el prefijo de cuatro bytes", got)
	}
	if got := countH264StartCodes([]byte{0, 0}); got != 0 {
		t.Fatalf("start codes=%d en dos ceros", got)
	}
}

func TestCountNALTypesSortsTheStream(t *testing.T) {
	stream := []byte{
		0, 0, 0, 1, 0x67, // SPS
		0, 0, 1, 0x68, // PPS
		0, 0, 1, 0x65, // IDR
		0, 0, 1, 0x41, // slice P
	}
	acc := map[int]int{}
	countNALTypes(stream, acc)
	if acc[7] != 1 || acc[8] != 1 || acc[5] != 1 || acc[1] != 1 {
		t.Fatalf("censo inesperado: %v", acc)
	}
}

func TestEnvFallsBackToDefault(t *testing.T) {
	t.Setenv("WIBOX_TEST_ENV", "valor")
	if got := env("WIBOX_TEST_ENV", "defecto"); got != "valor" {
		t.Fatalf("env=%q con la variable puesta", got)
	}
	t.Setenv("WIBOX_TEST_ENV", "")
	if got := env("WIBOX_TEST_ENV", "defecto"); got != "defecto" {
		t.Fatalf("env=%q con la variable vacia", got)
	}
}

func TestRemotePlayPasswordPrefersTheDynamicPassword(t *testing.T) {
	tok := &DeviceToken{AuthCode: "auth", DataEncodeKey: strings.Repeat("k", 32), DynamicPassword: "dinamica"}
	if got := remotePlayPassword(tok); got != "dinamica" {
		t.Fatalf("password=%q, se esperaba la dinamica", got)
	}
	tok.DynamicPassword = ""
	if got := remotePlayPassword(tok); got != encodeDevicePassword("auth") {
		t.Fatalf("password=%q, se esperaba el auth-code codificado", got)
	}
}

func TestMaskTokenHidesTheSecret(t *testing.T) {
	if got := maskToken(""); got != "desactivado" {
		t.Fatalf("maskToken vacio=%q", got)
	}
	if got := maskToken("abc"); got != "***" {
		t.Fatalf("maskToken corto=%q", got)
	}
	got := maskToken("wiboxhaos2026")
	if !strings.HasPrefix(got, "wiboxh") || !strings.Contains(got, "*") || strings.Contains(got, "2026") {
		t.Fatalf("maskToken largo=%q", got)
	}
}

func TestOrNoneExplainsTheMissingLAN(t *testing.T) {
	if got := orNone("192.168.1.60:34567"); got != "192.168.1.60:34567" {
		t.Fatalf("orNone con valor=%q", got)
	}
	got := orNone("")
	if got == "" || !strings.Contains(got, "no configurada") {
		t.Fatalf("orNone vacio=%q", got)
	}
}

func TestMinAndUsername(t *testing.T) {
	if min(2, 5) != 2 || min(5, 2) != 2 || min(3, 3) != 3 {
		t.Fatal("min no devuelve el menor")
	}
	c := &QuiiClient{}
	if c.username() != "adminapp2" {
		t.Fatalf("username=%q", c.username())
	}
}

func TestLocalIPv4sSkipsLoopback(t *testing.T) {
	ips := localIPv4s()
	for _, ip := range ips {
		if strings.HasPrefix(ip, "127.") {
			t.Fatalf("localIPv4s devolvio el loopback: %v", ips)
		}
	}
	if len(localIPv4s()) != len(ips) {
		t.Fatal("localIPv4s no es estable entre llamadas")
	}
}
