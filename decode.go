package main

import (
	"encoding/binary"
	"fmt"
	"io"
	"os"
)

// rbFrame es un frame KCP encontrado dentro de un volcado del tunel.
type rbFrame struct {
	Offset     int
	Total      int
	Command    uint32
	Seq        uint32
	DestID     uint32
	SrcID      uint32
	PayloadLen int
	Payload    []byte
}

// walkRbFrames recorre un volcado crudo buscando frames KCP (CMD_DATA) con la
// longitud coherente. Sirve para analizar volcados hechos con WIBOX_RAW_DUMP.
func walkRbFrames(raw []byte) []rbFrame {
	var out []rbFrame
	for i := 0; i+0x38 <= len(raw); i++ {
		if binary.LittleEndian.Uint32(raw[i:]) != 0xFFFFFFFF {
			continue
		}
		total := int(binary.LittleEndian.Uint32(raw[i+4:]))
		if total < 0x38 || i+total > len(raw) {
			continue
		}
		f := rbFrame{
			Offset:     i,
			Total:      total,
			Command:    binary.LittleEndian.Uint32(raw[i+0x14:]),
			Seq:        binary.LittleEndian.Uint32(raw[i+0x18:]),
			DestID:     binary.LittleEndian.Uint32(raw[i+0x28:]),
			SrcID:      binary.LittleEndian.Uint32(raw[i+0x2C:]),
			PayloadLen: int(binary.LittleEndian.Uint32(raw[i+0x24:])),
		}
		if f.Command != rbCmdData {
			continue
		}
		// El campo 0x24 es total-0x28 (incluye 0x10 de cabecera), asi que el
		// payload real es lo que queda hasta el final del frame.
		if total <= 0x38 {
			continue
		}
		f.Payload = raw[i+0x38 : i+total]
		out = append(out, f)
		i += total - 1
	}
	return out
}

// decodeDump intenta entender el contenido de los payloads de media: descifra
// la cabecera QUII con la clave del token y describe lo que encuentra.
func decodeDump(path string, key []byte, limit int) error {
	raw, err := os.ReadFile(path)
	if err != nil {
		return err
	}
	frames := walkRbFrames(raw)
	fmt.Printf("volcado %s: %d bytes, %d frames KCP\n", path, len(raw), len(frames))
	if len(frames) == 0 {
		return nil
	}
	shown := 0
	for _, f := range frames {
		if shown >= limit {
			break
		}
		if len(f.Payload) < 32 {
			continue
		}
		shown++
		fmt.Printf("\nframe @%d seq=%d payload=%d dest=0x%08x src=0x%08x\n",
			f.Offset, f.Seq, f.PayloadLen, f.DestID, f.SrcID)
		fmt.Printf("  claro   % x\n", f.Payload[:32])

		hdr, herr := aesCBC(key, f.Payload[:32], false)
		if herr == nil {
			typ := hdr[0]
			cmdSize := binary.LittleEndian.Uint16(hdr[9:])
			rawSize := binary.LittleEndian.Uint16(hdr[11:])
			media := binary.LittleEndian.Uint32(hdr[11:])
			fmt.Printf("  desc32  % x\n", hdr)
			fmt.Printf("  tipo=0x%02x plausible=%v cmdSize=%d rawSize=%d mediaSize=%d\n",
				typ, plausibleQuiiType(typ), cmdSize, rawSize, media)
			// Relacion entre lo que ocupa el payload en el cable y lo que
			// anuncia la cabecera: sirve para saber cuanto consumir.
			if plausibleQuiiType(typ) {
				pad := (int(media) + 15) / 16 * 16
				rest := (f.PayloadLen - 32) - pad
				fmt.Printf("  medida payload=%d media=%d pad=%d resto=%d\n",
					f.PayloadLen, media, pad, rest)
			}
			if plausibleQuiiType(typ) {
				rest := f.Payload[32:]
				if len(rest) > 0 && len(rest)%16 == 0 {
					if body, berr := aesCBC(key, rest, false); berr == nil {
						fmt.Printf("  cuerpo  % x\n", body[:min(24, len(body))])
					}
				}
			}
		}

		// Plan B: el payload entero cifrado (bloques completos al principio).
		if len(f.Payload) >= 32 {
			prefix := len(f.Payload) - len(f.Payload)%16
			if prefix >= 32 {
				if dec, derr := aesCBC(key, f.Payload[:prefix], false); derr == nil {
					fmt.Printf("  entero  % x\n", dec[:min(24, len(dec))])
				}
			}
		}
		// Donde empieza el cuerpo: se prueban los dos desfases plausibles y se
		// cuentan los start codes H.264 del texto descifrado.
		for _, off := range []int{32, 36} {
			if len(f.Payload) <= off+16 {
				continue
			}
			b := f.Payload[off:]
			n := len(b) - len(b)%16
			dec, derr := aesCBC(key, b[:n], false)
			if derr != nil {
				continue
			}
			fmt.Printf("  body@%d bytes=%d startcodes=%d head=% x\n",
				off, n, countH264StartCodes(dec), dec[:min(16, len(dec))])
		}
	}
	return nil
}

func readKeyFromCheck(cfg config, d *daemon) ([]byte, error) {
	if err := d.login(); err != nil {
		return nil, err
	}
	tok, err := d.auth.DeviceToken(cfg.deviceID)
	if err != nil {
		return nil, err
	}
	if len(tok.DataEncodeKey) != 32 {
		return nil, fmt.Errorf("data-encode-key de %d bytes", len(tok.DataEncodeKey))
	}
	return []byte(tok.DataEncodeKey), nil
}

func countH264StartCodes(b []byte) int {
	n := 0
	for i := 0; i+3 < len(b); i++ {
		if b[i] == 0 && b[i+1] == 0 && b[i+2] == 1 {
			n++
		}
	}
	return n
}

var _ = io.Discard
