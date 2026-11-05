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

// quiiStat acumula lo que se ve en un paquete de media del volcado.
type quiiStat struct {
	Type      byte
	MediaSize int
	MediaLen  int
	Frames    int
	Kind      string
	FrameType int
	Width     int
	Height    int
	FPS       float64
	TsMS      int64
	Kinds     []string
	Head      []byte
	NALs      map[int]int
	StartCode bool
}

// decodeAnyDump elige el parser segun el volcado: el del tunel en crudo trae
// frames KCP y el que sale del desmultiplexado ya es flujo QUII.
func decodeAnyDump(path string, key []byte, limit int) error {
	raw, err := os.ReadFile(path)
	if err != nil {
		return err
	}
	if len(walkRbFrames(raw)) > 0 {
		return decodeDump(path, key, limit)
	}
	return decodeQuiiStream(path, key)
}

// decodeQuiiStream recorre un volcado del flujo QUII tal cual sale del tunel
// (lo que escribe WIBOX_RAW_DUMP) y resume sus paquetes, con el detalle de los
// de media grandes: son los que se leen mal y los que hay que mirar cuando la
// imagen no arranca.
func decodeQuiiStream(path string, key []byte) error {
	raw, err := os.ReadFile(path)
	if err != nil {
		return err
	}
	buf := raw
	resyncBytes := 0
	packets := 0
	types := map[byte]int{}
	var stats []quiiStat
	scanned := 0
	for {
		p, used, derr := decodeQuiiPacket(key, buf)
		if derr == errShortPacket {
			break
		}
		if derr != nil {
			// Cabecera ilegible: se avanza un byte, como hace el daemon en
			// caliente, para ver cuanto del volcado es ruido.
			buf = buf[1:]
			resyncBytes++
			continue
		}
		packetRaw := buf[:used]
		buf = buf[used:]
		packets++
		types[p.Type]++
		if len(p.Media) == 0 {
			continue
		}
		// El media va en claro: se puede volcar tal cual para analizarlo.
		if out := os.Getenv("WIBOX_DECODE_MEDIA"); out != "" && p.Type >= 0xA0 && p.Type <= 0xA3 && len(p.Media) > 4096 {
			if _, serr := os.Stat(out); serr == nil {
				out = ""
			}
			if werr := os.WriteFile(out, p.Media, 0o600); werr == nil {
				fmt.Printf("media en claro del primer paquete grande guardada en %s (%d bytes)\n", out, len(p.Media))
			}
		}
		if scanned == 0 && p.Type >= 0xA0 && p.Type <= 0xA3 && p.RawSize > 4096 {
			scanBigOffsets(key, packetRaw, p.RawSize)
			scanned++
		}
		st := quiiStat{
			Type:      p.Type,
			MediaSize: p.RawSize,
			MediaLen:  len(p.Media),
			NALs:      map[int]int{},
		}
		for _, f := range demuxMedia(p.Media) {
			st.Frames++
			kind := fmt.Sprintf("%s/%d/%dB", frameKindName(f.FrameType), f.Codec, len(f.Bitstream))
			if st.Kind == "" && f.Video {
				st.Kind = frameKindName(f.FrameType)
				st.FrameType = f.FrameType
				st.Width, st.Height, st.FPS, st.TsMS = f.Width, f.Height, f.FPS, f.TimestampMS
			}
			if len(st.Head) == 0 && len(f.Bitstream) >= 12 {
				st.Head = append([]byte(nil), f.Bitstream[:12]...)
				st.StartCode = hasStartCode(f.Bitstream)
				countNALTypes(f.Bitstream, st.NALs)
			}
			st.Kinds = append(st.Kinds, kind)
		}
		stats = append(stats, st)
	}

	fmt.Printf("volcado QUII %s: %d bytes, %d paquetes, %d bytes descartados al resincronizar\n",
		path, len(raw), packets, resyncBytes)
	fmt.Printf("tipos %v\n", types)
	small, big := 0, 0
	for _, s := range stats {
		if s.MediaLen > 4096 {
			big++
		} else {
			small++
		}
	}
	fmt.Printf("paquetes de media: %d pequenos, %d grandes\n", small, big)
	shown := 0
	for _, s := range stats {
		if s.MediaLen <= 4096 || shown >= 6 {
			continue
		}
		shown++
		fmt.Printf("\ntipo=0x%02x anuncia=%d descifrado=%d frames=%d kind=%s ft=0x%02x %dx%d fps=%v ts=%d kinds=%v\n",
			s.Type, s.MediaSize, s.MediaLen, s.Frames, s.Kind, s.FrameType, s.Width, s.Height, s.FPS, s.TsMS, s.Kinds)
		fmt.Printf("  startcode=%v head=% x nals=%v\n", s.StartCode, s.Head, s.NALs)
	}
	return nil
}

