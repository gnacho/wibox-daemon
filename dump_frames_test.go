package main

import (
	"encoding/hex"
	"os"
	"testing"
)

// TestDumpFrames recorre un volcado crudo del tunel y cuenta los frames que sale
// el pipeline real (frames KCP -> paquete QUII de media -> cpacket -> demuxer).
// Se salta salvo que se indique el fichero en WIBOX_DUMP.
func TestDumpFrames(t *testing.T) {
	path := os.Getenv("WIBOX_DUMP")
	if path == "" {
		t.Skip("WIBOX_DUMP no definido")
	}
	keyHex := os.Getenv("WIBOX_DATA_KEY")
	var key []byte
	var err error
	if keyHex != "" {
		if key, err = hex.DecodeString(keyHex); err != nil {
			t.Fatalf("clave: %v", err)
		}
	} else {
		// Sin clave en el entorno se pide al cloud, que es lo natural cuando
		// se analiza un volcado de verdad.
		cfg := loadConfig()
		clientID = cfg.clientID
		d := &daemon{cfg: cfg, auth: NewUserAuth(cfg.baseURL, cfg.clientID)}
		if err := d.login(); err != nil {
			t.Skipf("login: %v", err)
		}
		if key, err = readKeyFromCheck(cfg, d); err != nil {
			t.Skipf("clave del token: %v", err)
		}
	}
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("leyendo volcado: %v", err)
	}

	// El volcado cambia de forma segun la version del daemon que lo escribio:
	// las viejas guardaban el buffer crudo del tunel (con frames KCP dentro) y
	// las nuevas guardan lo que sale de incoming(), que ya son payloads QUII
	// limpios. Si no hay frames KCP se pasa el fichero tal cual al parser.
	var chunks [][]byte
	for _, f := range walkRbFrames(raw) {
		chunks = append(chunks, f.Payload)
	}
	if len(chunks) == 0 {
		chunks = append(chunks, raw)
	}

	var buf []byte
	var all []byte
	var audioAll []byte
	var video, audio, keyFrames int
	var first *mediaFrame
	var firstAudio *mediaFrame
	for _, chunk := range chunks {
		buf = append(buf, chunk...)
		frames, rest := consumeQuiiPackets(key, buf)
		buf = rest
		for i := range frames {
			fr := frames[i]
			if fr.Video {
				video++
				all = append(all, fr.Bitstream...)
				if fr.KeyFrame {
					keyFrames++
				}
				if first == nil {
					first = &fr
				}
			} else {
				audio++
				audioAll = append(audioAll, fr.Bitstream...)
				if firstAudio == nil {
					firstAudio = &fr
				}
			}
		}
	}
	if first == nil {
		t.Fatalf("0 frames de video en %s (%d bytes)", path, len(raw))
	}
	t.Logf("video=%d key=%d audio=%d primero=%dx%d fps=%.1f codec=%d",
		video, keyFrames, audio, first.Width, first.Height, first.FPS, first.Codec)
	// Tipos NAL del flujo completo: si faltan PPS (8) o IDR (5) el H264 no es
	// decodificable aunque el demuxer saque frames.
	nal := map[byte]int{}
	for i := 0; i+3 < len(all); i++ {
		if all[i] == 0 && all[i+1] == 0 && all[i+2] == 1 {
			nal[all[i+3]&0x1f]++
		}
	}
	t.Logf("bitstream=%d bytes nal=%v", len(all), nal)
	if out := os.Getenv("WIBOX_DUMP_H264"); out != "" {
		if err := os.WriteFile(out, all, 0o644); err == nil {
			t.Logf("volcado h264 en %s", out)
		}
	}
	if firstAudio != nil {
		// El audio del timbre es G711A (codec 4). Estas trazas sirven para
		// comprobar la frecuencia y los canales antes de cablearlo a Home
		// Assistant, sin depender de que el timbre este despierto.
		t.Logf("audio=%d bytes codec=%d rate=%d canales=%d",
			len(audioAll), firstAudio.Codec, firstAudio.SampleRate, firstAudio.Channels)
		if out := os.Getenv("WIBOX_DUMP_AUDIO"); out != "" {
			if err := os.WriteFile(out, audioAll, 0o644); err == nil {
				t.Logf("volcado audio en %s", out)
			}
		}
	}
}
