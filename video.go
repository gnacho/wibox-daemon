package main

import (
	"fmt"
	"io"
	"log"
	"os"
	"strconv"
	"sync"
	"time"
)

// consumeQuiiPackets parsea todos los paquetes QUII completos que haya en el
// buffer y devuelve sus frames de media junto al resto sin consumir. Ante un
// paquete ilegible avanza un byte para resincronizar: sin eso el bucle no
// progresa nunca cuando el descifrado no da un tamano creible.
func consumeQuiiPackets(key, buf []byte) ([]mediaFrame, []byte) {
	var frames []mediaFrame
	// Una cabecera basura puede declarar un tamano grande pero creible y dejar
	// el bucle esperando bytes que no llegan nunca. Si el buffer ya es grande y
	// seguimos sin poder cerrar el paquete, se avanza un byte y se reintenta.
	const stallLimit = 512 * 1024
	for {
		p, used, err := decodeQuiiPacket(key, buf)
		if err == errShortPacket {
			if len(buf) < stallLimit {
				return frames, buf
			}
			buf = buf[1:]
			continue
		}
		if err != nil {
			if used > 0 {
				buf = buf[used:]
			} else {
				buf = buf[1:]
			}
			continue
		}
		buf = buf[used:]
		if len(p.Media) > 0 {
			frames = append(frames, demuxMedia(p.Media)...)
		}
	}
}

// SPS y PPS del timbre (H264 Main 352x288). Al engancharnos a mitad de stream no
// llegan en el bitstream, y sin PPS el decodificador no arranca. Los valores son
// los mismos que usa la app oficial, extraidos de un IDR real del propio timbre.
const (
	defaultSPSHex = "474d401e99a05825a100000303e800009c4084"
	defaultPPSHex = "48ee3c80"
)

func annexBParams() []byte {
	sps, pps := defaultSPSHex, defaultPPSHex
	if v := os.Getenv("WIBOX_SPS"); v != "" {
		sps = v
	}
	if v := os.Getenv("WIBOX_PPS"); v != "" {
		pps = v
	}
	out := []byte{0, 0, 0, 1}
	out = append(out, mustHex(sps)...)
	out = append(out, 0, 0, 0, 1)
	out = append(out, mustHex(pps)...)
	return out
}

