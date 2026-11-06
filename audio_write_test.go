package main

import (
	"bytes"
	"testing"
)

// En MPEG-TS el audio tiene que ir al muxer aunque no haya audioOut aparte. El
// fallo original era justo ese: la condicion pedia audioOut y en MPEG-TS es
// nil, asi que el audio del timbre no llegaba al consumidor.
func TestWriteAudioFrameGoesIntoTheMuxer(t *testing.T) {
	var out bytes.Buffer
	ts := newTSMuxer(&out)

	wrote, err := writeAudioFrame(ts, nil, 40, alawSilence)
	if err != nil {
		t.Fatalf("error: %v", err)
	}
	if !wrote {
		t.Fatal("dice que no escribio, con muxer delante")
	}
	if out.Len() == 0 {
		t.Fatal("no salio nada por el muxer")
	}
}

func TestWriteAudioFrameFallsBackToTheWriter(t *testing.T) {
	var out bytes.Buffer
	wrote, err := writeAudioFrame(nil, &out, 0, alawSilence)
	if err != nil || !wrote {
		t.Fatalf("wrote=%v err=%v", wrote, err)
	}
	if out.Len() != len(alawSilence) {
		t.Fatalf("%d bytes, se esperaban %d", out.Len(), len(alawSilence))
	}
}

func TestWriteAudioFrameWithoutSinkWritesNothing(t *testing.T) {
	wrote, err := writeAudioFrame(nil, nil, 0, alawSilence)
	if err != nil || wrote {
		t.Fatalf("wrote=%v err=%v, se esperaba no escribir", wrote, err)
	}
}

func TestWriteAudioFrameSkipsEmptyFrames(t *testing.T) {
	var out bytes.Buffer
	ts := newTSMuxer(&out)
	wrote, err := writeAudioFrame(ts, nil, 0, nil)
	if err != nil || wrote {
		t.Fatalf("wrote=%v err=%v, se esperaba saltarlo", wrote, err)
	}
}
