package main

import (
	"context"
	"crypto/tls"
	"encoding/binary"
	"encoding/json"
	"fmt"
	"log"
	"math/rand"
	"net"
	"net/url"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/gonzalop/mq"
)

const (
	ustVersion    = "v3.2.0"
	ustOEM        = "G0021"
	ustApp        = 2021
	ustClientType = "0"
)

// p2pConnectInfo es el content de la respuesta p2pconnect: los candidatos a los
// que hay que sondear y la sesion que el server asigna.
type p2pConnectInfo struct {
	ResultCode        int
	ResultMessage     string
	DeviceID          string
	SessionFlag       string
	ResponseSessionID int64
	NetType           int
	NetSubType        int
	PublicIP          string
	PublicUDPPort     int
	LocalUDPPort      int
	LocalIPs          []string
	UTDPublicIP       string
	UTDPublicUDPPort  int
}

// p2pTarget es un candidato a sondear, en el orden que marca la seleccion.
type p2pTarget struct {
	Kind     string
	IP       string
	Port     int
	TestMode int
}

// iterTargets ordena los candidatos: LAN privada, despues P2P publico, despues
// relay UTD. Los bits de nettype indican que caminos NO estan disponibles.
func iterTargets(info *p2pConnectInfo, forceTrans int) []p2pTarget {
	var out []p2pTarget
	if info == nil {
		return out
	}
	for _, ip := range info.LocalIPs {
		if p := net.ParseIP(ip); p != nil && p.IsPrivate() {
			if info.LocalUDPPort > 0 {
				out = append(out, p2pTarget{Kind: "lan", IP: ip, Port: info.LocalUDPPort, TestMode: 0})
			}
			break
		}
	}
	if forceTrans == 0 && info.NetType&0x1 == 0 && info.PublicIP != "" && info.PublicUDPPort > 0 {
		out = append(out, p2pTarget{Kind: "p2p", IP: info.PublicIP, Port: info.PublicUDPPort, TestMode: 0})
	}
	if info.NetType&0x2 == 0 && info.UTDPublicIP != "" && info.UTDPublicUDPPort > 0 {
		out = append(out, p2pTarget{Kind: "trans", IP: info.UTDPublicIP, Port: info.UTDPublicUDPPort, TestMode: 1})
	}
	return out
}

func (info *p2pConnectInfo) hasProbeTargets() bool {
	return len(iterTargets(info, 0)) > 0
}

type ustMqttConfig struct {
	BrokerURL string
	ClientID  string
	DeviceID  string
	Session   string
	Attempts  int
	// AwakePoll es la espera entre sondeos cuando el timbre esta dormido. Un
	// portal comunitario pasa horas sin actividad, asi que un vigilante largo
	// necesita un intervalo amplio para no saturar el broker.
	AwakePoll time.Duration
	// Budget acota el tiempo total del bucle de p2pconnect. Sin el, las
	// respuestas de "dormido" llegan en milisegundos y los intentos se
	// agotan en medio minuto, justo cuando el timbre empieza a despertar.
	Budget     time.Duration
	ForceTrans int
}

type ustMqtt struct {
	cfg ustMqttConfig

	client   *mq.Client
	inbox    chan ustIncoming
	userdata string

	serverIP   uint32
	serverPort int
	counter    int

	mu sync.Mutex
}

type ustIncoming struct {
	Topic   string
	Payload string
}

func newUserdata() string {
	n := 10_000_000_000 + rand.Int63n(89_999_999_999)
	return strconv.FormatInt(n, 10)
}

func newUstMqtt(cfg ustMqttConfig) *ustMqtt {
	if cfg.Attempts <= 0 {
		cfg.Attempts = 20
	}
	if cfg.Budget <= 0 {
		cfg.Budget = 240 * time.Second
	}
	if cfg.AwakePoll <= 0 {
		cfg.AwakePoll = 3 * time.Second
	}
	if cfg.Session == "" {
		cfg.Session = ""
	}
	return &ustMqtt{cfg: cfg, userdata: newUserdata(), inbox: make(chan ustIncoming, 256)}
}

