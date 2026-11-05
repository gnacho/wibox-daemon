package main

import (
	"encoding/json"
	"net"
	"strings"
	"testing"
)

func TestIterTargetsPicksOneOfEachKind(t *testing.T) {
	info := &p2pConnectInfo{
		LocalIPs:         []string{"8.8.8.8", "192.168.1.10", "10.0.0.5"},
		LocalUDPPort:     54450,
		NetType:          0,
		PublicIP:         "86.127.231.201",
		PublicUDPPort:    54450,
		UTDPublicIP:      "195.154.119.43",
		UTDPublicUDPPort: 30609,
	}
	got := iterTargets(info, 0)
	if len(got) != 3 {
		t.Fatalf("targets=%v, se esperaban 3", got)
	}
	if got[0].Kind != "lan" || got[0].IP != "192.168.1.10" || got[0].Port != 54450 {
		t.Fatalf("target lan = %+v", got[0])
	}
	if got[1].Kind != "p2p" || got[1].IP != "86.127.231.201" {
		t.Fatalf("target p2p = %+v", got[1])
	}
	if got[2].Kind != "trans" || got[2].TestMode != 1 {
		t.Fatalf("target trans = %+v", got[2])
	}
}

func TestIterTargetsRespectsNetTypeBits(t *testing.T) {
	// nettype 1 marca la red publica como no valida y 2 la utd, asi que solo
	// queda el candidato de la LAN.
	info := &p2pConnectInfo{
		LocalIPs:         []string{"192.168.1.10"},
		LocalUDPPort:     1234,
		NetType:          3,
		PublicIP:         "86.127.231.201",
		PublicUDPPort:    1234,
		UTDPublicIP:      "195.154.119.43",
		UTDPublicUDPPort: 99,
	}
	got := iterTargets(info, 0)
	if len(got) != 1 || got[0].Kind != "lan" {
		t.Fatalf("targets=%v, se esperaba solo la lan", got)
	}
}

func TestIterTargetsForceTransSkipsPublicPeer(t *testing.T) {
	info := &p2pConnectInfo{
		NetType:          0,
		PublicIP:         "86.127.231.201",
		PublicUDPPort:    1234,
		UTDPublicIP:      "195.154.119.43",
		UTDPublicUDPPort: 99,
	}
	got := iterTargets(info, 1)
	if len(got) != 1 || got[0].Kind != "trans" {
		t.Fatalf("targets=%v, con forceTrans solo cabe trans", got)
	}
}

func TestIterTargetsRejectsUnusableCandidates(t *testing.T) {
	if got := iterTargets(nil, 0); len(got) != 0 {
		t.Fatalf("nil deberia dar cero targets: %v", got)
	}
	info := &p2pConnectInfo{NetType: 3, PublicIP: "1.2.3.4", UTDPublicIP: "5.6.7.8"}
	if info.hasProbeTargets() {
		t.Fatal("sin direcciones privadas y con los dos bits puestos no hay targets")
	}
	// El puerto de la LAN a cero no vale como candidato.
	zero := &p2pConnectInfo{LocalIPs: []string{"192.168.1.10"}, LocalUDPPort: 0}
	if got := iterTargets(zero, 0); len(got) != 0 {
		t.Fatalf("targets con puerto cero: %v", got)
	}
}

func TestNewUserdataIsElevenDigits(t *testing.T) {
	for i := 0; i < 50; i++ {
		v := newUserdata()
		if len(v) != 11 {
			t.Fatalf("userdata %q tiene %d digitos", v, len(v))
		}
		for _, r := range v {
			if r < '0' || r > '9' {
				t.Fatalf("userdata %q no es numerico", v)
			}
		}
	}
}