// runVideo abre la cadena remota, arranca el play y escribe el bitstream H264 en
// Annex-B por el writer. Es lo que consume go2rtc desde Home Assistant con un
// origen exec, asi que no hace falta montar un servidor RTSP aqui.
func runVideo(cfg config, session string, key []byte, password string, out io.Writer, audioOut io.Writer) error {
	if len(key) != 32 {
		return fmt.Errorf("data-encode-key de %d bytes, se esperaban 32", len(key))
	}
	s, err := establishRemote(cfg, session)
	if err != nil {
		return err
	}
	defer s.close()

	t := newRbTunnel(s.conn, s.probe.peer, s.sessionFlag, key, password, cfg.channel, cfg.stream)
	t.start()
	defer t.close()

	if err := t.awaitConnected(20 * time.Second); err != nil {
		return err
	}
	log.Printf("video: tunel RB-UDP conectado con %s", s.probe.peer)
	if _, err := t.awaitQuiiDestID(12 * time.Second); err != nil {
		return err
	}
	// La respuesta del setup QUII (32 bytes EN CLARO) llega por el tunel. Hay
	// que consumirla antes del play o el buffer queda desalineado y el
	// descifrado de la cabecera siguiente da basura.
	setup, err := waitQuiiSetup(t.incoming(), 10*time.Second)
	if err != nil {
		return err
	}
	if setup[0] != 0xA9 {
		return fmt.Errorf("setup QUII inesperado: 0x%02x", setup[0])
	}
	log.Printf("video: setup QUII ok crypto=%d sha=%d", setup[10], setup[11])
	if err := t.sendQuiiPlay(); err != nil {
		return err
	}
	log.Printf("video: play enviado, esperando frames")

	// Keepalive cada 10 s, igual que el cliente oficial. Sin esto el timbre no
	// pasa de POST_CONNECT y nunca emite.
	keepStop := make(chan struct{})
	var keepOnce sync.Once
	defer keepOnce.Do(func() { close(keepStop) })
	go func() {
		ticker := time.NewTicker(10 * time.Second)
		defer ticker.Stop()
		for {
			select {
			case <-keepStop:
				return
			case <-ticker.C:
				if err := t.sendQuiiKeepalive(); err != nil {
					log.Printf("video: keepalive fallido: %v", err)
					return
				}
				if debugOn() {
					log.Printf("video: keepalive enviado")
				}
			}
		}
	}()

	// El play sync se vigila en segundo plano: esperarlo aqui bloqueaba la
	// lectura justo durante el arranque del stream, que es donde el timbre manda
	// el CSD y el primer keyframe. Medido: 25 s de espera y ni un IDR.
	go func() {
		if err := t.awaitQuiiReady(25 * time.Second); err != nil {
			log.Printf("video: play sync incompleto: %v", err)
			return
		}
		log.Printf("video: play sync completo")
	}()

	// Igual que la app: sin PPS no hay decodificador, y antes del primer IDR los
	// slices no sirven para nada. Se espera al keyframe y entonces se escribe
	// SPS+PPS+IDR de una tacada.
	params := annexBParams()
	waitingIDR := true
	skipped := 0
	var buf []byte
	var videoFrames, audioFrames, keyFrames int
	var rawBytes, rawMark int
	firstFrame := true
	// Ventana de espera del primer frame. El timbre duerme y a veces tarda en
	// arrancar el stream, asi que se puede alargar por entorno.
	videoWait := 20 * time.Second
	if v := os.Getenv("WIBOX_VIDEO_WAIT"); v != "" {
		if n, err := strconv.Atoi(v); err == nil && n > 0 {
			videoWait = time.Duration(n) * time.Second
		}
	}
	// Un time.After dentro del select se reinicia en cada vuelta, asi que con
	// datagramas entrando nunca vencia y el bucle no cortaba nunca.
	waitTimer := time.NewTimer(videoWait)
	defer waitTimer.Stop()

	// Volcado en crudo del flujo del tunel: con el play ya aceptado llegan MB
	// pero el parser no saca frames, asi que hay que ver los bytes tal cual.
	var rawDump *os.File
	var rawChunks int
	if p := os.Getenv("WIBOX_RAW_DUMP"); p != "" {
		if f, ferr := os.Create(p); ferr == nil {
			rawDump = f
			defer f.Close()
		} else {
			log.Printf("video: no se pudo abrir el volcado en crudo: %v", ferr)
		}
	}

	for {
		select {
		case chunk, ok := <-t.incoming():
			if !ok {
				return fmt.Errorf("el tunel se cerro tras %d frames de video", videoFrames)
			}
			buf = append(buf, chunk...)
			rawBytes += len(chunk)
			if rawDump != nil {
				if _, werr := rawDump.Write(chunk); werr != nil {
					log.Printf("video: volcado en crudo fallido: %v", werr)
					rawDump.Close()
					rawDump = nil
				}
			}
			if debugOn() && rawChunks < 6 && len(chunk) > 0 {
				rawChunks++
				n := len(chunk)
				if n > 48 {
					n = 48
				}
				log.Printf("video: chunk %d len=%d head=% x", rawChunks, len(chunk), chunk[:n])
			}
			if debugOn() && rawBytes/200000 > rawMark {
				rawMark = rawBytes / 200000
				log.Printf("video: %d bytes recibidos, %d frames, %d sin parsear", rawBytes, videoFrames, len(buf))
			}
			var frames []mediaFrame
			frames, buf = consumeQuiiPackets(key, buf)
			for _, f := range frames {
				if f.Video {
					if waitingIDR && !f.KeyFrame {
						skipped++
						if skipped <= 5 || skipped%100 == 0 {
							log.Printf("video: esperando keyframe, %d frames saltados", skipped)
						}
						if !waitTimer.Stop() {
							select {
							case <-waitTimer.C:
							default:
							}
						}
						// Mientras llegan frames el flujo esta vivo: no se corta.
						waitTimer.Reset(videoWait)
						continue
					}
					if waitingIDR {
						waitingIDR = false
						if _, err := out.Write(params); err != nil {
							return fmt.Errorf("escribiendo SPS/PPS: %w", err)
						}
						log.Printf("video: primer keyframe tras %d frames saltados, SPS/PPS inyectados", skipped)
					}
					if _, err := out.Write(f.Bitstream); err != nil {
						return fmt.Errorf("escribiendo video: %w", err)
					}
					videoFrames++
					if f.KeyFrame {
						keyFrames++
					}
					if firstFrame {
						firstFrame = false
						log.Printf("video: primer frame %dx%d fps=%.1f codec=%d key=%v",
							f.Width, f.Height, f.FPS, f.Codec, f.KeyFrame)
					}
					if videoFrames%100 == 0 {
						log.Printf("video: %d frames (%d key) audio=%d", videoFrames, keyFrames, audioFrames)
					}
					if !waitTimer.Stop() {
						select {
						case <-waitTimer.C:
						default:
						}
					}
					waitTimer.Reset(videoWait)
					continue
				}
				if audioOut != nil && len(f.Bitstream) > 0 {
					if _, err := audioOut.Write(f.Bitstream); err != nil {
						return fmt.Errorf("escribiendo audio: %w", err)
					}
					audioFrames++
				}
			}
			// Si el timbre deja de emitir, se corta en vez de quedarse colgado
			// con el tunel abierto y el portal ocupado.
			if len(buf) > 4*1024*1024 {
				log.Printf("video: %d bytes sin paquete valido, se descartan", len(buf))
				buf = buf[:0]
			}
		case <-waitTimer.C:
			if videoFrames > 0 {
				return fmt.Errorf("sin frames en %s tras %d frames", videoWait, videoFrames)
			}
			return fmt.Errorf("el timbre no emitio ningun frame en %s", videoWait)
		}
	}
}
