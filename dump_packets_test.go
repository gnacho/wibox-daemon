package main

import (
	"os"
	"testing"
)

// TestDumpPackets cuenta TODOS los paquetes QUII del volcado, no solo los que el
// demuxer acepta. Sirve para ver que se esta descartando: si el PPS y el IDR
// viajan en un cpacket de otro tipo, aqui aparecen.
func TestDumpPackets(t *testing.T) {
	path := os.Getenv("WIBOX_PACKETS")
	if path == "" {
		t.Skip("define WIBOX_PACKETS=<volcado> para analizar paquetes")
	}
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("leer %s: %v", path, err)
	}
	key := alignKey(t)

	type stat struct {
		count  int
		first  []byte
		maxLen int
		all    []byte
	}
	tally := map[string]*stat{}
	add := func(kind string, body []byte) {
		s := tally[kind]
		if s == nil {
			s = &stat{first: append([]byte{}, body...)}
			tally[kind] = s
		}
		s.count++
		s.all = append(s.all, body...)
		if len(body) > s.maxLen {
			s.maxLen = len(body)
		}
	}

	// Los volcados nuevos guardan ya payloads QUII limpios; los viejos llevan
	// frames KCP dentro.
	var chunks [][]byte
	for _, f := range walkRbFrames(raw) {
		chunks = append(chunks, f.Payload)
	}
	if len(chunks) == 0 {
		chunks = append(chunks, raw)
	}

	var buf []byte
	for _, chunk := range chunks {
		buf = append(buf, chunk...)
		for {
			p, used, err := decodeQuiiPacket(key, buf)
			if err != nil || used == 0 {
				if len(buf) > 0 {
					buf = buf[1:]
					continue
				}
				break
			}
			buf = buf[used:]
			switch {
			case p.Type >= 0xA0 && p.Type <= 0xA3:
				h, ok := parseCPacketHeader(p.Media, 0)
				if !ok {
					add("media-sin-cpacket", p.Media)
					continue
				}
				kind := "cpacket marker=" + hexByte(byte(h.Marker)) +
					" frameType=" + hexByte(byte(h.FrameType)) +
					" codec=" + hexByte(byte(h.Codec))
				add(kind, p.Media)
			default:
				add("quii-type="+hexByte(p.Type), p.Command)
			}
		}
	}
	for k, s := range tally {
		head := s.first
		if len(head) > 24 {
			head = head[:24]
		}
		nal := map[byte]int{}
		for i := 0; i+3 < len(s.all); i++ {
			if s.all[i] == 0 && s.all[i+1] == 0 && s.all[i+2] == 1 {
				nal[s.all[i+3]&0x1f]++
			}
		}
		t.Logf("%-46s count=%-5d maxLen=%-6d nal=%v head=% x", k, s.count, s.maxLen, nal, head)
	}
}

func hexByte(b byte) string {
	const hexdigits = "0123456789abcdef"
	return string([]byte{hexdigits[b>>4], hexdigits[b&0x0f]})
}
