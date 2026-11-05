package main

import (
	"os"
	"path/filepath"
	"testing"
)

func TestDecodeDumpWalksTheFrames(t *testing.T) {
	key := make([]byte, 32)
	for i := range key {
		key[i] = byte(i * 7)
	}
	payload := make([]byte, 40)
	for i := range payload {
		payload[i] = byte(i)
	}
	var dump []byte
	for seq := uint32(1); seq <= 3; seq++ {
		dump = append(dump, buildRbData(payload, rbSrcIDBase, 0x04000006, seq)...)
	}
	path := filepath.Join(t.TempDir(), "dump.bin")
	if err := os.WriteFile(path, dump, 0o600); err != nil {
		t.Fatalf("escribiendo el volcado: %v", err)
	}
	if err := decodeDump(path, key, 2); err != nil {
		t.Fatalf("decodeDump: %v", err)
	}
}

func TestDecodeDumpWithoutFrames(t *testing.T) {
	path := filepath.Join(t.TempDir(), "basura.bin")
	if err := os.WriteFile(path, []byte("no hay frames aqui"), 0o600); err != nil {
		t.Fatalf("escribiendo: %v", err)
	}
	if err := decodeDump(path, make([]byte, 32), 5); err != nil {
		t.Fatalf("decodeDump: %v", err)
	}
}

func TestDecodeDumpMissingFile(t *testing.T) {
	if err := decodeDump(filepath.Join(t.TempDir(), "no.bin"), make([]byte, 32), 1); err == nil {
		t.Fatal("se esperaba error con un fichero que no existe")
	}
}
