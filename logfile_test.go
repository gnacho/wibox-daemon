package main

import (
	"log"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestRotatingWriterKeepsAOldCopy(t *testing.T) {
	path := filepath.Join(t.TempDir(), "video.log")
	w, err := newRotatingWriter(path, 16, time.Nanosecond)
	if err != nil {
		t.Fatalf("abrir: %v", err)
	}
	defer w.Close()
	if _, err := w.Write([]byte(strings.Repeat("a", 32))); err != nil {
		t.Fatalf("primera escritura: %v", err)
	}
	time.Sleep(2 * time.Millisecond)
	if _, err := w.Write([]byte("nueva linea\n")); err != nil {
		t.Fatalf("segunda escritura: %v", err)
	}
	if _, err := os.Stat(path + ".1"); err != nil {
		t.Fatalf("no se creo la copia rotada: %v", err)
	}
	info, err := os.Stat(path)
	if err != nil {
		t.Fatalf("stat: %v", err)
	}
	if info.Size() >= 32 {
		t.Fatalf("el fichero nuevo conserva %d bytes, no roto", info.Size())
	}
}

func TestSetupLogFileTeesOutput(t *testing.T) {
	path := filepath.Join(t.TempDir(), "test.log")
	done, err := setupLogFile(path)
	if err != nil {
		t.Fatalf("setup: %v", err)
	}
	log.Print("hola log")
	done()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("leer: %v", err)
	}
	if !strings.Contains(string(data), "hola log") {
		t.Fatalf("el fichero no tiene la linea: %q", string(data))
	}
}

func TestSetupLogFileEmptyPathDoesNothing(t *testing.T) {
	done, err := setupLogFile("")
	if err != nil {
		t.Fatalf("setup: %v", err)
	}
	done()
}
