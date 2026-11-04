package main

import (
	"bytes"
	"encoding/binary"
	"testing"
)

// buildCPacket fabrica un cpacket con el formato que documenta el demuxer:
// 00 00 01 <marker> + payloadLen + marca de tiempo + codec/fps/tamano.
func buildCPacket(marker byte, codec, fpsOrChan, widthOrRate, height int, bitstream []byte) []byte {
	h := make([]byte, 0x14)
	h[0], h[1], h[2] = 0, 0, 1
	h[3] = marker
	binary.LittleEndian.PutUint32(h[4:], uint32(len(bitstream)))
	// 2026-11-04 20:00:00 codificado en el formato del timbre.
	stamp := uint32((26 << 26) | (11 << 22) | (4 << 17) | (20 << 12) | (0 << 6) | 0)
	binary.LittleEndian.PutUint32(h[8:], stamp)
	binary.LittleEndian.PutUint16(h[12:], 500)
	h[14] = byte(codec)
	h[15] = byte(fpsOrChan)
	binary.LittleEndian.PutUint16(h[16:], uint16(widthOrRate))
	binary.LittleEndian.PutUint16(h[18:], uint16(height))
	return append(h, bitstream...)
}

func TestDemuxVideoFrame(t *testing.T) {
	nal := []byte{0, 0, 0, 1, 0x65, 0x88, 0x84, 0x00}
	payload := buildCPacket(0xE0, 1, 100, 640, 480, nal)
	frames := demuxMedia(payload)
	if len(frames) != 1 {
		t.Fatalf("frames=%d, esperaba 1", len(frames))
	}
	f := frames[0]
	if !f.Video {
		t.Fatal("esperaba video")
	}
	if f.Width != 640 || f.Height != 480 {
		t.Fatalf("tamano %dx%d", f.Width, f.Height)
	}
	if f.FPS != 25 {
		t.Fatalf("fps=%v, esperaba 25 (100/4)", f.FPS)
	}
	if !f.KeyFrame {
		t.Fatal("el NAL 5 es un IDR, deberia marcarse como key frame")
	}
	if !bytes.Equal(f.Bitstream, nal) {
		t.Fatalf("bitstream distinto: % x", f.Bitstream)
	}
}

func TestDemuxAudioFrame(t *testing.T) {
	samples := []byte{0xD5, 0x00, 0xFF}
	payload := buildCPacket(0xE3, audioCodecG711A, 0, 8000, 0, samples)
	frames := demuxMedia(payload)
	if len(frames) != 1 {
		t.Fatalf("frames=%d, esperaba 1", len(frames))
	}
	f := frames[0]
	if f.Video {
		t.Fatal("esperaba audio")
	}
	if f.Codec != audioCodecG711A || f.SampleRate != 8000 || f.Channels != 1 {
		t.Fatalf("codec=%d rate=%d ch=%d", f.Codec, f.SampleRate, f.Channels)
	}
}

func TestDemuxChainedFrames(t *testing.T) {
	video := buildCPacket(0xE0, 1, 100, 640, 480, []byte{0, 0, 1, 0x41, 0x11})
	audio := buildCPacket(0xE3, audioCodecG711A, 0, 8000, 0, []byte{0x11, 0x22})
	frames := demuxMedia(append(append([]byte{}, video...), audio...))
	if len(frames) != 2 {
		t.Fatalf("frames=%d, esperaba 2", len(frames))
	}
	if !frames[0].Video || frames[1].Video {
		t.Fatalf("orden incorrecto: %v %v", frames[0].Video, frames[1].Video)
	}
}

func TestDemuxIgnoresUnknownMarker(t *testing.T) {
	payload := buildCPacket(0xE0, 1, 100, 640, 480, []byte{0, 0, 1, 0x41})
	payload[3] = 0x00 // marcador fuera del rango 0xE0..0xEA
	if frames := demuxMedia(payload); len(frames) != 0 {
		t.Fatalf("frames=%d, esperaba 0", len(frames))
	}
}

func TestDemuxTruncatedPayloadStops(t *testing.T) {
	payload := buildCPacket(0xE0, 1, 100, 640, 480, []byte{0, 0, 1, 0x41, 0x22, 0x33})
	cut := payload[:len(payload)-2] // el bitstream ya no cabe entero
	if frames := demuxMedia(cut); len(frames) != 0 {
		t.Fatalf("frames=%d, esperaba 0 con payload truncado", len(frames))
	}
}

func TestContainsIDR(t *testing.T) {
	cases := []struct {
		name string
		in   []byte
		want bool
	}{
		{"idr 4 bytes", []byte{0, 0, 0, 1, 0x65, 0x00}, true},
		{"idr 3 bytes", []byte{0, 0, 1, 0x65, 0x00}, true},
		{"p frame", []byte{0, 0, 0, 1, 0x41, 0x00}, false},
		{"sin start code", []byte{0x65, 0x00, 0x00}, false},
	}
	for _, c := range cases {
		if got := containsIDR(c.in); got != c.want {
			t.Errorf("%s: got %v, want %v", c.name, got, c.want)
		}
	}
}
