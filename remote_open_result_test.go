package main

import "testing"

func TestUnlockResultReadsTheDoorbellAnswer(t *testing.T) {
	cases := []struct {
		name string
		resp []byte
		rc   int
	}{
		{"sin respuesta", nil, -1},
		{"respuesta corta", []byte{0}, -1},
		{"aceptada", []byte{0, 0}, 0},
		{"contrasena rechazada", []byte{1, 2}, -10029},
		{"otro fallo", []byte{1, 9}, -1},
	}
	for _, tc := range cases {
		rc, err := unlockResult(tc.resp)
		if rc != tc.rc {
			t.Fatalf("%s: rc=%d, se esperaba %d", tc.name, rc, tc.rc)
		}
		if tc.rc != 0 && err == nil {
			t.Fatalf("%s: se esperaba error", tc.name)
		}
		if tc.rc == 0 && err != nil {
			t.Fatalf("%s: error inesperado: %v", tc.name, err)
		}
	}
}
