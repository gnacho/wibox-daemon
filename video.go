package main

import (
	"fmt"
	"io"
	"log"
	"os"
	"strconv"
	"sync"
	"sync/atomic"
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
const defaultVideoLock = "/tmp/wibox-video.lock"

const (
	// El byte de cabecera va como 0x67 (nal_ref_idc = 3) y no como el 0x47
	// (nal_ref_idc = 2) que manda el aparato. Los dos son validos en H.264,
	// pero go2rtc descarta los NAL que empiezan por 0x47: su comprobacion de
	// AUD de H.265 es b&0x7e == 70 y 0x47 & 0x7e tambien da 70, asi que el
	// SPS se toma por un AUD y no llega nunca al SDP del RTSP.
	defaultSPSHex = "674d401e99a05825a100000303e800009c4084"
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

// envFlag dice si una variable de entorno vale como un si: presente y distinta
// de "0".
func envFlag(name string) bool {
	v := os.Getenv(name)
	return v != "" && v != "0"
}

// mpegtsEnabled dice si la salida va muxada en MPEG-TS, con imagen y sonido en
// el mismo tubo, en vez de H264 desnudo en stdout y el audio aparte.
func mpegtsEnabled() bool {
	return envFlag("WIBOX_MPEGTS")
}

// shouldSkipFrame dice si un frame de video se descarta antes de mandarlo al
// consumidor: solo mientras no haya llegado un IDR y no se hayan pedido todos
// los frames.
func shouldSkipFrame(waitingIDR, keyFrame, allFrames bool) bool {
	return waitingIDR && !keyFrame && !allFrames
}

// writeAudioFrame manda un frame de audio al sitio que toque: al muxer cuando
// la salida va en MPEG-TS, o al writer suelto cuando van por caminos separados.
// Devuelve si escribio algo, para poder contarlo de verdad. Antes la condicion
// exigia un audioOut y en MPEG-TS es nil, asi que el audio del timbre no se
// muxaba nunca y la cuenta salia a cero.
func writeAudioFrame(ts *tsMuxer, audioOut io.Writer, ptsMS int64, bitstream []byte) (bool, error) {
	if len(bitstream) == 0 {
		return false, nil
	}
	if ts != nil {
		return true, ts.WriteAudio(ptsMS, bitstream)
	}
	if audioOut == nil {
		return false, nil
	}
	_, err := audioOut.Write(bitstream)
	return true, err
}

// runVideo abre la cadena remota, arranca el play y escribe el bitstream H264 en
// Annex-B por el writer. Es lo que consume go2rtc desde Home Assistant con un
// origen exec, asi que no hace falta montar un servidor RTSP aqui.
func runVideo(cfg config, session string, key []byte, password string, out io.Writer, audioOut io.Writer) error {
	if len(key) != 32 {
		return fmt.Errorf("data-encode-key de %d bytes, se esperaban 32", len(key))
	}
	lockPath := cfg.videoLock
	if lockPath == "" {
		lockPath = defaultVideoLock
	}
	// Igual que la app: sin PPS no hay decodificador, y antes del primer IDR los
	// slices no sirven para nada. Se espera al keyframe y entonces se escribe
	// SPS+PPS+IDR de una tacada.
	params := annexBParams()
	// Con WIBOX_MPEGTS la salida es un MPEG-TS con las dos pistas, que es la
	// unica forma de que go2rtc reciba imagen y sonido por el mismo pipe: dos
	// fuentes exec no se mezclan, se tratan como failover.
	var ts *tsMuxer
	if mpegtsEnabled() {
		ts = newTSMuxer(out)
		log.Printf("video: salida MPEG-TS con pistas H264 y G711A")
	}

	waitingIDR := true
	// El timbre puede tardar minuto y medio en mandar el primer IDR (GOP
	// larguisimo, medido en la app oficial) o no mandarlo nunca en toda la
	// sesion. Descartar hasta entonces deja al consumidor sin un solo paquete
	// de video, que es lo que hacia fallar la validacion del stream en Home
	// Assistant. Con esto se emiten igualmente: la imagen sale con el color
	// roto hasta el IDR, pero hay imagen y el flujo es valido.
	allFrames := envFlag("WIBOX_VIDEO_ALL_FRAMES")
	// Los parametros se escriben con el primer frame, no con el primer keyframe:
	// el MPEG-TS anuncia asi una pista H.264 valida desde el principio y la
	// validacion del config flow de Home Assistant no depende de un IDR.
	paramsWritten := false
	fillerSaved := false
	writeParams := func(ptsMS int64) error {
		if paramsWritten {
			return nil
		}
		paramsWritten = true
		if ts != nil {
			return ts.WriteVideo(ptsMS, params)
		}
		_, err := out.Write(params)
		return err
	}
	// La cabecera sale antes de conectar: go2rtc arranca este proceso cuando
	// alguien mira y quien valida el stream (el config flow de Home Assistant)
	// se rinde en segundos, mucho antes de que el tunel con el timbre este
	// montado.
	if err := writeParams(0); err != nil {
		return fmt.Errorf("escribiendo cabecera del stream: %w", err)
	}
	// go2rtc sondea el pipe esperando un paquete de cada tipo que anuncie el
	// PMT: con la pista de audio declarada y sin un solo paquete suyo, el
	// sondeo no termina hasta que el timbre manda media, y mientras tanto su
	// RTSP no contesta ni un DESCRIBE. Un frame de silencio lo cierra.
	if ts != nil {
		if err := ts.WriteOpeningAudio(); err != nil {
			return fmt.Errorf("escribiendo el silencio inicial: %w", err)
		}
	}
	// Un solo frame de silencio no basta: go2rtc no publica el stream hasta
	// que el demuxer lleva un rato recibiendo media. Con el timbre dormido el
	// tunel tarda decenas de segundos, asi que durante esa espera se mantiene
	// un goteo de silencio y se corta justo antes de escribir frames reales.
	stopTrickle := startAudioTrickle(ts, trickleInterval())
	defer stopTrickle()

	// El cerrojo se pide con el relleno ya en marcha: si el proceso anterior
	// todavia no ha soltado su sesion, el espectador nuevo sigue recibiendo
	// pistas en vez de quedarse mudo, y engancha el video real en cuanto la
	// otra sesion termina.
	release, lerr := acquireVideoLockWait(lockPath, lockWait())
	if lerr != nil {
		return lerr
	}
	defer release()

	// Si la API acaba de abrir, el timbre todavia tiene esa sesion y no deja
	// montar otra: esperar aqui es mas barato que gastar el presupuesto remoto
	// en p2pconnect que no van a terminar.
	awaitOpenSettle(lockPath, cfg.openSettle)

	s, err := establishRemote(cfg, session)
	if err != nil {
		return err
	}
	stopTrickle()
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
	// Contadores publicados por el canal de control (otra gorutina): van en
	// atomicos para no tener carreras con el bucle de frames.
	var vfCount, kfCount, pendingCount atomic.Int64
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
		stats := func() (int, int, int) {
			return int(vfCount.Load()), int(kfCount.Load()), int(pendingCount.Load())
		}
		if srv, serr := newControlServer(controlSockPath(""), unlock, stats); serr != nil {
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
				if perr := writeParams(f.TimestampMS); perr != nil {
					return fmt.Errorf("escribiendo SPS/PPS: %w", perr)
				}
				// El audio no depende de un keyframe: se emite desde el
				// principio, asi se oye al visitante aunque el IDR tarde.
				if !f.Video {
					wrote, aerr := writeAudioFrame(ts, audioOut, f.TimestampMS, f.Bitstream)
					if aerr != nil {
						return fmt.Errorf("escribiendo audio: %w", aerr)
					}
					if wrote {
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
					if shouldSkipFrame(waitingIDR, f.KeyFrame, allFrames) {
						skipped++
						pendingCount.Store(int64(skipped))
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
						log.Printf("video: primer keyframe tras %d frames saltados", skipped)
					}
					if ts != nil {
						// Los parametros del aparato no son validos y go2rtc
						// descarta los PES que solo llevan SPS/PPS, asi que
						// los nuestros viajan dentro del mismo PES que el
						// keyframe, y los del aparato se quitan.
						// Los parametros del aparato no son validos y go2rtc
						// reemite los que haya visto, asi que cada frame sale
						// con los nuestros delante y sin los suyos.
						payload := append(append([]byte(nil), params...), stripParamSets(f.Bitstream)...)
						if videoFrames < 3 {
							log.Printf("video: frame con parametros propios (%d/%d bytes, primeros % x)",
								len(payload), len(f.Bitstream), payload[:min(24, len(payload))])
						}
						if err := ts.WriteVideo(f.TimestampMS, payload); err != nil {
							return fmt.Errorf("escribiendo video: %w", err)
						}
						if f.KeyFrame && !fillerSaved {
							fillerSaved = true
							saveFiller(payload)
						}
					} else if _, err := out.Write(f.Bitstream); err != nil {
						return fmt.Errorf("escribiendo video: %w", err)
					}
					videoFrames++
					vfCount.Store(int64(videoFrames))
					if f.KeyFrame {
						keyFrames++
						kfCount.Store(int64(keyFrames))
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
