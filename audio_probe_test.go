package main

import (
	"os"
	"testing"
)

// TestAudioProbe cuenta que tipos de paquete salen de un volcado y cuantos
// frames de video y audio da cada uno. Sirve para saber si el audio llega al
// demuxer o se pierde antes.
func TestAudioProbe(t *testing.T) {
	path := os.Getenv("WIBOX_AUDIO_PROBE")
	if path == "" {
		t.Skip("sin WIBOX_AUDIO_PROBE")
	}
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("leer: %v", err)
	}
	key := alignKey(t)
	tipos := map[byte]int{}
	video, audio, fallos := 0, 0, 0
	buf := raw
	for {
		p, used, derr := decodeQuiiPacket(key, buf)
		if derr == errShortPacket {
			break
		}
		if derr != nil || used == 0 {
			buf = buf[1:]
			continue
		}
		buf = buf[used:]
		tipos[p.Type]++
		if len(p.Media) > 0 {
			fr := demuxMedia(p.Media)
			if len(fr) == 0 && fallos < 5 {
				fallos++
				n := len(p.Media)
				if n > 24 {
					n = 24
				}
				t.Logf("sin frames: media=%d head=% x", len(p.Media), p.Media[:n])
			}
			for _, f := range fr {
				if f.Video {
					video++
				} else {
					audio++
				}
			}
		}
	}
	t.Logf("tipos=%v video=%d audio=%d", tipos, video, audio)
}
