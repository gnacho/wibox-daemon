package main

import "testing"

func TestShouldSkipFrame(t *testing.T) {
	cases := []struct {
		name       string
		waitingIDR bool
		keyFrame   bool
		allFrames  bool
		want       bool
	}{
		{"esperando IDR y frame normal se descarta", true, false, false, true},
		{"el IDR nunca se descarta", true, true, false, false},
		{"tras el IDR pasa todo", false, false, false, false},
		{"con todos los frames no se descarta nada", true, false, true, false},
		{"con todos los frames el IDR pasa", true, true, true, false},
	}
	for _, c := range cases {
		if got := shouldSkipFrame(c.waitingIDR, c.keyFrame, c.allFrames); got != c.want {
			t.Errorf("%s: shouldSkipFrame(%v,%v,%v) = %v, se esperaba %v",
				c.name, c.waitingIDR, c.keyFrame, c.allFrames, got, c.want)
		}
	}
}

func TestEnvFlag(t *testing.T) {
	for value, want := range map[string]bool{"": false, "0": false, "1": true, "si": true} {
		t.Setenv("WIBOX_TEST_FLAG", value)
		if got := envFlag("WIBOX_TEST_FLAG"); got != want {
			t.Errorf("envFlag con %q = %v, se esperaba %v", value, got, want)
		}
	}
}

func TestMpegtsEnabled(t *testing.T) {
	t.Setenv("WIBOX_MPEGTS", "1")
	if !mpegtsEnabled() {
		t.Fatal("con WIBOX_MPEGTS=1 deberia estar activo")
	}
	t.Setenv("WIBOX_MPEGTS", "0")
	if mpegtsEnabled() {
		t.Fatal("con WIBOX_MPEGTS=0 deberia estar apagado")
	}
}
