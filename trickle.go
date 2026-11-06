package main

import (
	"encoding/base64"
	"log"
	"os"
	"strconv"
	"sync"
	"time"
)

// fakeFrame es un IDR gris de 352x288 con su propio SPS y PPS, generado con
// x264 y sin el SEI de version para que quepa en 99 bytes. Se emite mientras el
// tunel con el timbre se esta montando: go2rtc no publica el stream hasta que
// su demuxer ve un frame de video de verdad, y con solo audio de relleno su RTSP
// sigue sin contestar el DESCRIBE.
var fakeFrame = mustDecodeBase64("AAAAAWdNQA3cFglsBEAAAAMAQAAACgPFCuAAAAABaO4PLIAAAAFliIQEv/73rd+BNoFa7pXOLTLq5Q0PVH2lKZ4tkgAAN08qSDi9HSfwEIATMAADQDAcGNzHYWiwYqtwAAPT")

func mustDecodeBase64(s string) []byte {
	b, err := base64.StdEncoding.DecodeString(s)
	if err != nil {
		panic("frame de relleno ilegible: " + err.Error())
	}
	return b
}

// trickleInterval lee WIBOX_TRICKLE_MS (defecto 20 ms, que es un frame de
// audio G.711A a 8 kHz). Cero o negativo lo desactiva.
func trickleInterval() time.Duration {
	ms, _ := strconv.Atoi(env("WIBOX_TRICKLE_MS", "20"))
	if ms <= 0 {
		return 0
	}
	return time.Duration(ms) * time.Millisecond
}

// startAudioTrickle mantiene el tubo con media mientras el tunel con el timbre
// se esta montando: silencio G.711A en cada tick y un IDR de relleno cada cinco
// ticks. Sin esto el consumidor se queda sin paquetes durante decenas de
// segundos y su RTSP no contesta hasta que el timbre despierta.
//
// Solo se usa en la ventana previa a los frames reales: quien la llama tiene
// que pararla antes de empezar a escribir video, porque el muxer no esta
// preparado para dos escritores a la vez.
func startAudioTrickle(ts *tsMuxer, every time.Duration) (stop func()) {
	trickleVideo := os.Getenv("WIBOX_TRICKLE_VIDEO") != "0"
	if ts == nil || every <= 0 {
		return func() {}
	}
	done := make(chan struct{})
	var once sync.Once
	var wg sync.WaitGroup
	wg.Add(1)
	go func() {
		defer wg.Done()
		t := time.NewTicker(every)
		defer t.Stop()
		var ptsMS int64
		ticks := 0
		for {
			select {
			case <-done:
				return
			case <-t.C:
				ptsMS += 20
				ticks++
				if err := ts.WriteAudio(ptsMS, alawSilence); err != nil {
					return
				}
				// El IDR de relleno se manda solo si se pide: sus parametros
				// no son los del timbre y go2rtc se queda con ellos como si
				// fueran del stream, con lo que el video real no decodifica.
				if trickleVideo && ticks%5 == 0 {
					// Solo los parametros: declaran la pista de video para que
					// go2rtc responda el DESCRIBE sin meter en la cache un
					// keyframe con parametros que no son los del timbre.
					if err := ts.WriteVideo(ptsMS, annexBParams()); err != nil {
						return
					}
				}
			}
		}
	}()
	return func() {
		once.Do(func() {
			log.Printf("video: fin del relleno de espera")
			close(done)
			wg.Wait()
		})
	}
}
