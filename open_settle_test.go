package main

import (
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestPendingOpenSettle(t *testing.T) {
	dir := t.TempDir()
	lock := filepath.Join(dir, "video.lock")
	now := time.Date(2026, 11, 6, 12, 0, 0, 0, time.UTC)

	if got := pendingOpenSettle(lock, 75*time.Second, now); got != 0 {
		t.Fatalf("sin marca deberia ser 0, es %s", got)
	}

	if err := markOpen(lock, now.Add(-10*time.Second)); err != nil {
		t.Fatalf("markOpen: %v", err)
	}
	if got := pendingOpenSettle(lock, 75*time.Second, now); got != 65*time.Second {
		t.Fatalf("con una apertura de hace 10 s quedan 65 s, quedan %s", got)
	}

	if err := markOpen(lock, now.Add(-90*time.Second)); err != nil {
		t.Fatalf("markOpen: %v", err)
	}
	if got := pendingOpenSettle(lock, 75*time.Second, now); got != 0 {
		t.Fatalf("una apertura vieja no debe hacer esperar, espera %s", got)
	}

	if got := pendingOpenSettle(lock, 0, now); got != 0 {
		t.Fatalf("con ventana 0 no se espera, espera %s", got)
	}
	if got := pendingOpenSettle("", 75*time.Second, now); got != 0 {
		t.Fatalf("sin cerrojo no se espera, espera %s", got)
	}
}

func TestPendingOpenSettleIgnoresGarbage(t *testing.T) {
	dir := t.TempDir()
	lock := filepath.Join(dir, "video.lock")
	if err := os.WriteFile(lastOpenPath(lock), []byte("no es un numero\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if got := pendingOpenSettle(lock, 75*time.Second, time.Now()); got != 0 {
		t.Fatalf("una marca ilegible no debe bloquear, espera %s", got)
	}
}

func TestMarkOpenWithoutLock(t *testing.T) {
	if err := markOpen("", time.Now()); err != nil {
		t.Fatalf("sin cerrojo deberia ser un no-op, error %v", err)
	}
}

func TestAwaitOpenSettle(t *testing.T) {
	dir := t.TempDir()
	lock := filepath.Join(dir, "video.lock")
	if err := markOpen(lock, time.Now()); err != nil {
		t.Fatal(err)
	}
	start := time.Now()
	awaitOpenSettle(lock, 40*time.Millisecond)
	if elapsed := time.Since(start); elapsed < 25*time.Millisecond {
		t.Fatalf("deberia haber esperado la ventana, espero %s", elapsed)
	}
}
