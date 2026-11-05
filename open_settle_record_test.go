package main

import (
	"os"
	"path/filepath"
	"testing"
	"time"
)

// Solo una apertura buena ocupa la sesion del timbre. Si se marcaran tambien
// los fallos, el video se quedaria esperando una ventana entera para nada.
func TestRecordOpenOnlyMarksSuccessfulOpens(t *testing.T) {
	lock := filepath.Join(t.TempDir(), "video.lock")
	d := &daemon{cfg: config{videoLock: lock}}

	d.recordOpen("dev", "remote", -1, os.ErrDeadlineExceeded)
	if _, err := os.Stat(lastOpenPath(lock)); err == nil {
		t.Fatal("una apertura con error no debe dejar marca")
	}

	d.recordOpen("dev", "remote", 3, nil)
	if _, err := os.Stat(lastOpenPath(lock)); err == nil {
		t.Fatal("una apertura con rc distinto de cero no debe dejar marca")
	}

	d.recordOpen("dev", "remote", 0, nil)
	info, err := os.Stat(lastOpenPath(lock))
	if err != nil {
		t.Fatalf("una apertura buena debe dejar marca: %v", err)
	}
	if info.Size() == 0 {
		t.Fatal("la marca quedo vacia")
	}
	if left := pendingOpenSettle(lock, time.Minute, time.Now()); left <= 0 {
		t.Fatalf("la ventana deberia estar abierta, quedan %s", left)
	}
}
