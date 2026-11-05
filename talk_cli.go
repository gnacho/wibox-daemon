package main

import (
	"encoding/binary"
	"fmt"
	"log"
	"os"
	"time"
)

// readWAVPCM saca el PCM16 mono 8 kHz de un WAV sin dependencias: el timbre
// solo acepta ese formato en su altavoz.
func readWAVPCM(path string) ([]byte, error) {
	raw, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	if len(raw) < 12 || string(raw[0:4]) != "RIFF" || string(raw[8:12]) != "WAVE" {
		return nil, fmt.Errorf("%s no es un WAV", path)
	}
	var pcm []byte
	format, channels, rate, bits := 0, 0, 0, 0
	for off := 12; off+8 <= len(raw); {
		id := string(raw[off : off+4])
		size := int(binary.LittleEndian.Uint32(raw[off+4:]))
		body := off + 8
		if size < 0 || body+size > len(raw) {
			break
		}
		switch id {
		case "fmt ":
			if size >= 16 {
				format = int(binary.LittleEndian.Uint16(raw[body:]))
				channels = int(binary.LittleEndian.Uint16(raw[body+2:]))
				rate = int(binary.LittleEndian.Uint32(raw[body+4:]))
				bits = int(binary.LittleEndian.Uint16(raw[body+14:]))
			}
		case "data":
			pcm = raw[body : body+size]
		}
		off = body + size + size%2
	}
	if format != 1 || channels != 1 || rate != 8000 || bits != 16 {
		return nil, fmt.Errorf("%s tiene que ser PCM16 mono 8 kHz (formato=%d canales=%d rate=%d bits=%d)",
			path, format, channels, rate, bits)
	}
	if len(pcm) == 0 {
		return nil, fmt.Errorf("%s no lleva datos de audio", path)
	}
	return pcm, nil
}

// runTalk manda un WAV al altavoz del timbre. Solo funciona en la red local:
// el canal de vuelta no existe en el tunel remoto.
func runTalk(cfg config, key []byte, password, path string) error {
	if cfg.lanAddr == "" {
		return fmt.Errorf("el audio de vuelta solo va por LAN: falta WIBOX_LAN_ADDR")
	}
	pcm, err := readWAVPCM(path)
	if err != nil {
		return err
	}
	if len(key) != 32 {
		return fmt.Errorf("data-encode-key de %d bytes, se esperaban 32", len(key))
	}
	sess, err := dialTalk(cfg.lanAddr, key, password, cfg.channel, 5*time.Second)
	if err != nil {
		return err
	}
	defer sess.Close()
	log.Printf("talk: %d muestras (%.1f s) hacia %s", len(pcm)/2, float64(len(pcm)/2)/8000.0, cfg.lanAddr)
	if err := sess.SendPCM(pcm); err != nil {
		return err
	}
	log.Printf("talk: audio enviado")
	return nil
}
