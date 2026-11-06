package main

import (
	"bytes"
	"testing"
)

// pmtStreams saca los pares (stream_type, pid) del PMT del propio muxer,
// aplicando el pointer_field que va delante de la seccion cuando el paquete
// lleva el indicador de inicio de unidad.
func pmtStreams(t *testing.T, buf []byte) [][2]int {
	t.Helper()
	var out [][2]int
	for i := 0; i+188 <= len(buf); i += 188 {
		pkt := buf[i : i+188]
		if pkt[0] != 0x47 {
			t.Fatalf("paquete %d no empieza por sync: %#x", i/188, pkt[0])
		}
		pid := int(pkt[1]&0x1F)<<8 | int(pkt[2])
		if pid != int(tsPIDPmt) || pkt[1]&0x40 == 0 {
			continue
		}
		off := 4
		if pkt[3]&0x20 != 0 { // adaptation field
			off += 1 + int(pkt[4])
		}
		pl := pkt[off:]
		if len(pl) < 2 {
			t.Fatal("payload del PMT demasiado corto")
		}
		sec := pl[1+int(pl[0]):]
		if len(sec) < 12 || sec[0] != 0x02 {
			t.Fatalf("no parece un PMT: table_id %#x, %d bytes", sec[0], len(sec))
		}
		length := int(sec[1]&0x0F)<<8 | int(sec[2])
		infoLen := int(sec[10]&0x0F)<<8 | int(sec[11])
		end := 3 + length - 4 // sin CRC
		if end > len(sec) {
			t.Fatalf("section_length %d se sale de los %d bytes", length, len(sec))
		}
		for p := 12 + infoLen; p+5 <= end; {
			st := int(sec[p])
			esPID := int(sec[p+1]&0x1F)<<8 | int(sec[p+2])
			esInfo := int(sec[p+3]&0x0F)<<8 | int(sec[p+4])
			out = append(out, [2]int{st, esPID})
			p += 5 + esInfo
		}
		return out
	}
	t.Fatal("no hay PMT en la salida")
	return nil
}

// El PMT tiene que declarar exactamente las dos pistas que el muxer escribe.
// Un tercer stream declarado y sin datos deja a los consumidores esperando una
// pista que no existe.
func TestPMTDeclaresOnlyTheTracksItWrites(t *testing.T) {
	var buf bytes.Buffer
	m := newTSMuxer(&buf)
	if err := m.WriteVideo(0, []byte{0x00, 0x00, 0x00, 0x01, 0x67, 0x4d, 0x40, 0x1e}); err != nil {
		t.Fatalf("video: %v", err)
	}
	if err := m.WriteAudio(0, alawSilence); err != nil {
		t.Fatalf("audio: %v", err)
	}

	got := pmtStreams(t, buf.Bytes())
	want := [][2]int{{tsStreamTypeH264, int(tsPIDVideo)}, {tsStreamTypePCMA, int(tsPIDAudio)}}
	if len(got) != len(want) {
		t.Fatalf("%d streams %v, se esperaban %d %v", len(got), got, len(want), want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("stream %d es %v, se esperaba %v", i, got[i], want[i])
		}
	}
}
