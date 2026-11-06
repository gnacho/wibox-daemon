package main

import (
	"errors"
	"testing"
)

func TestStaleSessionDetectsDeadConnections(t *testing.T) {
	cases := []struct {
		err  error
		want bool
	}{
		{nil, false},
		{errors.New(`timeout esperando "get-device-token"`), true},
		{errors.New("unexpected EOF"), true},
		{errors.New("session expired"), true},
		{errors.New("use of closed network connection"), true},
		{errors.New("el flujo aborto: result 1009"), false},
	}
	for _, c := range cases {
		if got := staleSession(c.err); got != c.want {
			t.Fatalf("%v: %v, se esperaba %v", c.err, got, c.want)
		}
	}
}

// Si el fallo no huele a sesion muerta, no se reintenta ni se renueva nada.
func TestCloudCallDoesNotRetryOtherErrors(t *testing.T) {
	d := &daemon{}
	calls := 0
	want := errors.New("el flujo aborto: result 1009")
	err := d.cloudCall(func() error {
		calls++
		return want
	})
	if calls != 1 {
		t.Fatalf("llamo %d veces, se esperaba 1", calls)
	}
	if err != want {
		t.Fatalf("error %v, se esperaba el original", err)
	}
}

// Con sesion muerta reintenta, y si no puede renovarla devuelve el error
// original en vez de uno de login que despista.
func TestCloudCallKeepsOriginalErrorWhenReloginFails(t *testing.T) {
	d := &daemon{auth: NewUserAuth("http://127.0.0.1:1", "cid")}
	calls := 0
	want := errors.New(`timeout esperando "get-device-list"`)
	err := d.cloudCall(func() error {
		calls++
		return want
	})
	if calls != 1 {
		t.Fatalf("llamo %d veces, se esperaba 1 sin credenciales", calls)
	}
	if err != want {
		t.Fatalf("error %v, se esperaba el original", err)
	}
}
