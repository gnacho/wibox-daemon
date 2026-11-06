package main

import (
	"net"
	"testing"
	"time"
)

// close tiene que aguantar sesiones a medio montar: con el mqtt sin abrir, sin
// socket, o con los dos.
func TestRemoteSessionCloseIsSafe(t *testing.T) {
	(&remoteSession{}).close()

	conn, err := net.ListenUDP("udp", &net.UDPAddr{IP: net.IPv4(127, 0, 0, 1)})
	if err != nil {
		t.Fatalf("socket: %v", err)
	}
	(&remoteSession{conn: conn}).close()

	// Cerrado ya, un segundo close no puede petar el proceso.
	(&remoteSession{conn: conn}).close()
}

func TestExtractInnerJSON(t *testing.T) {
	// Lo que ya trae header se devuelve tal cual.
	direct := map[string]interface{}{"header": map[string]interface{}{"command": "register"}}
	if got := extractInnerJSON(direct); got["header"] == nil {
		t.Fatal("se perdio el header")
	}

	// Lo que llega envuelto como {"d":"<json>"} se desenvuelve.
	wrapped := map[string]interface{}{"d": `{"header":{"command":"sub-device-state"}}`}
	if got := extractInnerJSON(wrapped); jsonCommand(got) != "sub-device-state" {
		t.Fatalf("no se desenvuelve: %v", got)
	}

	// Basura: se devuelve el objeto original sin inventar nada.
	junk := map[string]interface{}{"d": "no soy json"}
	if got := extractInnerJSON(junk); got["d"] != "no soy json" {
		t.Fatalf("se invento algo: %v", got)
	}
}

func TestJSONCommand(t *testing.T) {
	if got := jsonCommand(map[string]interface{}{"header": map[string]interface{}{"command": "p2pconnect"}}); got != "p2pconnect" {
		t.Fatalf("%q", got)
	}
	if got := jsonCommand(map[string]interface{}{"command": "register"}); got != "register" {
		t.Fatalf("%q", got)
	}
	if got := jsonCommand(map[string]interface{}{}); got != "" {
		t.Fatalf("%q, se esperaba vacio", got)
	}
}

func newTestMqtt(deviceID string) *ustMqtt {
	return &ustMqtt{
		cfg:   ustMqttConfig{DeviceID: deviceID},
		inbox: make(chan ustIncoming, 8),
	}
}

func TestWaitCommandMatchesTheRightMessage(t *testing.T) {
	m := newTestMqtt("dev1")
	m.inbox <- ustIncoming{Topic: "t", Payload: `{"header":{"command":"otro"}}`}
	m.inbox <- ustIncoming{Topic: "t", Payload: `{"header":{"command":"p2pconnect"},"content":{"ip":"1.2.3.4"}}`}

	obj, err := m.waitCommand("p2pconnect", time.Second)
	if err != nil {
		t.Fatalf("no encontro el mensaje: %v", err)
	}
	if jsonCommand(obj) != "p2pconnect" {
		t.Fatalf("devolvio %q", jsonCommand(obj))
	}

	// Sin nada en la bandeja, agota el plazo en vez de colgarse para siempre.
	if _, err := m.waitCommand("nada", 30*time.Millisecond); err == nil {
		t.Fatal("deberia devolver timeout")
	}
}

func TestWaitDeviceOnlineReturnsWhenTheBellReportsOnline(t *testing.T) {
	m := newTestMqtt("dev1")
	m.inbox <- ustIncoming{Topic: "t", Payload: `{"content":{"devid":"dev1","state":"online"},"header":{"command":"sub-device-state"}}`}
	start := time.Now()
	m.waitDeviceOnline(2 * time.Second)
	if time.Since(start) > time.Second {
		t.Fatal("no salio al ver el timbre en linea")
	}
}
