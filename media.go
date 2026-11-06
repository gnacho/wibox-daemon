package main

// Media del timbre: cada paquete QUII de tipo 0xA0-0xA3 lleva dentro una lista
// de "cpackets" con cabecera propia. El prefijo 00 00 01 es el mismo marcador de
// inicio de NAL de H264, asi que el bitstream de video se puede reenviar tal cual.

const (
	audioCodecG711A = 4
	audioCodecG711U = 5
	audioCodecPCM   = 9
)

type mediaFrame struct {
	Video       bool
	Bitstream   []byte
	FrameType   int
	Codec       int
	Width       int
	Height      int
	FPS         float64
	SampleRate  int
	Channels    int
	TimestampMS int64
	KeyFrame    bool
}

type cpacketHeader struct {
	Marker      int
	FrameType   int
	PayloadLen  int
	TimestampMS int64
	Codec       int
	FPSOrChan   int
	WidthOrRate int
	Height      int
}

func readU16LE(src []byte, off int) int {
	return int(src[off]) | int(src[off+1])<<8
}

func readU32LE(src []byte, off int) int {
	return readU16LE(src, off) | readU16LE(src, off+2)<<16
}

func computeTimestampMS(payload []byte, off int) int64 {
	stamp := int64(readU32LE(payload, off+8)) & 0xFFFFFFFF
	ms := int64(readU16LE(payload, off+12))
	year := int((stamp>>26)&0x3F) + 2000
	month := int((stamp >> 22) & 0x0F)
	day := int((stamp >> 17) & 0x1F)
	hour := int((stamp >> 12) & 0x1F)
	minute := int((stamp >> 6) & 0x3F)
	second := int(stamp & 0x3F)
	// Solo se usa para trazas y orden, no para calcular duraciones, asi que
	// basta con aproximar el epoch. UTC para no depender de la zona horaria.
	epoch := epochFromFields(year, month, day, hour, minute, second)
	return epoch*1000 + ms
}

// epochFromFields convierte una fecha civil a segundos epoch sin usar time.Date,
// que necesitaria la zona horaria y aqui solo hace falta orden relativo.
func epochFromFields(year, month, day, hour, minute, second int) int64 {
	if month < 1 || month > 12 {
		return 0
	}
	days := daysFromCivil(year, month, day)
	return days*86400 + int64(hour*3600+minute*60+second)
}

func daysFromCivil(y, m, d int) int64 {
	if m <= 2 {
		y--
	}
	era := y / 400
	if y < 0 && y%400 != 0 {
		era--
	}
	yoe := y - era*400
	mp := (m + 9) % 12
	doy := (153*mp+2)/5 + d - 1
	doe := yoe*365 + yoe/4 - yoe/100 + doy
	return int64(era*146097 + doe - 719468)
}

func parseCPacketHeader(payload []byte, off int) (cpacketHeader, bool) {
	if len(payload) < off+0x14 {
		return cpacketHeader{}, false
	}
	if payload[off] != 0 || payload[off+1] != 0 || payload[off+2] != 1 {
		return cpacketHeader{}, false
	}
	marker := int(payload[off+3])
	if marker < 0xE0 || marker > 0xEA {
		return cpacketHeader{}, false
	}
	return cpacketHeader{
		Marker:      marker,
		FrameType:   (marker + 0x20) & 0xFF,
		PayloadLen:  readU32LE(payload, off+4),
		TimestampMS: computeTimestampMS(payload, off),
		Codec:       int(payload[off+14]),
		FPSOrChan:   int(payload[off+15]),
		WidthOrRate: readU16LE(payload, off+16),
		Height:      readU16LE(payload, off+18),
	}, true
}