type ustClientInfo struct {
	ID   string `json:"id"`
	Type string `json:"type"`
	OEM  string `json:"oem"`
	App  string `json:"app"`
}

type ustHeader struct {
	Flag     string        `json:"flag"`
	Version  string        `json:"version"`
	Command  string        `json:"command"`
	Client   ustClientInfo `json:"client"`
	Userdata string        `json:"userdata"`
	Seq      *int          `json:"seq,omitempty"`
	Session  string        `json:"session,omitempty"`
}

type ustMessage struct {
	Header  ustHeader   `json:"header"`
	Content interface{} `json:"content,omitempty"`
}

type ustSubDeviceState struct {
	DevID []string `json:"devid"`
}

type ustAwaken struct {
	DevID string `json:"devid"`
}

type ustKCPParam struct {
	Mode string `json:"mode"`
}

type ustDevTrans struct {
	MonChn int `json:"monChn"`
}

type ustP2PConnect struct {
	DevID         string      `json:"devid"`
	SessionFlag   string      `json:"session-flag"`
	RequSessionID int64       `json:"requ-session-id"`
	ForceTrans    int         `json:"force-trans"`
	KCPParam      ustKCPParam `json:"kcpParam"`
	DevTrans      ustDevTrans `json:"devTrans"`
	DevType       string      `json:"devType"`
	DevSubState   string      `json:"devSubState"`
}

type ustNetinfo struct {
	NetType       int      `json:"nettype"`
	NetSubType    int      `json:"netsubtype"`
	PublicIP      string   `json:"pub-ip"`
	PublicUDPPort int      `json:"pub-udpport"`
	LocalIPs      []string `json:"loc-ip"`
	LocalUDPPort  int      `json:"loc-udp-port"`
}

func (m *ustMqtt) header(command string) ustHeader {
	h := ustHeader{
		Flag:    "tdkcloud",
		Version: ustVersion,
		Command: command,
		Client: ustClientInfo{
			ID:   m.cfg.ClientID,
			Type: ustClientType,
			OEM:  ustOEM,
			App:  strconv.Itoa(ustApp),
		},
		Userdata: m.userdata,
	}
	if m.cfg.Session != "" {
		zero := 0
		h.Seq = &zero
		h.Session = m.cfg.Session
	}
	return h
}

func (m *ustMqtt) buildPayload(command string, content interface{}) ([]byte, error) {
	return json.Marshal(ustMessage{Header: m.header(command), Content: content})
}

func (m *ustMqtt) topic() string { return "app/ust/json/" + m.cfg.ClientID }

