package main

import (
	"os"
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

func TestVideoLockStateReportsTheHolderPID(t *testing.T) {
	sock := t.TempDir() + "/video.lock"

	free, pid := videoLockState(sock)
	if !free || pid != 0 {
		t.Fatalf("libre esperaba true/0, obtuve %v/%d", free, pid)
	}

	release, err := acquireVideoLock(sock)
	if err != nil {
		t.Fatalf("no se pudo tomar el cerrojo: %v", err)
	}
	defer release()

	free, pid = videoLockState(sock)
	if free {
		t.Fatal("el cerrojo esta tomado")
	}
	if pid != os.Getpid() {
		t.Fatalf("pid=%d, se esperaba %d", pid, os.Getpid())
	}
}
