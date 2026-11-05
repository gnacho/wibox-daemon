package main

import (
	"bytes"
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
	// El cuerpo va cifrado en bloques de 16 y la cabecera anuncia el tamano
	// real, que incluye hasta 15 bytes de cola fuera de bloque (el timbre real
	// anuncia 180 para 176 cifrados). Se replica ese caso.
	body := zeroPad(media)
	enc, err := aesCBC(key, body, true)
	if err != nil {
		t.Fatalf("cifrando cuerpo: %v", err)
	}
	binary.LittleEndian.PutUint32(h[11:], uint32(len(enc)+4))
	encH, err := aesCBC(key, h, true)
	if err != nil {
		t.Fatalf("cifrando cabecera: %v", err)
	}
	return append(append(encH, enc...), 0, 0, 0, 0)
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

// Una cabecera basura que declara un tamano grande no debe atascar el bucle:
// con el buffer por encima del limite se avanza y se recupera el paquete bueno.
func TestConsumeQuiiPacketsRecoversFromStalledHeader(t *testing.T) {
	key := bytes.Repeat([]byte{7}, 32)
	media := buildCPacket(0xE0, 1, 100, 640, 480, []byte{0, 0, 0, 1, 0x65, 1, 2, 3})
	good := quiiMediaPacket(t, key, media)

	// Cabecera descifrable como media con tamano enorme y nunca completada.
	stall := make([]byte, 32)
	clearHeader := make([]byte, 32)
	clearHeader[0] = 0xA0
	binary.LittleEndian.PutUint32(clearHeader[11:], 900000)
	enc, err := aesCBC(key, clearHeader, true)
	if err != nil {
		t.Fatalf("cifrando cabecera: %v", err)
	}
	copy(stall, enc)

	buf := append(append([]byte{}, stall...), good...)
	buf = append(buf, make([]byte, 600*1024)...)

	frames, rest := consumeQuiiPackets(key, buf)
	if len(frames) != 1 {
		t.Fatalf("frames=%d, se esperaba recuperar 1 tras el atasco", len(frames))
	}
	if !frames[0].KeyFrame {
		t.Fatal("el frame recuperado deberia ser keyframe")
	}
	_ = rest
}