// connect abre el canal MQTT contra el broker UST y deja dos suscripciones: la del
// topic propio y la que usa el server para el bootstrap. Nunca se suscribe "#":
// el broker expulsa al cliente.
func (m *ustMqtt) connect() error {
	user, pass, err := decodeMQTTCredentials(m.cfg.BrokerURL, m.cfg.ClientID)
	if err != nil {
		return err
	}
	u, err := url.Parse(m.cfg.BrokerURL)
	if err != nil {
		return fmt.Errorf("url del broker ilegible: %w", err)
	}
	host := u.Hostname()
	if host == "" {
		return fmt.Errorf("url del broker sin host: %s", m.cfg.BrokerURL)
	}
	port := 8883
	if p := u.Port(); p != "" {
		port, _ = strconv.Atoi(p)
	}
	if ip := net.ParseIP(host); ip != nil {
		if v4 := ip.To4(); v4 != nil {
			m.serverIP = binary.BigEndian.Uint32(v4)
		}
	} else if addrs, lerr := net.LookupIP(host); lerr == nil {
		for _, a := range addrs {
			if v4 := a.To4(); v4 != nil {
				m.serverIP = binary.BigEndian.Uint32(v4)
				break
			}
		}
	}
	m.serverPort = port

	will, err := m.buildPayload("unregister", nil)
	if err != nil {
		return err
	}
	handler := func(_ *mq.Client, msg mq.Message) {
		select {
		case m.inbox <- ustIncoming{Topic: msg.Topic, Payload: string(msg.Payload)}:
		default:
		}
	}
	client, err := mq.Dial(
		fmt.Sprintf("ssl://%s:%d", host, port),
		mq.WithClientID("app_"+m.cfg.ClientID+"_"+m.userdata+"_"),
		mq.WithCredentials(user, pass),
		// El broker es antiguo (el app oficial usa Paho 1.2.5 Java): 3.1.1 fijo.
		mq.WithProtocolVersion(4),
		mq.WithKeepAlive(30*time.Second),
		mq.WithAutoReconnect(true),
		mq.WithConnectTimeout(10*time.Second),
		// El cert del broker Qualvision no tiene subjectAltNames.
		mq.WithTLS(&tls.Config{InsecureSkipVerify: true}),
		mq.WithWill(m.topic(), will, 0, false),
		mq.WithDefaultPublishHandler(handler),
		mq.WithIncomingQueueSize(256),
	)
	if err != nil {
		return fmt.Errorf("broker UST: %w", err)
	}
	m.client = client

	// Dos suscripciones: el topic propio y el que usa el server para el bootstrap.
	// Nunca "#": el broker expulsa al cliente.
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	for _, t := range []string{m.topic(), m.cfg.ClientID + "/ust/json"} {
		tok := client.Subscribe(ctx, t, mq.AtMostOnce, handler)
		if err := tok.Wait(ctx); err != nil {
			return fmt.Errorf("suscripcion %s fallida: %w", t, err)
		}
	}
	return nil
}

func (m *ustMqtt) publish(command string, content interface{}) error {
	payload, err := m.buildPayload(command, content)
	if err != nil {
		return err
	}
	m.mu.Lock()
	client := m.client
	m.mu.Unlock()
	if client == nil {
		return fmt.Errorf("mqtt no conectado")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	tok := client.Publish(ctx, m.topic(), payload, mq.WithQoS(mq.AtMostOnce))
	if err := tok.Wait(ctx); err != nil {
		return fmt.Errorf("publicando %s: %w", command, err)
	}
	return nil
}

// pollMessage saca el siguiente mensaje y le quita el envoltorio si lo trae.
func (m *ustMqtt) pollMessage(timeout time.Duration) (map[string]interface{}, bool) {
	select {
	case in := <-m.inbox:
		if debugOn() {
			log.Printf("mqtt rx topic=%s payload=%s", in.Topic, truncate(in.Payload, 600))
		}
		var raw map[string]interface{}
		if err := json.Unmarshal([]byte(in.Payload), &raw); err != nil {
			return nil, false
		}
		return extractInnerJSON(raw), true
	case <-time.After(timeout):
		return nil, false
	}
}

// extractInnerJSON desenvuelve los payloads que llegan como {"d":"<json>"}.
func extractInnerJSON(obj map[string]interface{}) map[string]interface{} {
	if _, ok := obj["header"]; ok {
		return obj
	}
	for _, v := range obj {
		s, ok := v.(string)
		if !ok || !strings.HasPrefix(strings.TrimSpace(s), "{") {
			continue
		}
		var inner map[string]interface{}
		if err := json.Unmarshal([]byte(s), &inner); err == nil {
			return inner
		}
	}
	return obj
}

func jsonCommand(obj map[string]interface{}) string {
	if h, ok := obj["header"].(map[string]interface{}); ok {
		if c, ok := h["command"].(string); ok {
			return c
		}
	}
	if c, ok := obj["command"].(string); ok {
		return c
	}
	return ""
}

func (m *ustMqtt) waitCommand(command string, timeout time.Duration) (map[string]interface{}, error) {
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		obj, ok := m.pollMessage(500 * time.Millisecond)
		if !ok {
			continue
		}
		if jsonCommand(obj) == command {
			return obj, nil
		}
	}
	return nil, fmt.Errorf("timeout esperando %s", command)
}

