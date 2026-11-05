package main

import "testing"

// El tunel no puede mandar nada de QUII hasta que el timbre le asigne el
// destino en la respuesta al connect. Estas guardas evitan escribir en un
// destino cero, que dejaria el flujo mudo sin decir por que.
func TestTunnelRefusesQuiiWithoutDestination(t *testing.T) {
	tr, _ := newTestTunnel(t)
	key := make([]byte, 32)
	for i := range key {
		key[i] = byte(i + 1)
	}
	tr.key = key
	tr.password = "clave"

	if err := tr.sendQuiiSetup(); err == nil {
		t.Fatal("sendQuiiSetup deberia exigir destino")
	}
	if err := tr.sendQuii([]byte{0xA9}); err == nil {
		t.Fatal("sendQuii deberia exigir destino")
	}
	if err := tr.sendQuiiPlay(); err == nil {
		t.Fatal("sendQuiiPlay deberia exigir destino")
	}
	if err := tr.sendQuiiKeepalive(); err == nil {
		t.Fatal("sendQuiiKeepalive deberia exigir destino")
	}
}
