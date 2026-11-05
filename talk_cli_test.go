package main

import (
	"encoding/binary"
	"os"
	"path/filepath"
	"testing"
)

// buildWAV arma un WAV minimo con los parametros pedidos.
func buildWAV(format, channels, rate, bits int, pcm []byte) []byte {
	blockAlign := channels * bits / 8
	var b []byte
	b = append(b, "RIFF"...)
	b = binary.LittleEndian.AppendUint32(b, uint32(36+len(pcm)))
	b = append(b, "WAVE"...)
	b = append(b, "fmt "...)
	b = binary.LittleEndian.AppendUint32(b, 16)
	b = binary.LittleEndian.AppendUint16(b, uint16(format))
	b = binary.LittleEndian.AppendUint16(b, uint16(channels))
	b = binary.LittleEndian.AppendUint32(b, uint32(rate))
	b = binary.LittleEndian.AppendUint32(b, uint32(rate*blockAlign))
	b = binary.LittleEndian.AppendUint16(b, uint16(blockAlign))
	b = binary.LittleEndian.AppendUint16(b, uint16(bits))
	b = append(b, "data"...)
	b = binary.LittleEndian.AppendUint32(b, uint32(len(pcm)))
	return append(b, pcm...)
}

func writeTemp(t *testing.T, name string, data []byte) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), name)
	if err := os.WriteFile(path, data, 0o600); err != nil {
		t.Fatalf("escribiendo %s: %v", name, err)
	}
	return path
}

func TestReadWAVPCM(t *testing.T) {
	pcm := make([]byte, 640)
	pcm[0], pcm[1] = 0x10, 0x27
	path := writeTemp(t, "ok.wav", buildWAV(1, 1, 8000, 16, pcm))
	got, err := readWAVPCM(path)
	if err != nil {
		t.Fatalf("readWAVPCM: %v", err)
	}
	if len(got) != len(pcm) || got[0] != 0x10 {
		t.Fatalf("pcm de %d bytes, se esperaban %d", len(got), len(pcm))
	}
}

func TestReadWAVPCMRejectsStereo(t *testing.T) {
	path := writeTemp(t, "stereo.wav", buildWAV(1, 2, 8000, 16, make([]byte, 640)))
	if _, err := readWAVPCM(path); err == nil {
		t.Fatal("esperaba error con dos canales")
	}
}

func TestReadWAVPCMRejectsWrongRate(t *testing.T) {
	path := writeTemp(t, "rate.wav", buildWAV(1, 1, 44100, 16, make([]byte, 640)))
	if _, err := readWAVPCM(path); err == nil {
		t.Fatal("esperaba error a 44,1 kHz")
	}
}

func TestReadWAVPCMRejectsNotWAV(t *testing.T) {
	path := writeTemp(t, "no.bin", []byte("no soy un wav"))
	if _, err := readWAVPCM(path); err == nil {
		t.Fatal("esperaba error con datos que no son WAV")
	}
}

func TestReadWAVPCMMissingFile(t *testing.T) {
	if _, err := readWAVPCM(filepath.Join(t.TempDir(), "nada.wav")); err == nil {
		t.Fatal("esperaba error con fichero inexistente")
	}
}
