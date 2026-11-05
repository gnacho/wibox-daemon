package main

import (
	"bytes"
	"os"
	"testing"
)

// checkTSPackets valida la estructura basica de un flujo TS: paquetes de 188
// con byte de sincronismo y continuidad correcta por PID.
func checkTSPackets(t *testing.T, raw []byte) {
	t.Helper()
	if len(raw) == 0 || len(raw)%tsPacketSize != 0 {
		t.Fatalf("el flujo no son paquetes de %d bytes: %d", tsPacketSize, len(raw))
	}
	lastCC := map[uint16]int{}
	for off := 0; off < len(raw); off += tsPacketSize {
		pkt := raw[off : off+tsPacketSize]
		if pkt[0] != tsSyncByte {
			t.Fatalf("paquete en %d sin byte de sincronismo: 0x%02x", off, pkt[0])
		}
		pid := uint16(pkt[1]&0x1F)<<8 | uint16(pkt[2])
		if pkt[3]&0x10 == 0 {
			continue
		}
		cc := int(pkt[3] & 0x0F)
		if prev, ok := lastCC[pid]; ok && (prev+1)&0x0F != cc {
			t.Fatalf("continuidad rota en pid %#x: %d -> %d", pid, prev, cc)
		}
		lastCC[pid] = cc
	}
}

func TestTSMuxerWritesPATAndPMT(t *testing.T) {
	var buf bytes.Buffer
	m := newTSMuxer(&buf)
	if err := m.WriteVideo(1000, []byte{0x00, 0x00, 0x01, 0x65, 0xAA}); err != nil {
		t.Fatalf("video: %v", err)
	}
	raw := buf.Bytes()
	checkTSPackets(t, raw)

	if pid := uint16(raw[1]&0x1F)<<8 | uint16(raw[2]); pid != tsPIDPat {
		t.Fatalf("el primer paquete no es el PAT: pid %#x", pid)
	}
	pmtOff := tsPacketSize
	if pid := uint16(raw[pmtOff+1]&0x1F)<<8 | uint16(raw[pmtOff+2]); pid != tsPIDPmt {
		t.Fatalf("el segundo paquete no es el PMT: pid %#x", pid)
	}
	// Seccion PMT: 0x02 al principio del payload (tras el puntero a cero).
	section := raw[pmtOff+5:]
	if section[0] != 0x02 {
		t.Fatalf("table_id del PMT = 0x%02x", section[0])
	}
	// total = cabecera de 3 bytes + section_length declarada.
	total := 3 + int(section[1]&0x0F)<<8 + int(section[2])
	crc := crc32MPEG(section[:total-4])
	got := uint32(section[total-4])<<24 | uint32(section[total-3])<<16 |
		uint32(section[total-2])<<8 | uint32(section[total-1])
	if crc != got {
		t.Fatalf("CRC del PMT mal: calculado %#x, escrito %#x", crc, got)
	}
	// Las dos pistas tienen que anunciarse o go2rtc no descubre el audio.
	if !bytes.Contains(raw[pmtOff:pmtOff+tsPacketSize], []byte{tsStreamTypeH264}) {
		t.Fatal("el PMT no anuncia H264")
	}
	if !bytes.Contains(raw[pmtOff:pmtOff+tsPacketSize], []byte{tsStreamTypePCMA}) {
		t.Fatal("el PMT no anuncia PCMA")
	}
}

func TestTSMuxerWritesHeaderOnce(t *testing.T) {
	var buf bytes.Buffer
	m := newTSMuxer(&buf)
	if err := m.WriteAudio(0, []byte{0x11}); err != nil {
		t.Fatalf("audio: %v", err)
	}
	first := buf.Len()
	if err := m.WriteAudio(20, []byte{0x22}); err != nil {
		t.Fatalf("audio: %v", err)
	}
	raw := buf.Bytes()
	if bytes.Count(raw, []byte{0x47, 0x40, 0x00}) < 1 {
		t.Fatal("no hay paquete PAT")
	}
	// Un segundo PAT/PMT por frame duplicaria la cabecera: solo puede haber
	// una pareja en todo el flujo.
	if n := bytes.Count(raw[:], []byte{tsSyncByte, 0x40, 0x00}); n != 1 {
		t.Fatalf("PAT repetido %d veces", n)
	}
	if buf.Len() <= first {
		t.Fatal("el segundo frame no se escribio")
	}
}

func TestTSMuxerPTSIsMonotonic(t *testing.T) {
	var buf bytes.Buffer
	m := newTSMuxer(&buf)
	// El demuxer puede entregar marcas que retroceden: el PTS escrito nunca
	// puede hacerlo o el reproductor descarta el frame.
	seq := []int64{1000, 900, 1100}
	for _, ms := range seq {
		if err := m.WriteVideo(ms, []byte{0x00, 0x00, 0x01, 0x41}); err != nil {
			t.Fatalf("video: %v", err)
		}
	}
	if m.lastPTS[tsPIDVideo] != uint32(1100*90)&0x1FFFFFFF {
		t.Fatalf("PTS final %d", m.lastPTS[tsPIDVideo])
	}
	checkTSPackets(t, buf.Bytes())
}

