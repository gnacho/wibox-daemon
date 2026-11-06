package main

import (
	"path/filepath"
	"testing"
	"time"
)

// Un cerrojo ocupado ya no puede dejar al espectador nuevo sin nada: la espera
// acaba enganchando la sesion en cuanto la anterior se suelta.
func TestAcquireVideoLockWaitTakesOver(t *testing.T) {
	path := filepath.Join(t.TempDir(), "video.lock")
	release, err := acquireVideoLock(path)
	if err != nil {
		t.Fatalf("cerrojo inicial: %v", err)
	}
	go func() {
		time.Sleep(120 * time.Millisecond)
		release()
	}()

	got, err := acquireVideoLockWait(path, 2*time.Second)
	if err != nil {
		t.Fatalf("no tomo el cerrojo tras esperar: %v", err)
	}
	got()
}

func TestAcquireVideoLockWaitGivesUpWhenAskedNotToWait(t *testing.T) {
	path := filepath.Join(t.TempDir(), "video.lock")
	release, err := acquireVideoLock(path)
	if err != nil {
		t.Fatalf("cerrojo inicial: %v", err)
	}
	defer release()

	if _, err := acquireVideoLockWait(path, 0); err == nil {
		t.Fatal("con espera cero deberia fallar si esta ocupado")
	}
}

func TestLockWaitDefault(t *testing.T) {
	t.Setenv("WIBOX_LOCK_WAIT", "")
	if got := lockWait(); got != 300*time.Second {
		t.Fatalf("defecto %v, se esperaba 300s", got)
	}
	t.Setenv("WIBOX_LOCK_WAIT", "0")
	if got := lockWait(); got != 0 {
		t.Fatalf("%v, se esperaba 0", got)
	}
}
