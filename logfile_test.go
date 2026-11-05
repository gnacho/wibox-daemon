package main

import (
	"log"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestSetupLogFileTeesOutput(t *testing.T) {
	path := filepath.Join(t.TempDir(), "daemon.log")
	done, err := setupLogFile(path)
	if err != nil {
		t.Fatalf("setupLogFile: %v", err)
	}
	previous := log.Writer()
	log.Print("linea de prueba")
	done()
	log.SetOutput(previous)

	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("leer el log: %v", err)
	}
	if !strings.Contains(string(raw), "linea de prueba") {
		t.Fatalf("el fichero no recogio la linea: %q", string(raw))
	}
}

func TestSetupLogFileEmptyPathDoesNothing(t *testing.T) {
	done, err := setupLogFile("")
	if err != nil {
		t.Fatalf("setupLogFile: %v", err)
	}
	done()
	before := log.Writer()
	done2, _ := setupLogFile("")
	done2()
	if log.Writer() != before {
		t.Fatal("una ruta vacia no debe cambiar la salida del log")
	}
}