// waitDeviceOnline espera el estado del timbre, pero no bloquea el flujo si el
// cloud no lo emite: el p2pconnect puede responder igual.
func (m *ustMqtt) waitDeviceOnline(timeout time.Duration) {
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		obj, ok := m.pollMessage(500 * time.Millisecond)
		if !ok {
			continue
		}
		text, err := json.Marshal(obj)
		if err != nil {
			continue
		}
		s := string(text)
		if strings.Contains(s, m.cfg.DeviceID) && strings.Contains(s, `"online"`) {
			return
		}
	}
}

func jsonInt(v interface{}) int {
	switch t := v.(type) {
	case float64:
		return int(t)
	case int:
		return t
	case json.Number:
		n, _ := t.Int64()
		return int(n)
	case string:
		n, _ := strconv.Atoi(t)
		return n
	}
	return 0
}

func jsonInt64(v interface{}) int64 {
	switch t := v.(type) {
	case float64:
		return int64(t)
	case json.Number:
		n, _ := t.Int64()
		return n
	case string:
		n, _ := strconv.ParseInt(t, 10, 64)
		return n
	}
	return 0
}

func jsonString(v interface{}) string {
	if s, ok := v.(string); ok {
		return s
	}
	return ""
}

func jsonStringList(v interface{}) []string {
	list, ok := v.([]interface{})
	if !ok {
		return nil
	}
	out := make([]string, 0, len(list))
	for _, it := range list {
		if s, ok := it.(string); ok {
			out = append(out, s)
		}
	}
	return out
}

func p2pConnectInfoFrom(content map[string]interface{}) *p2pConnectInfo {
	info := &p2pConnectInfo{
		DeviceID:          jsonString(content["devid"]),
		SessionFlag:       jsonString(content["session-flag"]),
		ResponseSessionID: jsonInt64(content["resp-session-id"]),
		NetType:           jsonInt(content["nettype"]),
		NetSubType:        jsonInt(content["netsubtype"]),
		PublicIP:          jsonString(content["pub-ip"]),
		PublicUDPPort:     jsonInt(content["pub-udpport"]),
		LocalUDPPort:      jsonInt(content["loc-udpport"]),
		LocalIPs:          jsonStringList(content["loc-ip"]),
		UTDPublicIP:       jsonString(content["utd-pub-ip"]),
		UTDPublicUDPPort:  jsonInt(content["utd-pub-udpport"]),
	}
	if result, ok := content["result"].(map[string]interface{}); ok {
		info.ResultCode = jsonInt(result["code"])
		info.ResultMessage = jsonString(result["message"])
	} else {
		info.ResultCode = jsonInt(content["result"])
		info.ResultMessage = jsonString(content["result-message"])
	}
	return info
}

// deviceAsleep lee el estado que publica el servidor en sub-device-state. Cuando
// el timbre esta en aoffline no va a responder al p2pconnect, asi que no tiene
// sentido esperar el timeout entero.
func (m *ustMqtt) deviceAsleep(obj map[string]interface{}) bool {
	content, ok := obj["content"].(map[string]interface{})
	if !ok {
		return false
	}
	state, ok := content["state-s"].(map[string]interface{})
	if !ok {
		return false
	}
	for _, id := range jsonStringList(state["aoffline"]) {
		if id == m.cfg.DeviceID {
			return true
		}
	}
	return false
}