// demuxMedia parte un payload de media en frames de video y audio.
func demuxMedia(payload []byte) []mediaFrame {
	frames := make([]mediaFrame, 0, 4)
	pos := 0
	for pos+0x14 <= len(payload) {
		hdr, ok := parseCPacketHeader(payload, pos)
		if !ok {
			break
		}
		end := pos + 0x14 + hdr.PayloadLen
		if end > len(payload) {
			// El tamano de la cabecera QUII excluye el relleno AES, asi que
			// el ultimo cpacket del paquete llega recortado hasta en 15
			// bytes. Se usa lo que hay en vez de tirar el frame entero:
			// medido, sin esto se perdian 12663 de 12797 paquetes.
			end = len(payload)
		}
		bitstream := payload[pos+0x14 : end]
		switch hdr.FrameType {
		case 0x00, 0x01, 0x09, 0x0A:
			isH264 := hdr.Codec == 1 || (hdr.Codec >= 0x10 && hdr.Codec <= 0x1F)
			if isH264 {
				frames = append(frames, mediaFrame{
					Video:       true,
					Bitstream:   bitstream,
					FrameType:   hdr.FrameType,
					Codec:       hdr.Codec,
					Width:       hdr.WidthOrRate,
					Height:      hdr.Height,
					FPS:         float64(hdr.FPSOrChan) / 4,
					TimestampMS: hdr.TimestampMS,
					KeyFrame:    containsIDR(bitstream),
				})
			}
		case 0x03:
			channels := hdr.FPSOrChan
			if channels == 0 {
				channels = 1
			}
			frames = append(frames, mediaFrame{
				FrameType:   hdr.FrameType,
				Bitstream:   bitstream,
				Codec:       hdr.Codec,
				SampleRate:  hdr.WidthOrRate,
				Channels:    channels,
				TimestampMS: hdr.TimestampMS,
			})
		}
		pos = end
	}
	return frames
}

// containsIDR busca un NAL de tipo 5 (IDR) dentro del bitstream.
// countNALTypes acumula los tipos de NAL (campo de 5 bits tras el start code)
// del bitstream, para poder decir si el flujo trae PPS (8) o IDR (5) sin
// tener que analizar el fichero a mano.
func countNALTypes(b []byte, acc map[int]int) {
	for i := 0; i+3 <= len(b); i++ {
		if b[i] != 0 || b[i+1] != 0 {
			continue
		}
		startLen := 0
		if b[i+2] == 1 {
			startLen = 3
		} else if b[i+2] == 0 && i+4 <= len(b) && b[i+3] == 1 {
			startLen = 4
		}
		if startLen == 0 {
			continue
		}
		nal := i + startLen
		if nal < len(b) {
			acc[int(b[nal]&0x1F)]++
		}
		i += startLen
	}
}

// stripParamSets devuelve el bitstream sin los NAL de SPS (7) y PPS (8): el
// muxer antepone los suyos, que son los que la app del timbre usa.
func stripParamSets(b []byte) []byte {
	out := make([]byte, 0, len(b))
	i := 0
	for i < len(b) {
		start := -1
		startLen := 0
		if i+3 <= len(b) && b[i] == 0 && b[i+1] == 0 && b[i+2] == 1 {
			start, startLen = i, 3
		} else if i+4 <= len(b) && b[i] == 0 && b[i+1] == 0 && b[i+2] == 0 && b[i+3] == 1 {
			start, startLen = i, 4
		}
		if start < 0 {
			i++
			continue
		}
		nal := i + startLen
		if nal >= len(b) {
			break
		}
		kind := b[nal] & 0x1F
		next := len(b)
		for j := nal + 1; j+3 <= len(b); j++ {
			if b[j] == 0 && b[j+1] == 0 && (b[j+2] == 1 || (b[j+2] == 0 && j+4 <= len(b) && b[j+3] == 1)) {
				next = j
				break
			}
		}
		if kind != 7 && kind != 8 {
			out = append(out, b[start:next]...)
		}
		i = next
	}
	return out
}

func containsIDR(b []byte) bool {
	for i := 0; i+4 <= len(b); i++ {
		if b[i] != 0 || b[i+1] != 0 {
			continue
		}
		startLen := 0
		if b[i+2] == 1 {
			startLen = 3
		} else if b[i+2] == 0 && i+4 <= len(b) && b[i+3] == 1 {
			startLen = 4
		}
		if startLen == 0 {
			continue
		}
		nal := i + startLen
		if nal < len(b) && b[nal]&0x1F == 5 {
			return true
		}
		i += startLen
	}
	return false
}
