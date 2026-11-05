package main

import (
	"encoding/binary"
	"io"
)

// Muxer MPEG-TS minimo: una pista de video H.264 (Annex-B) y otra de audio
// G.711A. La fuente exec: de go2rtc solo admite un flujo por pipe, y el
// demuxer de go2rtc descubre las pistas leyendo el PMT, asi que este muxer
// existe para que Home Assistant reciba imagen y sonido juntos.
const (
	tsPacketSize = 188
	tsSyncByte   = 0x47

	tsPIDPat   = 0x0000
	tsPIDPmt   = 0x1000
	tsPIDVideo = 0x0100
	tsPIDAudio = 0x0101

	tsStreamTypeH264 = 0x1B
	tsStreamTypePCMA = 0x90
)

type tsMuxer struct {
	w        io.Writer
	cc       map[uint16]byte
	lastPTS  map[uint16]uint32
	written  bool
	patPMT   []byte
	videoPID uint16
	audioPID uint16
}

func newTSMuxer(w io.Writer) *tsMuxer {
	m := &tsMuxer{
		w:        w,
		cc:       map[uint16]byte{},
		lastPTS:  map[uint16]uint32{},
		videoPID: tsPIDVideo,
		audioPID: tsPIDAudio,
	}
	m.patPMT = append(m.buildPAT(), m.buildPMT()...)
	return m
}

// crc32MPEG es el CRC de las tablas PSI (polinomio 0x04C11DB7, sin reflejar y
// sin xor final), distinto del CRC32 de IEEE.
func crc32MPEG(data []byte) uint32 {
	crc := uint32(0xFFFFFFFF)
	for _, b := range data {
		crc ^= uint32(b) << 24
		for i := 0; i < 8; i++ {
			if crc&0x80000000 != 0 {
				crc = (crc << 1) ^ 0x04C11DB7
			} else {
				crc <<= 1
			}
		}
	}
	return crc
}

func (m *tsMuxer) buildPAT() []byte {
	section := []byte{
		0x00, // table_id: program association
		0xB0, 0x0D,
		0x00, 0x01, // transport_stream_id
		0xC1,       // version 0, current_next 1
		0x00, 0x00, // section 0 de 0
		0x00, 0x01, // program_number 1
		0xE0 | byte(tsPIDPmt>>8), byte(tsPIDPmt & 0xFF),
	}
	crc := crc32MPEG(section)
	section = append(section, byte(crc>>24), byte(crc>>16), byte(crc>>8), byte(crc))
	return m.packSection(tsPIDPat, section)
}

func (m *tsMuxer) buildPMT() []byte {
	body := []byte{
		0x02,       // table_id: program map
		0x00, 0x00, // section_length (se rellena abajo)
		0x00, 0x01, // program_number
		0xC1,       // version 0, current_next 1
		0x00, 0x00, // section 0 de 0
		0xE0 | byte(m.videoPID>>8), byte(m.videoPID & 0xFF), // PCR PID
		0xF0, 0x00, // program_info_length 0
		tsStreamTypeH264, 0xE0 | byte(m.videoPID>>8), byte(m.videoPID & 0xFF), 0xF0, 0x00,
		tsStreamTypePCMA, 0xE0 | byte(m.audioPID>>8), byte(m.audioPID & 0xFF), 0xF0, 0x00,
	}
	// section_length = bytes despues del campo hasta el final del CRC
	length := len(body) - 3 + 4
	body[1] = 0xB0 | byte((length>>8)&0x0F)
	body[2] = byte(length & 0xFF)
	crc := crc32MPEG(body)
	body = append(body, byte(crc>>24), byte(crc>>16), byte(crc>>8), byte(crc))
	return m.packSection(tsPIDPmt, body)
}

// packSection coloca una seccion PSI en paquetes TS con el campo de puntero a
// cero, rellenando con 0xFF.
func (m *tsMuxer) packSection(pid uint16, section []byte) []byte {
	payload := append([]byte{0x00}, section...)
	out := make([]byte, 0, tsPacketSize*2)
	first := true
	for len(payload) > 0 {
		pkt := make([]byte, tsPacketSize)
		pkt[0] = tsSyncByte
		var b1 byte
		if first {
			b1 = 0x40
		}
		pkt[1] = b1 | byte(pid>>8)&0x1F
		pkt[2] = byte(pid)
		pkt[3] = 0x10 | (m.cc[pid] & 0x0F)
		m.cc[pid]++
		n := copy(pkt[4:], payload)
		for i := 4 + n; i < tsPacketSize; i++ {
			pkt[i] = 0xFF
		}
		payload = payload[n:]
		first = false
		out = append(out, pkt...)
	}
	return out
}

