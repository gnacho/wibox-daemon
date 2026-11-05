package main

import (
	"testing"
	"time"
)

func TestTunnelFinishesPlaySync(t *testing.T) {
	tr, _ := newTestTunnel(t)
	tr.startPlaySync(100)
	control, err := parseRbControl(buildRbControl(0, 0, 100, 200, ctrlPlaySync))
	if err != nil {
		t.Fatalf("control de play sync: %v", err)
	}
	for i := 0; i < len(playSyncStageStatuses); i++ {
		tr.handlePlaySync(control)
	}
	if err := tr.awaitQuiiReady(50 * time.Millisecond); err != nil {
		t.Fatalf("el play sync no cerro: %v", err)
	}
	// Una etapa de mas no debe romper nada ni volver a bloquear el canal.
	tr.handlePlaySync(control)
	if err := tr.awaitQuiiReady(20 * time.Millisecond); err != nil {
		t.Fatalf("tras cerrar, awaitQuiiReady sigue fallando: %v", err)
	}
}

func TestTunnelPlaySyncAdjustsIdentifiers(t *testing.T) {
	tr, _ := newTestTunnel(t)
	tr.startPlaySync(100)
	control, cerr := parseRbControl(buildRbControl(0, 0, 100, 200, ctrlPlaySync))
	if cerr != nil {
		t.Fatalf("control de play sync: %v", cerr)
	}
	tr.handlePlaySync(control)
	base := uint32(100)
	wantRemote := base + playSyncStep
	wantRemote += stage2RemoteBias
	if tr.playSyncRemoteID != wantRemote {
		t.Fatalf("remote=%d, se esperaba %d", tr.playSyncRemoteID, wantRemote)
	}
	if tr.playSyncStage != 1 {
		t.Fatalf("etapa=%d, se esperaba 1", tr.playSyncStage)
	}
}

func TestTunnelSendsPlayProbeOnThresholds(t *testing.T) {
	tr, bell := newTestTunnel(t)
	tr.startPlaySync(100)

	tr.playSyncSentCount = 17
	tr.maybeSendPlayProbe()
	if tr.probeStage != 0 {
		t.Fatalf("con 17 sondeos no debe disparar, probeStage=%d", tr.probeStage)
	}

	tr.playSyncSentCount = 18
	tr.maybeSendPlayProbe()
	if tr.probeStage != 1 {
		t.Fatalf("probeStage=%d, se esperaba 1", tr.probeStage)
	}
	_ = readBellDatagram(t, bell, time.Second)

	before := tr.playSyncLocalID
	tr.playSyncSentCount = 35
	tr.maybeSendPlayProbe()
	if tr.probeStage != 2 {
		t.Fatalf("probeStage=%d, se esperaba 2", tr.probeStage)
	}
	if tr.playSyncLocalID != before+stage2LocalDelta {
		t.Fatalf("local=%d, se esperaba %d", tr.playSyncLocalID, before+stage2LocalDelta)
	}
}
