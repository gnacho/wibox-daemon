package main

import (
	"encoding/hex"
	"fmt"
	"net"
	"os"
	"testing"
	"time"
)

// TestNatcheckLive consulta el servidor natcheck real e imprime la respuesta
// cruda. Es una prueba manual: solo corre con WIBOX_NATCHECK_LIVE=1.
func TestNatcheckLive(t *testing.T) {
	if os.Getenv("WIBOX_NATCHECK_LIVE") == "" {
		t.Skip("prueba manual, activar con WIBOX_NATCHECK_LIVE=1")
	}
	url := os.Getenv("WIBOX_NATCHECK_URL")
	if url == "" {
		url = "udp://8.211.5.8:8300"
	}
	conn, err := net.ListenUDP("udp4", &net.UDPAddr{})
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()
	localPort := conn.LocalAddr().(*net.UDPAddr).Port
	flag := "abcdefghijklmnopqrstuvwxyzabcdefghijklmnopqrstuvwxyzabcdefgh3csfewyk"
	t.Logf("local port %d, peticion %d bytes", localPort, p2pFrameSize)
	got := natcheckRaw(conn, flag, 12345, url)
	for i, p := range got {
		t.Logf("respuesta %d (%d bytes): %s", i, len(p), hex.EncodeToString(p))
	}
	if len(got) == 0 {
		t.Fatal("sin respuesta del natcheck")
	}
	// volcado crudo de la respuesta a nuestra plantilla corta
	addr, _ := net.ResolveUDPAddr("udp", url[len("udp://"):])
	for i := 0; i < 2; i++ {
		conn.WriteToUDP(buildNatcheckQuery(), addr)
		conn.SetReadDeadline(time.Now().Add(1500 * time.Millisecond))
		buf2 := make([]byte, 4096)
		n, _, err := conn.ReadFromUDP(buf2)
		t.Logf("corta intento %d: n=%d err=%v hex=%s", i, n, err, hex.EncodeToString(buf2[:n]))
	}
	mapped := natcheckPublic(conn, url)
	if mapped == nil {
		t.Fatal("natcheckPublic sin mapeo")
	}
	t.Logf("natcheckPublic: %s (puerto local %d)", mapped, localPort)
}

// natcheckRaw manda la peticion de natcheck y devuelve cada respuesta cruda.
func natcheckRaw(conn *net.UDPConn, sessionFlag string, responseSessionID int64, natURL string) [][]byte {
	host := natURL[len("udp://"):]
	var port int
	fmt.Sscanf(host[len(host)-4:], "%d", &port)
	addr, err := net.ResolveUDPAddr("udp", host)
	if err != nil {
		return nil
	}
	localPort := conn.LocalAddr().(*net.UDPAddr).Port
	buf := make([]byte, 4096)
	var out [][]byte
	for attempt := 0; attempt < 2; attempt++ {
		pkt := buildP2pTestPacket(p2pProbeSeqLAN, sessionFlag, responseSessionID, localPort, "8.211.5.8", 8300, 0)
		if _, err := conn.WriteToUDP(pkt, addr); err != nil {
			continue
		}
		conn.SetReadDeadline(time.Now().Add(1500 * time.Millisecond))
		n, _, err := conn.ReadFromUDP(buf)
		if err != nil {
			continue
		}
		cp := make([]byte, n)
		copy(cp, buf[:n])
		out = append(out, cp)
	}
	return out
}
