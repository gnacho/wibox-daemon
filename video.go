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
	for {
		p, used, err := decodeQuiiPacket(key, buf)
		if err == errShortPacket {
			return frames, buf
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

	if err := t.awaitQuiiReady(25 * time.Second); err != nil {
		log.Printf("video: play sync incompleto: %v", err)
	} else {
		log.Printf("video: play sync completo")
	}

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

	for {
		select {
		case chunk, ok := <-t.incoming():
			if !ok {
				return fmt.Errorf("el tunel se cerro tras %d frames de video", videoFrames)
			}
			buf = append(buf, chunk...)
			rawBytes += len(chunk)
			if debugOn() && rawBytes/200000 > rawMark {
				rawMark = rawBytes / 200000
				log.Printf("video: %d bytes recibidos, %d frames, %d sin parsear", rawBytes, videoFrames, len(buf))
			}
			var frames []mediaFrame
			frames, buf = consumeQuiiPackets(key, buf)
			for _, f := range frames {
				if f.Video {
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