// deviceOnline es el complemento de deviceAsleep: el servidor lo lista en
// aonline cuando el timbre ha despertado y admite p2pconnect.
func (m *ustMqtt) deviceOnline(obj map[string]interface{}) bool {
	content, ok := obj["content"].(map[string]interface{})
	if !ok {
		return false
	}
	state, ok := content["state-s"].(map[string]interface{})
	if !ok {
		return false
	}
	for _, id := range jsonStringList(state["aonline"]) {
		if id == m.cfg.DeviceID {
			return true
		}
	}
	return false
}

// waitDeviceAwake sondea el estado hasta ver el timbre online. Con el timbre
// dormido no se manda p2pconnect: la sesion se quedaria abierta en el
// dispositivo y la app oficial recibe "placa ocupada con otra conversacion".
func (m *ustMqtt) waitDeviceAwake(timeout time.Duration) bool {
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		obj, ok := m.pollMessage(500 * time.Millisecond)
		if !ok {
			continue
		}
		if jsonCommand(obj) == "sub-device-state" && m.deviceOnline(obj) {
			return true
		}
	}
	return false
}

// waitP2PConnectResponse devuelve los candidatos, o asleep=true si el servidor
// ha dicho que el timbre esta dormido (para reintentar sin agotar el timeout).
func (m *ustMqtt) waitP2PConnectResponse(sessionFlag string, timeout time.Duration) (*p2pConnectInfo, bool) {
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		obj, ok := m.pollMessage(500 * time.Millisecond)
		if !ok {
			continue
		}
		if jsonCommand(obj) == "sub-device-state" && m.deviceAsleep(obj) {
			return nil, true
		}
		content, ok := obj["content"].(map[string]interface{})
		if !ok {
			continue
		}
		rxFlag := jsonString(content["session-flag"])
		// El server devuelve el flag truncado a 63 caracteres: se compara por prefijo.
		if rxFlag == "" || !(strings.HasPrefix(sessionFlag, rxFlag) || strings.HasPrefix(rxFlag, sessionFlag)) {
			if debugOn() {
				log.Printf("p2p skip: rxFlag=%q esperado=%q pub-ip=%q utd=%q",
					rxFlag, sessionFlag, jsonString(content["pub-ip"]), jsonString(content["utd-pub-ip"]))
			}
			continue
		}
		info := p2pConnectInfoFrom(content)
		if !info.hasProbeTargets() {
			if debugOn() {
				log.Printf("p2p sin candidatos: nettype=%d pub-ip=%q utd=%q", info.NetType, info.PublicIP, info.UTDPublicIP)
			}
			continue
		}
		return info, false
	}
	return nil, false
}

// buildSessionFlag arma el flag de 64 caracteres: letras al azar, el device id sin
// sus 4 primeros caracteres, y la ip/puerto del broker con un contador.
func (m *ustMqtt) buildSessionFlag() string {
	m.counter++
	base := fmt.Sprintf("%08x%05d%05d", m.serverIP, m.serverPort, m.counter&0xFFFF)
	devSuffix := m.cfg.DeviceID
	if len(devSuffix) > 4 {
		devSuffix = devSuffix[4:]
	}
	prefixLen := 64 - len(base) - len(devSuffix)
	if prefixLen < 0 {
		prefixLen = 0
	}
	var letters strings.Builder
	for letters.Len() < prefixLen {
		letters.WriteByte(byte('a' + rand.Intn(26)))
	}
	flag := letters.String() + devSuffix + base
	if len(flag) > 64 {
		flag = flag[:64]
	}
	return flag
}

// connectAndP2PConnect ejecuta el flujo completo hasta tener candidatos:
// unregister, register, sub-device-state, device-awaken y el bucle de p2pconnect.
// publishNetinfo republica el estado de red con nuestro mapping externo. Hay que
// repetirlo despues del natcheck: el timbre necesita saber donde responder.
func (m *ustMqtt) publishNetinfo(localIPs []string, localUDPPort int, publicIP string, publicUDPPort int) error {
	return m.publish("update-netinfo", ustNetinfo{
		NetType:       4,
		NetSubType:    0,
		PublicIP:      publicIP,
		PublicUDPPort: publicUDPPort,
		LocalIPs:      localIPs,
		LocalUDPPort:  localUDPPort,
	})
}

