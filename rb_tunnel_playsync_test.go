package main

import (
	"testing"
	"time"
)

func TestTunnelFinishesPlaySync(t *testing.T) {
	tr, _ := newTestTunnel(t)
	tr.mu.Lock()
	tr.startPlaySync(100)
	tr.mu.Unlock()

	control, err := parseRbControl(buildRbControl(0, 0, 100, 200, ctrlPlaySync))
	if err != nil {
		t.Fatalf("control de play sync: %v", err)
	}
	for i := 0; i < len(playSyncStageStatuses); i++ {
		tr.mu.Lock()
		tr.handlePlaySync(control)
		tr.mu.Unlock()
	}
	if err := tr.awaitQuiiReady(50 * time.Millisecond); err != nil {
		t.Fatalf("el play sync no cerro: %v", err)
	}
	// Una etapa de mas no debe romper nada ni volver a bloquear el canal.
	tr.mu.Lock()
	tr.handlePlaySync(control)
	tr.mu.Unlock()
	if err := tr.awaitQuiiReady(20 * time.Millisecond); err != nil {
		t.Fatalf("tras cerrar, awaitQuiiReady sigue fallando: %v", err)
	}
}

func TestTunnelPlaySyncAdjustsIdentifiers(t *testing.T) {
	tr, _ := newTestTunnel(t)
	tr.mu.Lock()
	tr.startPlaySync(100)
	tr.mu.Unlock()

	control, cerr := parseRbControl(buildRbControl(0, 0, 100, 200, ctrlPlaySync))
	if cerr != nil {
		t.Fatalf("control de play sync: %v", cerr)
	}
	tr.mu.Lock()
	tr.handlePlaySync(control)
	gotRemote := tr.playSyncRemoteID
	gotStage := tr.playSyncStage
	tr.mu.Unlock()

	base := uint32(100)
	wantRemote := base + playSyncStep
	wantRemote += stage2RemoteBias
	if gotRemote != wantRemote {
		t.Fatalf("remote=%d, se esperaba %d", gotRemote, wantRemote)
	}
	if gotStage != 1 {
		t.Fatalf("etapa=%d, se esperaba 1", gotStage)
	}
}

func TestTunnelSendsPlayProbeOnThresholds(t *testing.T) {
	tr, bell := newTestTunnel(t)
	tr.mu.Lock()
	tr.startPlaySync(100)
	tr.mu.Unlock()

	tr.mu.Lock()
	tr.playSyncSentCount = 17
	tr.maybeSendPlayProbe()
	stage := tr.probeStage
	tr.mu.Unlock()
	if stage != 0 {
		t.Fatalf("con 17 sondeos no debe disparar, probeStage=%d", stage)
	}

	tr.mu.Lock()
	tr.playSyncSentCount = 18
	tr.maybeSendPlayProbe()
	stage = tr.probeStage
	tr.mu.Unlock()
	if stage != 1 {
		t.Fatalf("probeStage=%d, se esperaba 1", stage)
	}
	_ = readBellDatagram(t, bell, time.Second)

	tr.mu.Lock()
	before := tr.playSyncLocalID
	tr.playSyncSentCount = 35
	tr.maybeSendPlayProbe()
	stage = tr.probeStage
	after := tr.playSyncLocalID
	tr.mu.Unlock()
	if stage != 2 {
		t.Fatalf("probeStage=%d, se esperaba 2", stage)
	}
	if after != before+stage2LocalDelta {
		t.Fatalf("local=%d, se esperaba %d", after, before+stage2LocalDelta)
	}
}
