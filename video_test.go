package main

import (
	"encoding/binary"
	"testing"
)

// quiiMediaPacket arma un paquete QUII de media tal como lo manda el timbre:
// cabecera de 32 bytes cifrada y cuerpo de media en claro.
func quiiMediaPacket(t *testing.T, key []byte, media []byte) []byte {
	t.Helper()
	h := make([]byte, 32)
	h[0] = 0xA1 // tipo de media
	binary.LittleEndian.PutUint16(h[9:], 0)
	binary.LittleEndian.PutUint32(h[11:], uint32(len(media)))
	enc, err := aesCBC(key, h, true)
	if err != nil {
		t.Fatalf("cifrando cabecera: %v", err)
	}
	return append(enc, media...)
}

func TestConsumeQuiiPacketsReturnsFrames(t *testing.T) {
	key := make([]byte, 32)
	media := buildCPacket(0xE1, 1, 25, 640, 480, []byte{0x00, 0x00, 0x00, 0x01, 0x65, 0x88})
	pkt := quiiMediaPacket(t, key, media)

	frames, rest := consumeQuiiPackets(key, pkt)
	if len(frames) != 1 {
		t.Fatalf("frames=%d, esperaba 1", len(frames))
	}
	if !frames[0].Video || frames[0].Width != 640 || frames[0].Height != 480 {
		t.Fatalf("frame inesperado: %+v", frames[0])
	}
	if !frames[0].KeyFrame {
		t.Fatal("esperaba key frame por el NAL 0x65")
	}
	if len(rest) != 0 {
		t.Fatalf("resto=%d bytes, esperaba 0", len(rest))
	}
}

// El caso que colgaba el bucle: basura que no descifra a un tamano creible.
func TestConsumeQuiiPacketsResyncsOnGarbage(t *testing.T) {
	key := make([]byte, 32)
	media := buildCPacket(0xE1, 1, 25, 640, 480, []byte{0x00, 0x00, 0x00, 0x01, 0x41, 0x01})
	pkt := quiiMediaPacket(t, key, media)

	garbage := []byte{0xDE, 0xAD, 0xBE, 0xEF, 0x11, 0x22, 0x33}
	frames, rest := consumeQuiiPackets(key, append(garbage, pkt...))
	if len(frames) != 1 {
		t.Fatalf("frames=%d, esperaba 1 tras resincronizar", len(frames))
	}
	if len(rest) != 0 {
		t.Fatalf("resto=%d bytes, esperaba 0", len(rest))
	}
}

func TestConsumeQuiiPacketsKeepsIncompleteTail(t *testing.T) {
	key := make([]byte, 32)
	media := buildCPacket(0xE1, 1, 25, 640, 480, []byte{0x00, 0x00, 0x00, 0x01, 0x65, 0x88})
	pkt := quiiMediaPacket(t, key, media)

	frames, rest := consumeQuiiPackets(key, pkt[:len(pkt)-3])
	if len(frames) != 0 {
		t.Fatalf("frames=%d, no deberia haber paquete completo", len(frames))
	}
	if len(rest) != len(pkt)-3 {
		t.Fatalf("resto=%d bytes, esperaba %d", len(rest), len(pkt)-3)
	}
}
