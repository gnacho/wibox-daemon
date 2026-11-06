package main

import (
	"os"
	"path/filepath"
	"testing"
)

func TestTruncate(t *testing.T) {
	if got := truncate("corto", 20); got != "corto" {
		t.Fatalf("%q, se esperaba intacto", got)
	}
	if got := truncate("abcdef", 3); got != "abc..." {
		t.Fatalf("%q, se esperaba abc...", got)
	}
	// Justo el limite no recorta ni anyade nada.
	if got := truncate("abc", 3); got != "abc" {
		t.Fatalf("%q, se esperaba intacto en el limite", got)
	}
}

func TestFrameKindName(t *testing.T) {
	cases := map[int]string{
		0x03: "audio",
		0x00: "video",
		0x01: "video",
		0x09: "video",
		0x0A: "video",
		0x07: "0x07",
		0xFF: "0xff",
	}
	for in, want := range cases {
		if got := frameKindName(in); got != want {
			t.Fatalf("%#x: %q, se esperaba %q", in, got, want)
		}
	}
}

func TestHasStartCode(t *testing.T) {
	if !hasStartCode([]byte{0x00, 0x00, 0x01, 0x41}) {
		t.Fatal("start code de tres bytes no detectado")
	}
	if !hasStartCode([]byte{0x00, 0x00, 0x00, 0x01, 0x67}) {
		t.Fatal("start code de cuatro bytes no detectado")
	}
	if hasStartCode([]byte{0x01, 0x02, 0x03}) {
		t.Fatal("se invento un start code")
	}
	if hasStartCode(nil) {
		t.Fatal("con nada deberia decir que no")
	}
}

// videoLockState es lo que alimenta /status: cuando el cerrojo esta libre lo
// suelta, y cuando no, devuelve el pid que el dueno dejo escrito.
func TestVideoLockState(t *testing.T) {
	path := filepath.Join(t.TempDir(), "video.lock")
	if free, pid := videoLockState(path); !free || pid != 0 {
		t.Fatalf("libre: %v, %d", free, pid)
	}

	release, err := acquireVideoLock(path)
	if err != nil {
		t.Fatalf("cerrojo: %v", err)
	}
	free, pid := videoLockState(path)
	if free {
		t.Fatal("ocupado y dice libre")
	}
	if pid != os.Getpid() {
		t.Fatalf("pid %d, se esperaba %d", pid, os.Getpid())
	}
	release()

	if free, _ := videoLockState(path); !free {
		t.Fatal("tras soltarlo deberia estar libre")
	}
}
