package main

import (
	"testing"
	"time"
)

func TestTunnelSendsFlowWhenThePeerIsKnown(t *testing.T) {
	tr, _ := newTestTunnel(t)
	tr.peerLogicID = 55
	feedDatagram(tr, buildRbControl(0, 0, 1, 0, ctrlFlow))
	if !tr.flowSent {
		t.Fatal("no se marco el flujo como enviado")
	}
}

func TestTunnelSendsProgressOnce(t *testing.T) {
	tr, _ := newTestTunnel(t)
	tr.peerLogicID = 77
	// La primera rama de handleProgress solo pide el connect; la segunda, que
	// envia el progreso, exige que ese connect ya se haya pedido.
	tr.setupProbeSent = true
	feedDatagram(tr, buildRbControl(0, 0, 77, 77, ctrlProgress))
	if !tr.progressSent {
		t.Fatal("no se marco el progreso como enviado")
	}
}

func TestTunnelPendingEstablishedWithoutPlay(t *testing.T) {
	tr, bell := newTestTunnel(t)
	feedDatagram(tr, buildRbControl(0, 0, 10, 20, ctrlEstablished))

	// El establecimiento pendiente se consume al final del despacho: lo que se
	// puede comprobar es que el tunel manda el ctrlEstablished al timbre.
	bell.SetReadDeadline(time.Now().Add(500 * time.Millisecond))
	buf := make([]byte, 2048)
	n, _, err := bell.ReadFromUDP(buf)
	if err != nil {
		t.Fatalf("el timbre no recibio el establecimiento: %v", err)
	}
	c, perr := parseRbControl(buf[:n])
	if perr != nil {
		t.Fatalf("control ilegible: %v", perr)
	}
	if c.StatusWord != ctrlEstablished {
		t.Fatalf("estado 0x%08x, se esperaba 0x%08x", c.StatusWord, ctrlEstablished)
	}
}

func TestTunnelAnswersHeartbeat(t *testing.T) {
	tr, bell := newTestTunnel(t)
	tr.laneLocalID = 300
	tr.laneRemoteID = 400
	// El heartbeat se reconoce por status16 == 0x0D00; ctrlHeartbeatRequest es
	// la respuesta que mandamos nosotros, no lo que llega.
	feedDatagram(tr, buildRbControl(0, 0, 300, 400, ctrlFlow))

	bell.SetReadDeadline(time.Now().Add(500 * time.Millisecond))
	buf := make([]byte, 2048)
	seen := 0
	for seen < 2 {
		n, _, err := bell.ReadFromUDP(buf)
		if err != nil {
			t.Fatalf("el timbre esperaba dos respuestas, recibio %d: %v", seen, err)
		}
		if n < 28 {
			t.Fatalf("respuesta corta de %d bytes", n)
		}
		seen++
	}
}

func TestTunnelPlaySyncSeedsTheIdentifiers(t *testing.T) {
	tr, _ := newTestTunnel(t)
	tr.startPlaySync(100)
	if !tr.playSyncActive {
		t.Fatal("no se activo el play sync")
	}
	if tr.playSyncLocalID != 100+playSyncLocalBias {
		t.Fatalf("local=%d", tr.playSyncLocalID)
	}
	if tr.playSyncRemoteID != 100+playSyncStep {
		t.Fatalf("remote=%d", tr.playSyncRemoteID)
	}
	if err := tr.awaitConnected(10 * time.Millisecond); err == nil {
		t.Fatal("sin handshake awaitConnected debe fallar")
	}
}

func TestTunnelIgnoresPlaySyncWithBaseOne(t *testing.T) {
	tr, _ := newTestTunnel(t)
	tr.startPlaySync(1)
	if tr.playSyncActive {
		t.Fatal("una base de uno no debe activar el play sync")
	}
}