func TestHeaderAndPayloadCarryTheClient(t *testing.T) {
	m := &ustMqtt{cfg: ustMqttConfig{ClientID: "003-2021-87dad215f30f1f7e"}}
	m.userdata = "12345678901"

	raw, err := m.buildPayload("register", map[string]string{"devid": "abc"})
	if err != nil {
		t.Fatalf("buildPayload: %v", err)
	}
	var msg struct {
		Header struct {
			Flag     string `json:"flag"`
			Version  string `json:"version"`
			Command  string `json:"command"`
			Userdata string `json:"userdata"`
			Session  string `json:"session"`
			Seq      *int   `json:"seq"`
			Client   struct {
				ID   string `json:"id"`
				Type string `json:"type"`
				OEM  string `json:"oem"`
				App  string `json:"app"`
			} `json:"client"`
		} `json:"header"`
		Content map[string]string `json:"content"`
	}
	if err := json.Unmarshal(raw, &msg); err != nil {
		t.Fatalf("payload ilegible: %v", err)
	}
	if msg.Header.Command != "register" || msg.Header.Flag != "tdkcloud" {
		t.Fatalf("header = %+v", msg.Header)
	}
	if msg.Header.Client.ID != m.cfg.ClientID || msg.Header.Client.App != "2021" {
		t.Fatalf("client = %+v", msg.Header.Client)
	}
	if msg.Header.Seq != nil {
		t.Fatalf("sin sesion no debe llevar seq: %v", *msg.Header.Seq)
	}
	if msg.Content["devid"] != "abc" {
		t.Fatalf("content = %v", msg.Content)
	}

	// Con sesion aparecen seq y session, que es lo que espera el broker.
	m.cfg.Session = "SESION"
	raw, err = m.buildPayload("p2pconnect", nil)
	if err != nil {
		t.Fatalf("buildPayload con sesion: %v", err)
	}
	var withSession map[string]interface{}
	if err := json.Unmarshal(raw, &withSession); err != nil {
		t.Fatalf("payload ilegible: %v", err)
	}
	header := withSession["header"].(map[string]interface{})
	if header["session"] != "SESION" {
		t.Fatalf("header sin sesion: %v", header)
	}
	if seq, ok := header["seq"].(float64); !ok || seq != 0 {
		t.Fatalf("seq = %v", header["seq"])
	}
}

func TestTopicUsesTheClientID(t *testing.T) {
	m := &ustMqtt{cfg: ustMqttConfig{ClientID: "003-2021-87dad215f30f1f7e"}}
	if got := m.topic(); got != "app/ust/json/003-2021-87dad215f30f1f7e" {
		t.Fatalf("topic = %q", got)
	}
}

func TestExtractInnerJSONUnwrapsPayload(t *testing.T) {
	inner := map[string]interface{}{"header": map[string]interface{}{"command": "p2pconnect"}}
	wrapped := map[string]interface{}{"d": `{"header":{"command":"p2pconnect"}}`}
	got := extractInnerJSON(wrapped)
	if jsonCommand(got) != "p2pconnect" {
		t.Fatalf("inner = %v", got)
	}
	// Un objeto que ya trae header se devuelve tal cual.
	if out := extractInnerJSON(inner); jsonCommand(out) != "p2pconnect" {
		t.Fatalf("objeto con header alterado: %v", out)
	}
	plain := map[string]interface{}{"d": "no es json", "n": 1}
	if out := extractInnerJSON(plain); jsonCommand(out) != "" {
		t.Fatalf("objeto sin json interno: %v", out)
	}
}

func TestJSONCommandReadsHeaderOrRoot(t *testing.T) {
	if got := jsonCommand(map[string]interface{}{"header": map[string]interface{}{"command": "register"}}); got != "register" {
		t.Fatalf("command desde header = %q", got)
	}
	if got := jsonCommand(map[string]interface{}{"command": "update-netinfo"}); got != "update-netinfo" {
		t.Fatalf("command raiz = %q", got)
	}
	if got := jsonCommand(map[string]interface{}{}); got != "" {
		t.Fatalf("sin command = %q", got)
	}
}

func TestJSONHelpersConvertLoosely(t *testing.T) {
	if got := jsonInt(float64(7)); got != 7 {
		t.Fatalf("jsonInt float = %d", got)
	}
	if got := jsonInt("12"); got != 12 {
		t.Fatalf("jsonInt string = %d", got)
	}
	if got := jsonInt(nil); got != 0 {
		t.Fatalf("jsonInt nil = %d", got)
	}
	if got := jsonInt64(float64(1500)); got != 1500 {
		t.Fatalf("jsonInt64 float = %d", got)
	}
	if got := jsonInt64("x"); got != 0 {
		t.Fatalf("jsonInt64 basura = %d", got)
	}
	// Los campos que lee jsonString son cadenas en el wire, asi que un numero
	// no se convierte: devuelve vacio en vez de inventarse un valor.
	if got := jsonString(42); got != "" {
		t.Fatalf("jsonString numero = %q, se esperaba vacio", got)
	}
	if got := jsonString(nil); got != "" {
		t.Fatalf("jsonString nil = %q", got)
	}
	list := jsonStringList([]interface{}{"a", 2})
	if len(list) != 1 || list[0] != "a" {
		t.Fatalf("jsonStringList = %v, se esperaba solo el elemento de texto", list)
	}
	if got := jsonStringList("suelto"); len(got) != 0 {
		t.Fatalf("jsonStringList no lista = %v", got)
	}
}