func TestTSMuxerEmptyPayloadWritesNothing(t *testing.T) {
	var buf bytes.Buffer
	m := newTSMuxer(&buf)
	if err := m.WriteVideo(0, nil); err != nil {
		t.Fatalf("video: %v", err)
	}
	if err := m.WriteAudio(0, nil); err != nil {
		t.Fatalf("audio: %v", err)
	}
	if buf.Len() != 0 {
		t.Fatalf("un flujo vacio escribio %d bytes", buf.Len())
	}
}

// TestTSMuxerDumpAudio escribe un MPEG-TS de solo audio a partir de un fichero
// G.711A crudo, para comprobar el muxer con un reproductor externo (ffprobe).
func TestTSMuxerDumpAudio(t *testing.T) {
	in := os.Getenv("WIBOX_TS_ALAW")
	out := os.Getenv("WIBOX_TS_DUMP")
	if in == "" || out == "" {
		t.Skip("sin WIBOX_TS_ALAW y WIBOX_TS_DUMP")
	}
	raw, err := os.ReadFile(in)
	if err != nil {
		t.Fatalf("leyendo %s: %v", in, err)
	}
	f, err := os.Create(out)
	if err != nil {
		t.Fatalf("creando %s: %v", out, err)
	}
	defer f.Close()

	m := newTSMuxer(f)
	const frameBytes = 160 // 20 ms a 8 kHz
	var pts int64
	for off := 0; off+frameBytes <= len(raw); off += frameBytes {
		if err := m.WriteAudio(pts, raw[off:off+frameBytes]); err != nil {
			t.Fatalf("audio: %v", err)
		}
		pts += 20
	}
	if err := f.Close(); err != nil {
		t.Fatalf("cerrando: %v", err)
	}
	t.Logf("escrito %s con %d ms de audio", out, pts)
}

// TestTSMuxerFromPipeline lleva dos cpackets (video con IDR y audio G711A)
// por el pipeline real (paquete QUII de media -> demuxer) y los muxea a TS.
// Es la prueba de extremo a extremo del muxer sin depender del timbre ni de un
// volcado, porque la data key del token rota y los volcados viejos ya no se
// pueden descifrar.
func TestTSMuxerFromPipeline(t *testing.T) {
	key := []byte("0123456789abcdef0123456789abcdef")

	videoNAL := []byte{0x00, 0x00, 0x00, 0x01, 0x65, 0x88, 0x84, 0x00, 0x10}
	video := quiiMediaPacket(t, key, buildCPacket(0xE1, 1, 25, 640, 480, videoNAL))
	audioPCM := bytes.Repeat([]byte{0xD5}, 160)
	audio := quiiMediaPacket(t, key, buildCPacket(0xE3, 4, 1, 8000, 0, audioPCM))

	frames, tail := consumeQuiiPackets(key, append(append([]byte{}, video...), audio...))
	if len(tail) != 0 {
		t.Fatalf("quedaron %d bytes sin consumir", len(tail))
	}
	if len(frames) != 2 {
		t.Fatalf("esperaba 2 frames, obtuve %d", len(frames))
	}

	var out bytes.Buffer
	m := newTSMuxer(&out)
	params := annexBParams()
	for i, f := range frames {
		if f.Video {
			if i == 0 {
				if err := m.WriteVideo(f.TimestampMS, params); err != nil {
					t.Fatalf("sps/pps: %v", err)
				}
			}
			if err := m.WriteVideo(f.TimestampMS, f.Bitstream); err != nil {
				t.Fatalf("video: %v", err)
			}
			continue
		}
		if err := m.WriteAudio(f.TimestampMS, f.Bitstream); err != nil {
			t.Fatalf("audio: %v", err)
		}
	}
	raw := out.Bytes()
	checkTSPackets(t, raw)
	if !bytes.Contains(raw, []byte{0x1B}) || !bytes.Contains(raw, []byte{0x90}) {
		t.Fatalf("el PMT no anuncia las dos pistas")
	}
	t.Logf("TS de %d bytes con video y audio", len(raw))
}

func TestTSMuxerNeverRepeatsAPTS(t *testing.T) {
	var buf bytes.Buffer
	m := newTSMuxer(&buf)
	sample := []byte{0x00, 0x00, 0x00, 0x01, 0x65, 0x01}
	for i := 0; i < 4; i++ {
		if err := m.WriteVideo(1000, sample); err != nil {
			t.Fatalf("WriteVideo: %v", err)
		}
	}
	first := m.lastPTS[tsPIDVideo]
	if first == 0 {
		t.Fatal("sin marca de tiempo")
	}
	// Cuatro frames con la misma marca deben quedar en cuatro valores
	// distintos y crecientes; el ultimo es el primero mas tres ticks.
	if first != uint32(1000*90)+3 {
		t.Fatalf("pts final = %d, se esperaba %d", first, 1000*90+3)
	}
}
