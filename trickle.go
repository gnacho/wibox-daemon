package main

import (
	"strconv"
	"sync"
	"time"
)

// trickleInterval lee WIBOX_TRICKLE_MS (defecto 20 ms, que es un frame de
// audio G.711A a 8 kHz). Cero o negativo lo desactiva.
func trickleInterval() time.Duration {
	ms, _ := strconv.Atoi(env("WIBOX_TRICKLE_MS", "20"))
	if ms <= 0 {
		return 0
	}
	return time.Duration(ms) * time.Millisecond
}

// startAudioTrickle escribe silencio G.711A mientras el tunel con el timbre se
// esta montando. Sin esto el tubo se queda seco decenas de segundos, go2rtc no
// publica el stream hasta que su demuxer recibe media, y su RTSP no contesta ni
// un DESCRIBE mientras tanto.
//
// Solo se usa en la ventana previa a los frames reales: quien la llama tiene
// que pararla antes de empezar a escribir video, porque el muxer no esta
// preparado para dos escritores a la vez.
func startAudioTrickle(ts *tsMuxer, every time.Duration) (stop func()) {
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
		for {
			select {
			case <-done:
				return
			case <-t.C:
				ptsMS += 20
				if err := ts.WriteAudio(ptsMS, alawSilence); err != nil {
					return
				}
			}
		}
	}()
	return func() {
		once.Do(func() {
			close(done)
			wg.Wait()
		})
	}
}