func (m *ustMqtt) connectAndP2PConnect(localIPs []string, localUDPPort int, publicIP string) (*p2pConnectInfo, string, error) {
	if err := m.connect(); err != nil {
		return nil, "", err
	}
	if err := m.publish("unregister", nil); err != nil {
		return nil, "", err
	}
	time.Sleep(100 * time.Millisecond)
	if err := m.publish("register", nil); err != nil {
		return nil, "", err
	}
	if _, err := m.waitCommand("register", 10*time.Second); err != nil {
		return nil, "", err
	}
	if err := m.publish("sub-device-state", ustSubDeviceState{DevID: []string{m.cfg.DeviceID}}); err != nil {
		return nil, "", err
	}
	if err := m.publish("device-awaken", ustAwaken{DevID: m.cfg.DeviceID}); err != nil {
		return nil, "", err
	}
	m.waitDeviceOnline(10 * time.Second)

	sessionFlag := m.buildSessionFlag()
	requestSessionID := int64(rand.Uint32())

	var info *p2pConnectInfo
	deadline := time.Now().Add(m.cfg.Budget)
	attempt := 0
	for attempt < m.cfg.Attempts {
		if time.Now().After(deadline) {
			break
		}
		attempt++
		// El aoffline de sub-device-state no es fiable: medido el 5-nov-2026,
		// el servidor daba el timbre por dormido en el mismo minuto en que
		// otro cliente obtenia candidatos y abria el tunel. Por eso aqui se
		// manda p2pconnect SIEMPRE, y el estado/despertar solo se piden como
		// intento adicional en cada vuelta.
		if attempt > 1 {
			if err := m.publish("sub-device-state", ustSubDeviceState{DevID: []string{m.cfg.DeviceID}}); err != nil {
				return nil, "", err
			}
			if err := m.publish("device-awaken", ustAwaken{DevID: m.cfg.DeviceID}); err != nil {
				return nil, "", err
			}
		}
		content := ustP2PConnect{
			DevID:         m.cfg.DeviceID,
			SessionFlag:   sessionFlag,
			RequSessionID: requestSessionID,
			ForceTrans:    m.cfg.ForceTrans,
			KCPParam:      ustKCPParam{Mode: "normal"},
			DevTrans:      ustDevTrans{MonChn: -1},
			DevType:       "normal",
			DevSubState:   "awakened",
		}
		if err := m.publish("p2pconnect", content); err != nil {
			return nil, "", err
		}
		if r, wasAsleep := m.waitP2PConnectResponse(sessionFlag, 12*time.Second); r != nil {
			info = r
			break
		} else if wasAsleep {
			if attempt == 1 || attempt%20 == 0 || debugOn() {
				log.Printf("p2p: el servidor da el timbre por dormido, se reintenta igual, intento %d/%d", attempt, m.cfg.Attempts)
			}
			// Las respuestas de dormido llegan en milisegundos: sin esta
			// espera los intentos se agotan antes de que el timbre despierte.
			time.Sleep(m.cfg.AwakePoll)
		}
	}
	if info == nil {
		return nil, "", fmt.Errorf("timeout esperando p2pconnect response (session-flag=%s)", sessionFlag)
	}

	if err := m.publishNetinfo(localIPs, localUDPPort, publicIP, localUDPPort); err != nil {
		return nil, "", err
	}
	return info, sessionFlag, nil
}

func (m *ustMqtt) close() {
	m.mu.Lock()
	client := m.client
	m.client = nil
	m.mu.Unlock()
	if client == nil {
		return
	}
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	_ = client.Disconnect(ctx)
}