func TestP2PConnectInfoFromContent(t *testing.T) {
	content := map[string]interface{}{
		"devid":           "23123csfewyk",
		"session-flag":    "flag",
		"resp-session-id": float64(196676032),
		"nettype":         float64(4),
		"netsubtype":      float64(0),
		"pub-ip":          "86.127.231.201",
		"pub-udpport":     float64(54450),
		"loc-udpport":     float64(54450),
		"loc-ip":          []interface{}{"192.168.1.10", "192.168.1.246"},
		"utd-pub-ip":      "195.154.119.43",
		"utd-pub-udpport": float64(17737),
		"result":          map[string]interface{}{"code": "0", "message": "ok"},
	}
	info := p2pConnectInfoFrom(content)
	if info == nil {
		t.Fatal("info nil")
	}
	if info.DeviceID != "23123csfewyk" || info.SessionFlag != "flag" {
		t.Fatalf("info = %+v", info)
	}
	if info.ResponseSessionID != 196676032 || info.NetType != 4 {
		t.Fatalf("info = %+v", info)
	}
	if len(info.LocalIPs) != 2 || info.LocalIPs[1] != "192.168.1.246" {
		t.Fatalf("loc-ip = %v", info.LocalIPs)
	}
	if info.UTDPublicIP != "195.154.119.43" || info.UTDPublicUDPPort != 17737 {
		t.Fatalf("utd = %+v", info)
	}
	if info.ResultCode != 0 || info.ResultMessage != "ok" {
		t.Fatalf("result = %d/%q", info.ResultCode, info.ResultMessage)
	}

	// El resultado tambien puede venir como numero suelto.
	flat := p2pConnectInfoFrom(map[string]interface{}{
		"result":         float64(-10029),
		"result-message": "rechazado",
	})
	if flat.ResultCode != -10029 || flat.ResultMessage != "rechazado" {
		t.Fatalf("result plano = %d/%q", flat.ResultCode, flat.ResultMessage)
	}
}

func TestDeviceStateHelpers(t *testing.T) {
	m := &ustMqtt{cfg: ustMqttConfig{DeviceID: "23123csfewyk"}}
	state := func(offline, online []interface{}) map[string]interface{} {
		return map[string]interface{}{
			"header": map[string]interface{}{"command": "sub-device-state"},
			"content": map[string]interface{}{
				"state-s": map[string]interface{}{
					"aoffline": offline,
					"aonline":  online,
				},
			},
		}
	}

	if !m.deviceAsleep(state([]interface{}{"23123csfewyk"}, []interface{}{})) {
		t.Fatal("deberia estar dormido")
	}
	if m.deviceOnline(state([]interface{}{"23123csfewyk"}, []interface{}{})) {
		t.Fatal("no deberia estar online")
	}
	if !m.deviceOnline(state([]interface{}{}, []interface{}{"23123csfewyk"})) {
		t.Fatal("deberia estar online")
	}
	other := state([]interface{}{"otro"}, []interface{}{})
	if m.deviceAsleep(other) || m.deviceOnline(other) {
		t.Fatal("otro dispositivo no cuenta")
	}
	if m.deviceAsleep(map[string]interface{}{}) {
		t.Fatal("un objeto sin content no puede dar dormido")
	}
}

func TestBuildSessionFlagHasSixtyFourChars(t *testing.T) {
	m := &ustMqtt{cfg: ustMqttConfig{DeviceID: "23123csfewyk"}, serverIP: 0x86_7f_e7_c9, serverPort: 54450}
	first := m.buildSessionFlag()
	if len(first) != 64 {
		t.Fatalf("flag de %d caracteres: %q", len(first), first)
	}
	if !strings.HasSuffix(first, "csfewyk") && !strings.Contains(first, "csfewyk") {
		t.Fatalf("el flag deberia llevar el sufijo del dispositivo: %q", first)
	}
	second := m.buildSessionFlag()
	if first == second {
		t.Fatal("dos flags seguidos no pueden ser iguales (el contador debe avanzar)")
	}
	if !net.ParseIP("192.168.1.10").IsPrivate() {
		t.Fatal("sanity de net.ParseIP")
	}
}
