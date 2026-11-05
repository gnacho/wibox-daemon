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
	return consumeQuiiPacketsWith(key, buf, nil)
}

// consumeQuiiPacketsWith es igual pero entrega cada paquete decodificado a
// onPacket antes de demuxear. Sirve para atender respuestas del canal
// transparente (la apertura) sin perderlas entre la media.
func consumeQuiiPacketsWith(key, buf []byte, onPacket func(*packet)) ([]mediaFrame, []byte) {
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
		if onPacket != nil {
			onPacket(p)
		}
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

	ready := make(chan struct{})
	var readyOnce sync.Once

	// La apertura se hace por esta misma sesion: el timbre solo admite una y
	// mientras dura el video la API no puede montar otro tunel.
	transparent := make(chan []byte, 16)
	var unlockMu sync.Mutex
	var ctl *controlServer
	if cfg.pin != "" {
		unlock := func() (int, error) {
			unlockMu.Lock()
			defer unlockMu.Unlock()
			// Se arranca antes de tener la sesion lista para que la API no
			// monte otro tunel mientras este se esta montando (es justo la
			// ventana en la que se peleaban por el timbre).
			select {
			case <-ready:
			case <-time.After(40 * time.Second):
				return -1, fmt.Errorf("la sesion de video no esta lista")
			}
			pkt, perr := encryptPacket(key, 0xFE, unlockBody(cfg.pin, 1, 1), 4, 0, 0, 0, 0)
			if perr != nil {
				return -1, perr
			}
			// Se vacia lo que hubiera pendiente: una respuesta vieja no vale.
			for len(transparent) > 0 {
				<-transparent
			}
			if serr := t.sendQuii(pkt); serr != nil {
				return -1, serr
			}
			select {
			case resp := <-transparent:
				return unlockResult(resp)
			case <-time.After(15 * time.Second):
				return -1, fmt.Errorf("timeout esperando la respuesta de apertura")
			}
		}
		if srv, serr := newControlServer(controlSockPath(""), unlock); serr != nil {
			log.Printf("video: sin canal de apertura: %v", serr)
		} else {
			ctl = srv
			defer ctl.close()
			log.Printf("video: canal de apertura en %s", srv.path)
		}
	}

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
	readyOnce.Do(func() { close(ready) })
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
	// Con WIBOX_MPEGTS la salida es un MPEG-TS con las dos pistas, que es la
	// unica forma de que go2rtc reciba imagen y sonido por el mismo pipe: dos
	// fuentes exec no se mezclan, se tratan como failover.
	var ts *tsMuxer
	if v := os.Getenv("WIBOX_MPEGTS"); v != "" && v != "0" {
		ts = newTSMuxer(out)
		log.Printf("video: salida MPEG-TS con pistas H264 y G711A")
	}
	waitingIDR := true
	nalCensus := map[int]int{}
	skipped := 0
	// El timbre emite un IDR al arrancar el stream, asi que enganchados a mitad
	// de GOP se pueden pasar minutos sin keyframe. Reenviar el play reinicia el
	// stream y provoca uno nuevo (es lo que hace la app propia en resendPlay).
	playResends := 0
	maxPlayResends := 3
	// Con WIBOX_PLAY_RESENDS=0 no se reenvia el play: el reenvio reinicia el
	// stream y puede resetear el contador del GOP, asi que para dejar que el
	// timbre llegue a su keyframe hay que poder desactivarlo.
	if v := os.Getenv("WIBOX_PLAY_RESENDS"); v != "" {
		if n, nerr := strconv.Atoi(v); nerr == nil && n >= 0 {
			maxPlayResends = n
		}
	}
	log.Printf("video: tope de reenvios del play %d", maxPlayResends)
	lastResend := time.Now()
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

	// Tope de vida de la sesion: el timbre admite una sola conexion QUII, asi
	// que un cliente RTSP colgado dejaria el portal ocupado sin limite. Al
	// cumplirse el proceso termina y go2rtc lo relanza si hace falta.
	maxSession := 15 * time.Minute
	if v := os.Getenv("WIBOX_VIDEO_MAX"); v != "" {
		if n, merr := strconv.Atoi(v); merr == nil && n >= 0 {
			if n == 0 {
				maxSession = 0
			} else {
				maxSession = time.Duration(n) * time.Second
			}
		}
	}
	var maxC <-chan time.Time
	if maxSession > 0 {
		maxTimer := time.NewTimer(maxSession)
		defer maxTimer.Stop()
		maxC = maxTimer.C
	}

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
			frames, buf = consumeQuiiPacketsWith(key, buf, func(p *packet) {
				// Solo las respuestas a la orden 4 (apertura): otros
				// transparentes podrian confundirse con ella.
				if p.Type == 0xFE && (p.B13 == 4 || p.B14 == 4) {
					select {
					case transparent <- p.Command:
					default:
					}
				}
			})
			for _, f := range frames {
				// El audio no depende de un keyframe: se emite desde el
				// principio, asi se oye al visitante aunque el IDR tarde.
				if !f.Video {
					if audioOut != nil && len(f.Bitstream) > 0 {
						if ts != nil {
							if aerr := ts.WriteAudio(f.TimestampMS, f.Bitstream); aerr != nil {
								return fmt.Errorf("escribiendo audio: %w", aerr)
							}
						} else if _, aerr := audioOut.Write(f.Bitstream); aerr != nil {
							return fmt.Errorf("escribiendo audio: %w", aerr)
						}
						audioFrames++
					}
					if !waitTimer.Stop() {
						select {
						case <-maxC:
							log.Printf("video: sesion cerrada tras %s con %d frames de video y %d de audio",
								maxSession, videoFrames, audioFrames)
							return nil
						case <-waitTimer.C:
						default:
						}
					}
					waitTimer.Reset(videoWait)
					continue
				}
				if f.Video {
					countNALTypes(f.Bitstream, nalCensus)
					if waitingIDR && !f.KeyFrame {
						skipped++
						if skipped <= 5 || skipped%100 == 0 {
							log.Printf("video: esperando keyframe, %d frames saltados, censo NAL %v", skipped, nalCensus)
						}
						// Sin keyframe no hay nada que decodificar: se pide un
						// reinicio del stream cada 10 s, unas pocas veces.
						if playResends < maxPlayResends && time.Since(lastResend) > 10*time.Second {
							playResends++
							lastResend = time.Now()
							if perr := t.sendQuiiPlayAgain(); perr != nil {
								log.Printf("video: no se pudo reenviar el play: %v", perr)
							} else {
								log.Printf("video: play reenviado (%d/%d) para forzar un keyframe", playResends, maxPlayResends)
							}
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
						if ts != nil {
							if err := ts.WriteVideo(f.TimestampMS, params); err != nil {
								return fmt.Errorf("escribiendo SPS/PPS: %w", err)
							}
						} else if _, err := out.Write(params); err != nil {
							return fmt.Errorf("escribiendo SPS/PPS: %w", err)
						}
						log.Printf("video: primer keyframe tras %d frames saltados, SPS/PPS inyectados", skipped)
					}
					if ts != nil {
						if err := ts.WriteVideo(f.TimestampMS, f.Bitstream); err != nil {
							return fmt.Errorf("escribiendo video: %w", err)
						}
					} else if _, err := out.Write(f.Bitstream); err != nil {
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
						log.Printf("video: %d frames (%d key) audio=%d censo NAL %v",
							videoFrames, keyFrames, audioFrames, nalCensus)
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
