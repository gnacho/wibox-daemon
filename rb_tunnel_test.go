package main

import (
	"bytes"
	"net"
	"strings"
	"testing"
	"time"
)

// Timbre falso del tunel: contesta al bootstrap con SYN_ACK y despues manda
// las cinco etapas del play sync, que es lo que cierra el canal QUII.
func TestTunnelConnectsAndReachesQuiiReady(t *testing.T) {
	bell, err := net.ListenUDP("udp4", &net.UDPAddr{IP: net.IPv4(127, 0, 0, 1)})
	if err != nil {
		t.Fatalf("timbre falso: %v", err)
	}
	defer bell.Close()

	conn, err := net.ListenUDP("udp4", &net.UDPAddr{IP: net.IPv4(127, 0, 0, 1)})
	if err != nil {
		t.Fatalf("socket local: %v", err)
	}

	tr := newRbTunnel(conn, bell.LocalAddr().(*net.UDPAddr),
		strings.Repeat("a", 64), bytes.Repeat([]byte{7}, 32), "clave", 1, 2)
	tr.start()
	defer tr.close()

	gotBootstrap := make(chan struct{})
	go func() {
		buf := make([]byte, 4096)
		n, addr, rerr := bell.ReadFromUDP(buf)
		if rerr != nil {
			return
		}
		c, perr := parseRbControl(buf[:n])
		if perr != nil || c.StatusWord != ctrlBootstrap {
			return
		}
		close(gotBootstrap)
		if _, werr := bell.WriteToUDP(buildRbControl(0, 0, 0, 77, ctrlSynAck), addr); werr != nil {
			return
		}
		for range playSyncStageStatuses {
			time.Sleep(20 * time.Millisecond)
			if _, werr := bell.WriteToUDP(buildRbControl(0, 0, 100, 200, ctrlPlaySync), addr); werr != nil {
				return
			}
		}
	}()

	if err := tr.awaitConnected(2 * time.Second); err != nil {
		t.Fatalf("no conecto: %v", err)
	}
	select {
	case <-gotBootstrap:
	case <-time.After(time.Second):
		t.Fatal("el tunel no mando el bootstrap")
	}
	if err := tr.awaitQuiiReady(3 * time.Second); err != nil {
		t.Fatalf("play sync sin cerrar: %v", err)
	}
}

// Sin respuesta no hay conexion y el error lo dice.
func TestTunnelRejectsWhenNobodyAnswers(t *testing.T) {
	bell, err := net.ListenUDP("udp4", &net.UDPAddr{IP: net.IPv4(127, 0, 0, 1)})
	if err != nil {
		t.Fatalf("timbre falso: %v", err)
	}
	addr := bell.LocalAddr().(*net.UDPAddr)
	bell.Close()

	conn, err := net.ListenUDP("udp4", &net.UDPAddr{IP: net.IPv4(127, 0, 0, 1)})
	if err != nil {
		t.Fatalf("socket local: %v", err)
	}
	tr := newRbTunnel(conn, addr, strings.Repeat("a", 64),
		bytes.Repeat([]byte{7}, 32), "clave", 1, 2)
	tr.start()
	defer tr.close()
	if err := tr.awaitConnected(300 * time.Millisecond); err == nil {
		t.Fatal("esperaba error sin timbre al otro lado")
	}
}
