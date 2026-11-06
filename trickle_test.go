package main

import (
	"bytes"
	"sync"
	"testing"
	"time"
)

// Mientras el tunel se monta el tubo no puede quedarse seco: go2rtc no publica
// el stream hasta que su demuxer lleva media y su RTSP no contesta. El goteo
// tiene que escribir de forma continua y pararse en cuanto se le pide.
func TestAudioTrickleKeepsWriting(t *testing.T) {
	var out bytes.Buffer
	ts := newTSMuxer(&out)

	stop := startAudioTrickle(ts, time.Millisecond)
	time.Sleep(40 * time.Millisecond)
	stop()

	afterStop := out.Len()
	if afterStop <= 188*3 {
		t.Fatalf("solo %d bytes: el goteo no escribio", afterStop)
	}

	// Parar dos veces no puede petar, y tras parar no puede seguir escribiendo.
	stop()
	time.Sleep(20 * time.Millisecond)
	if out.Len() != afterStop {
		t.Fatalf("siguio escribiendo tras parar: %d -> %d", afterStop, out.Len())
	}
}

func TestAudioTrickleWithoutMuxerIsSafe(t *testing.T) {
	stop := startAudioTrickle(nil, time.Millisecond)
	stop()
	stop()
}

func TestTrickleInterval(t *testing.T) {
	t.Setenv("WIBOX_TRICKLE_MS", "")
	if got := trickleInterval(); got != 20*time.Millisecond {
		t.Fatalf("defecto %v, se esperaba 20ms", got)
	}
	t.Setenv("WIBOX_TRICKLE_MS", "50")
	if got := trickleInterval(); got != 50*time.Millisecond {
		t.Fatalf("%v, se esperaba 50ms", got)
	}
	t.Setenv("WIBOX_TRICKLE_MS", "0")
	if got := trickleInterval(); got != 0 {
		t.Fatalf("%v, se esperaba 0", got)
	}
}

// El goteo corre en su propia gorutina y el muxer no lleva cerrojo: si alguien
// lo arrancase dos veces a la vez, los paquetes se partirian. La API solo
// permite un arranque por proceso, y esto lo deja escrito.
func TestAudioTrickleSingleWriter(t *testing.T) {
	var out bytes.Buffer
	var mu sync.Mutex
	ts := newTSMuxer(writerFunc(func(p []byte) (int, error) {
		mu.Lock()
		defer mu.Unlock()
		return out.Write(p)
	}))

	stop := startAudioTrickle(ts, time.Millisecond)
	time.Sleep(20 * time.Millisecond)
	stop()
	if out.Len() == 0 {
		t.Fatal("no escribio nada a traves del writer envuelto")
	}
}

type writerFunc func([]byte) (int, error)

func (f writerFunc) Write(p []byte) (int, error) { return f(p) }

// go2rtc no publica el stream hasta que su demuxer ve un frame de video, asi
// que el relleno tiene que llevar tambien un IDR, no solo audio.
func TestAudioTrickleWritesVideoFrames(t *testing.T) {
	t.Setenv("WIBOX_TRICKLE_VIDEO", "1")
	var out bytes.Buffer
	ts := newTSMuxer(&out)

	stop := startAudioTrickle(ts, time.Millisecond)
	time.Sleep(30 * time.Millisecond)
	stop()

	// Los parametros viajan tal cual dentro del PES de video.
	needle := annexBParams()[4:24]
	if !bytes.Contains(out.Bytes(), needle) {
		t.Fatal("el relleno no emitio la pista de video")
	}
}