// writePES empaqueta un PES completo en paquetes TS de 188 bytes.
func (m *tsMuxer) writePES(pid uint16, streamID byte, pts uint32, payload []byte) error {
	pesLen := 3 + 5 + len(payload)
	pes := make([]byte, 6+3+5, 6+3+5+len(payload))
	pes[0], pes[1], pes[2] = 0x00, 0x00, 0x01
	pes[3] = streamID
	if pesLen <= 0xFFFF {
		binary.BigEndian.PutUint16(pes[4:], uint16(pesLen))
	}
	pes[6] = 0x80 // marcador '10'
	pes[7] = 0x80 // solo PTS
	pes[8] = 5    // longitud de la cabecera opcional
	writeTimestamp(pes[9:], pts)
	pes = append(pes, payload...)

	first := true
	for len(pes) > 0 {
		pkt := make([]byte, tsPacketSize)
		pkt[0] = tsSyncByte
		var b1 byte
		if first {
			b1 = 0x40 // payload_unit_start_indicator
		}
		pkt[1] = b1 | byte(pid>>8)&0x1F
		pkt[2] = byte(pid)

		body := pkt[4:]
		if len(pes) >= len(body) {
			pkt[3] = 0x10 | (m.cc[pid] & 0x0F)
			copy(body, pes[:len(body)])
			pes = pes[len(body):]
		} else {
			// Adaptation field de relleno para que el paquete quede lleno.
			afLen := len(body) - 1 - len(pes)
			pkt[3] = 0x30 | (m.cc[pid] & 0x0F)
			body[0] = byte(afLen)
			for i := 1; i <= afLen; i++ {
				body[i] = 0xFF
			}
			copy(body[1+afLen:], pes)
			pes = nil
		}
		m.cc[pid]++
		if _, err := m.w.Write(pkt); err != nil {
			return err
		}
		first = false
	}
	return nil
}

// writeTimestamp codifica un PTS de 33 bits con los bits marcadores del
// estandar.
func writeTimestamp(b []byte, t uint32) {
	b[0] = 0x21 | byte((t>>29)&0x0E)
	b[1] = byte(t >> 22)
	b[2] = 0x01 | byte((t>>14)&0xFE)
	b[3] = byte(t >> 7)
	b[4] = 0x01 | byte((t<<1)&0xFE)
}

// WriteVideo y WriteAudio escriben un frame como PES. El PTS se calcula en
// base de 90 kHz a partir de los milisegundos del demuxer.
func (m *tsMuxer) WriteVideo(ptsMS int64, annexB []byte) error {
	if len(annexB) == 0 {
		return nil
	}
	if err := m.writeHeaderIfNeeded(); err != nil {
		return err
	}
	return m.writePES(m.videoPID, 0xE0, m.ptsFor(m.videoPID, ptsMS), annexB)
}

func (m *tsMuxer) WriteAudio(ptsMS int64, alaw []byte) error {
	if len(alaw) == 0 {
		return nil
	}
	if err := m.writeHeaderIfNeeded(); err != nil {
		return err
	}
	return m.writePES(m.audioPID, 0xC0, m.ptsFor(m.audioPID, ptsMS), alaw)
}

func (m *tsMuxer) writeHeaderIfNeeded() error {
	if m.written {
		return nil
	}
	m.written = true
	_, err := m.w.Write(m.patPMT)
	return err
}

// ptsFor mantiene el PTS monotono por PID: un retroceso de la marca de tiempo
// del demuxer hace que los reproductores descarten el frame.
func (m *tsMuxer) ptsFor(pid uint16, ptsMS int64) uint32 {
	pts := uint32(ptsMS) * 90
	if ptsMS < 0 {
		pts = 0
	}
	if last, ok := m.lastPTS[pid]; ok && int32(pts-last) < 0 {
		pts = last
	}
	m.lastPTS[pid] = pts
	return pts & 0x1FFFFFFF
}
