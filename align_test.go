package main

import (
	"encoding/binary"
	"encoding/hex"
	"fmt"
	"os"
	"testing"
)

// Prueba empirica del encuadre: para cada estrategia de avance, se intenta
// recorrer el payload de cada frame KCP paquete a paquete y se cuenta cuantos
// frames se consumen exactos. La estrategia que acierte es la buena.
func TestAlignStrategies(t *testing.T) {
	path := os.Getenv("WIBOX_ALIGN")
	if path == "" {
		t.Skip("define WIBOX_ALIGN=<volcado> para medir encuadres")
	}
	key := alignKey(t)
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read: %v", err)
	}
	frames := walkRbFrames(raw)
	t.Logf("volcado %s: %d frames KCP", path, len(frames))

	steps := []struct {
		name string
		fn   func(media int) int
	}{
		{"32+media", func(m int) int { return 32 + m }},
		{"32+pad", func(m int) int { return 32 + (m+15)/16*16 }},
		{"32+pad+4", func(m int) int { return 32 + (m+15)/16*16 + 4 }},
		{"32+media+4", func(m int) int { return 32 + m + 4 }},
		{"pad+4", func(m int) int { return (m+15)/16*16 + 4 }},
		{"media+4", func(m int) int { return m + 4 }},
	}

	for _, st := range steps {
		framesOK, packets, mismatch := 0, 0, 0
		for _, f := range frames {
			pl := f.Payload
			off := 0
			ok := true
			for {
				if off == len(pl) {
					break
				}
				if off+32 > len(pl) {
					ok = false
					break
				}
				hdr, herr := aesCBC(key, pl[off:off+32], false)
				if herr != nil || !plausibleQuiiType(hdr[0]) {
					ok = false
					break
				}
				media := int(binary.LittleEndian.Uint32(hdr[11:]))
				step := st.fn(media)
				if step <= 0 || media < 0 || media > 65535 {
					ok = false
					break
				}
				packets++
				off += step
				if off > len(pl) {
					ok = false
					break
				}
			}
			if ok && off == len(pl) {
				framesOK++
			} else {
				mismatch++
			}
		}
		fmt.Printf("estrategia %-12s frames exactos=%4d/%4d paquetes=%5d fallos=%4d\n",
			st.name, framesOK, len(frames), packets, mismatch)
	}
}

// alignKey saca la data key del entorno (hex) o del cloud, sin imprimirla.
func alignKey(t *testing.T) []byte {
	if h := os.Getenv("WIBOX_DATA_KEY"); h != "" {
		b, err := hex.DecodeString(h)
		if err != nil {
			t.Fatalf("WIBOX_DATA_KEY: %v", err)
		}
		return b
	}
	cfg := loadConfig()
	if cfg.account == "" || cfg.deviceID == "" {
		t.Skip("sin WIBOX_DATA_KEY y sin credenciales")
	}
	d := &daemon{cfg: cfg, auth: NewUserAuth(cfg.baseURL, cfg.clientID)}
	if err := d.login(); err != nil {
		t.Skipf("login: %v", err)
	}
	key, err := readKeyFromCheck(cfg, d)
	if err != nil {
		t.Skipf("key: %v", err)
	}
	return key
}
