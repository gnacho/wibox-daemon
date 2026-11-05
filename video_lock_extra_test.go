package main

import (
	"path/filepath"
	"testing"
)

func TestVideoLockFreeDetectsTheHolder(t *testing.T) {
	path := filepath.Join(t.TempDir(), "video.lock")
	if !videoLockFree(path) {
		t.Fatal("un cerrojo sin dueno debe estar libre")
	}
	release, err := acquireVideoLock(path)
	if err != nil {
		t.Fatalf("no se pudo tomar el cerrojo: %v", err)
	}
	if videoLockFree(path) {
		t.Fatal("con el cerrojo tomado no puede estar libre")
	}
	release()
	if !videoLockFree(path) {
		t.Fatal("tras soltarlo debe volver a estar libre")
	}
}