func frameKindName(t int) string {
	switch t {
	case 0x03:
		return "audio"
	case 0x00, 0x01, 0x09, 0x0A:
		return "video"
	}
	return fmt.Sprintf("0x%02x", byte(t))
}

func hasStartCode(b []byte) bool {
	for i := 0; i+3 <= len(b); i++ {
		if b[i] == 0 && b[i+1] == 0 && b[i+2] == 1 {
			return true
		}
	}
	return false
}

// scanBigOffsets prueba a descifrar el cuerpo de un paquete de media grande
// desde varios desfases. Si el device mete un prefijo en claro (cabecera
// propia) antes del bloque cifrado, solo un desfase concreto dara un access
// unit H.264 coherente (varios start codes y algun NAL 5 u 8).
func scanBigOffsets(key, packetRaw []byte, mediaSize int) {
	if len(packetRaw) < 32+mediaSize {
		return
	}
	body := packetRaw[32 : 32+mediaSize]
	fmt.Printf("\ndesfases del cuerpo cifrado (%d bytes):\n", len(body))
	dec0 := body[:len(body)-len(body)%16]
	if d0, err0 := aesCBC(key, dec0, false); err0 == nil {
		dec0 = d0
	}
	for off := 0; off <= 32; off++ {
		if len(body) <= off+16 {
			break
		}
		b := body[off:]
		n := len(b) - len(b)%16
		dec, err := aesCBC(key, b[:n], false)
		if err != nil {
			continue
		}
		if save := os.Getenv("WIBOX_DECODE_SAVE"); save != "" && off == 0 {
			if werr := os.WriteFile(save, dec, 0o600); werr == nil {
				fmt.Printf("  cuerpo cifrado completo guardado en %s\n", save)
			}
		}
		sc := countH264StartCodes(dec)
		nals := map[int]int{}
		countNALTypes(dec, nals)
		if sc < 1 {
			continue
		}
		fmt.Printf("  off=%2d bytes=%d startcodes=%d nals=%v head=% x\n",
			off, n, sc, nals, dec[:min(20, len(dec))])
	}
	// Segunda pasada: si el device cifra dos veces (o cifra solo la slice), el
	// texto que vemos sigue siendo ciphertext y otra vuelta lo revela.
	for off := 0; off <= 32; off++ {
		if len(dec0) <= off+16 {
			break
		}
		b := dec0[off:]
		n := len(b) - len(b)%16
		dec2, derr := aesCBC(key, b[:n], false)
		if derr != nil {
			continue
		}
		sc := countH264StartCodes(dec2)
		if sc < 2 {
			continue
		}
		nals := map[int]int{}
		countNALTypes(dec2, nals)
		fmt.Printf("  DOBLE off=%2d startcodes=%d nals=%v head=% x\n", off, sc, nals, dec2[:min(20, len(dec2))])
	}
	fmt.Printf("  (se listan los desfases con al menos un start code)\n")
}
