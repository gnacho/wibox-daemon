package main

import (
	"fmt"
	"log"
	"net"
	"sync"
	"time"
)

// Tags de 8 bytes que acompanan a cada paquete envuelto.
var (
	rbPlayTag  = []byte{0x61, 0x00, 0x6e, 0x00, 0x64, 0x00, 0x72, 0x00}
	rbProbeTag = []byte{0x00, 0x00, 0x80, 0x3f, 0x00, 0x50, 0xc3, 0xc7}
)

const (
	rbProbeInnerDest uint32 = 0x0B030000

	playSyncLocalBias uint32 = 0x140
	playSyncStep      uint32 = 0x58C
	stage2LocalDelta  uint32 = 0x88
	stage4LocalDelta  uint32 = 0x88
	stage4RemoteBias  uint32 = 0xA1
)

// Biases negativos, en complemento a dos.
const (
	stage2RemoteBias uint32 = 0xFFFFFF0E // -0xF2
	stage3RemoteBias uint32 = 0xFFFFFEAE // -0x152
	stage5RemoteBias uint32 = 0xFFFFFF09 // -0xF7
	stage6RemoteBias uint32 = 0xFFFFFDFE // -0x202
)

// Limites del buffer de frames KCP sin enmarcar: por encima se resincroniza,
// y si crece demasiado se descarta en vez de arrastrar basura para siempre.
const (
	kcpResyncLimit = 512 * 1024
	kcpMaxBuffer   = 2 * 1024 * 1024
)

var playSyncStageStatuses = []uint32{0xFB540900, 0xFAD40900, 0xFE0D0900, 0xFC0C0900, 0xFB7F0900}

// Payloads de las sondas de sincronizacion del play.
var (
	playProbe2 = mustHex("c92a42d0f7ae17f0e7452a46ecfe5c4bc34b116aea1895dea5faec8f5a051f12628d07bf32b0020b7bc3fdee22102946aff8e68130346b50e4e7ce77f85693a266aeb42f1bfc291abe335ac2254602be")
	playProbe3 = mustHex("939cd332393f73adee029583c588f74b17a6d27bb938de0c904c2a9a4499830ccf41af8852573ff6841eb9e753bbe64e55a11539403019424d80b692aaf030d1d0148a96a307f1a1c34ab4722c9b4303")
	playProbe4 = mustHex("900ed22985e6500a6f60b4670871d7fdcded3931d7cfcca249494621adf3fba4e795166764cac338b143da5d0749726952aacd7f96452f0e5fd5c429c9ca106564340dca828431e2a6e3cf30b3c88fe5")
)

// rbTunnel mantiene el canal RB-UDP contra el timbre y lleva la maquina de
// estados del handshake. Todo el estado se toca con el mutex tomado.
type rbTunnel struct {
	conn        *net.UDPConn
	peer        *net.UDPAddr
	sessionFlag string
	key         []byte
	password    string
	channel     int
	stream      int

	mu                   sync.Mutex
	laneWord4            uint32
	laneWord8            uint32
	laneLocalID          uint32
	laneRemoteID         uint32
	rxStreamNextRemoteID uint32
	rxStreamKnown        bool
	rxStreamPackets      int
	rxKcpBuffer          []byte
	connectID            uint32
	peerLogicID          uint32
	flowSent             bool
	progressSent         bool
	setupProbeSent       bool
	pendingEstablished   bool
	quiiPlaySent         bool
	quiiPlayAcked        bool
	dumpSamples          int
	quiiPlayEndLocalID   uint32
	quiiSetupSent        bool
	connectSent          bool
	quiiDestID           uint32
	quiiNextSeq          uint32
	playSyncActive       bool
	playSyncStage        int
	playSyncLocalID      uint32
	playSyncRemoteID     uint32
	playSyncSentCount    int
	probeStage           int

	connected     chan struct{}
	connectedOnce sync.Once
	quiiReady     chan struct{}
	quiiReadyOnce sync.Once
	quiiIncoming  chan []byte

	stop     chan struct{}
	stopOnce sync.Once
	wg       sync.WaitGroup
}

func newRbTunnel(conn *net.UDPConn, peer *net.UDPAddr, sessionFlag string, key []byte, password string, channel, stream int) *rbTunnel {
	return &rbTunnel{
		conn:         conn,
		peer:         peer,
		sessionFlag:  sessionFlag,
		key:          key,
		password:     password,
		channel:      channel,
		stream:       stream,
		quiiNextSeq:  2,
		connected:    make(chan struct{}),
		quiiReady:    make(chan struct{}),
		quiiIncoming: make(chan []byte, 4096),
		stop:         make(chan struct{}),
	}
}

func (t *rbTunnel) start() {
	t.wg.Add(1)
	go t.receiveLoop()
	// Bootstrap: abre el canal con el timbre.
	t.mu.Lock()
	t.sendControl(ctrlBootstrap, 0, 0x01000000, 0, 0)
	t.mu.Unlock()
}

func (t *rbTunnel) close() {
	t.stopOnce.Do(func() { close(t.stop) })
	t.wg.Wait()
}

func (t *rbTunnel) awaitConnected(timeout time.Duration) error {
	select {
	case <-t.connected:
		return nil
	case <-time.After(timeout):
		return fmt.Errorf("tunel RB-UDP sin conectar en %s", timeout)
	}
}

func (t *rbTunnel) awaitQuiiReady(timeout time.Duration) error {
	select {
	case <-t.quiiReady:
		return nil
	case <-time.After(timeout):
		return fmt.Errorf("QUII sin listo en %s", timeout)
	}
}

func (t *rbTunnel) awaitQuiiDestID(timeout time.Duration) (uint32, error) {
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		t.mu.Lock()
		dest := t.quiiDestID
		t.mu.Unlock()
		if dest != 0 {
			return dest, nil
		}
		time.Sleep(100 * time.Millisecond)
	}
	return 0, fmt.Errorf("el timbre no asigno destino KCP en %s", timeout)
}

// --- envio (siempre con el mutex tomado) ---

func (t *rbTunnel) sendUDP(packet []byte) {
	_, _ = t.conn.WriteToUDP(packet, t.peer)
}

func (t *rbTunnel) sendControl(status, word4, word8, localID, remoteID uint32) {
	t.sendUDP(buildRbControl(word4, word8, localID, remoteID, status))
}

func (t *rbTunnel) sendWrapped(inner, tag8 []byte, statusWord uint32) {
	t.sendUDP(buildRbWrapped(inner, t.laneWord4, t.laneWord8, t.laneLocalID, t.laneRemoteID, statusWord, tag8))
	// El stack nativo avanza su id local con la longitud del interior.
	t.laneLocalID += uint32(len(inner))
}

func (t *rbTunnel) sendWrappedConnect() {
	if t.connectSent {
		return
	}
	t.connectSent = true
	t.sendWrapped(buildKcpConnect(rbSrcIDBase, 0, 0), rbConnectTag, rbWrappedStatus)
}

func (t *rbTunnel) sendWrappedData(payload []byte, seq uint32, tag8 []byte) {
	t.sendWrapped(buildRbData(payload, rbSrcIDBase, t.quiiDestID, seq), tag8, rbWrappedStatus)
}

func (t *rbTunnel) sendDataAck(payloadLength, seq uint32) {
	t.sendWrapped(buildRbDataAck(payloadLength, seq, t.laneLocalID, t.laneRemoteID), rbDataTag, rbWrappedStatus)
}

// sendOffsetAck contesta a un control invirtiendo las palabras y sumando el
// desplazamiento que espera el timbre.
func (t *rbTunnel) sendOffsetAck(status, remoteDelta uint32, c *rbControl) {
	t.sendControl(status, c.Word8+remoteDelta, c.Word4, t.laneLocalID, t.laneRemoteID)
}

// --- recepcion ---

func (t *rbTunnel) receiveLoop() {
	defer t.wg.Done()
	buf := make([]byte, 4096)
	stats := map[uint32]int{}
	lastStats := time.Now()
	totalDatagrams := 0
	totalBytes := 0
	for {
		select {
		case <-t.stop:
			return
		default:
		}
		_ = t.conn.SetReadDeadline(time.Now().Add(500 * time.Millisecond))
		n, _, err := t.conn.ReadFromUDP(buf)
		if err != nil {
			if ne, ok := err.(net.Error); ok && ne.Timeout() {
				continue
			}
			select {
			case <-t.stop:
				return
			default:
			}
			continue
		}
		if n < 4 {
			continue
		}
		raw := make([]byte, n)
		copy(raw, buf[:n])
		totalDatagrams++
		totalBytes += n
		if len(raw) >= 0x18 {
			stats[readU32(raw, 0x14)]++
		}
		if debugOn() && time.Since(lastStats) >= 5*time.Second {
			log.Printf("tunel rx %d datagramas, %d bytes, estados %v", totalDatagrams, totalBytes, stats)
			stats = map[uint32]int{}
			lastStats = time.Now()
		}
		t.mu.Lock()
		t.handleDatagram(raw)
		t.mu.Unlock()
	}
}

func (t *rbTunnel) handleDatagram(raw []byte) {
	if readU32(raw, 0) != rbMarker {
		return
	}
	status := readU32(raw, 0x14)
	// Volcado de los datagramas que no encajan en ningun tipo conocido: sin
	// esto solo se ve el contador del estado y no que lleva dentro.
	if debugOn() && t.dumpSamples < 12 {
		known := status&0x1000 != 0 || len(raw) == 28 ||
			status&0xFFFF == 0x0500 || status&0xFFFF == 0x1900
		if !known {
			t.dumpSamples++
			n := len(raw)
			if n > 48 {
				n = 48
			}
			log.Printf("tunel raw len=%d status=0x%08x first=%x", len(raw), status, raw[:n])
		}
	}
	if len(raw) >= 0x1C && status&0x1000 != 0 {
		t.handleDataPayload(raw)
		return
	}
	if len(raw) >= 0x20 {
		if readU32(raw, 0x1C) != 0xFFFFFFFF {
			switch status & 0xFFFF {
			case 0x0500:
				t.handleStreamPayload(raw)
			case 0x1900:
				t.handleDataPayload(raw)
			default:
				t.handleWrapped(raw)
			}
			return
		}
		t.handleWrapped(raw)
		return
	}
	if len(raw) == 28 {
		if c, err := parseRbControl(raw); err == nil {
			t.dispatchControl(c)
		}
	}
}

func (t *rbTunnel) emitQuii(payload []byte) {
	if len(payload) == 0 {
		return
	}
	buf := make([]byte, len(payload))
	copy(buf, payload)
	// NUNCA descartar en silencio: un paquete perdido deja un agujero en el
	// flujo y el H264 deja de ser decodificable. Si el canal esta lleno se
	// espera; el consumidor (el demuxer) va mucho mas rapido que la red.
	select {
	case t.quiiIncoming <- buf:
	case <-t.stop:
	}
}

func (t *rbTunnel) handleStreamPayload(raw []byte) {
	payload := raw[0x1C:]
	if len(payload) == 0 || allFill(payload) {
		return
	}
	if t.quiiPlaySent {
		t.emitQuii(payload)
	}
}

func (t *rbTunnel) handleDataPayload(raw []byte) {
	status16 := readU32(raw, 0x14) & 0xFFFF
	if status16 != 0x0500 && status16 != 0x1900 {
		return
	}
	buffer := raw[0x1C:]
	if len(buffer) == 0 {
		return
	}
	packetLocalID := readU32(raw, 0x0C)
	// Hueco: se perdio un datagrama o llego desordenado. Antes se descartaba
	// todo el resto del flujo para siempre y el video se quedaba congelado en
	// los primeros frames. Se resincroniza y se sigue: el H264 tolera mejor un
	// hueco que un flujo muerto.
	if t.rxStreamKnown && t.rxStreamNextRemoteID != packetLocalID {
		log.Printf("tunel: hueco en el flujo, esperado=%d llego=%d (resincronizo)", t.rxStreamNextRemoteID, packetLocalID)
		t.rxKcpBuffer = nil
		t.rxStreamKnown = false
	}
	if !t.rxStreamKnown || t.rxStreamNextRemoteID == packetLocalID {
		t.rxStreamKnown = true
		t.rxStreamNextRemoteID = packetLocalID + uint32(len(buffer))
		t.rxKcpBuffer = append(t.rxKcpBuffer, buffer...)
		offset := 0
		frames := 0
		for offset+0x18 <= len(t.rxKcpBuffer) {
			if readU32(t.rxKcpBuffer, offset) != 0xFFFFFFFF {
				break
			}
			packetLen := int(readU32(t.rxKcpBuffer, offset+4))
			if packetLen < 0x18 || packetLen > 20*1024*1024 {
				break
			}
			if packetLen > len(t.rxKcpBuffer)-offset {
				break
			}
			frame := make([]byte, packetLen)
			copy(frame, t.rxKcpBuffer[offset:offset+packetLen])
			t.dispatchKcpFrame(frame)
			offset += packetLen
			frames++
		}
		t.rxKcpBuffer = t.rxKcpBuffer[offset:]
		// Nunca se emite el buffer crudo como si fuera un paquete QUII: esos
		// bytes entran en el flujo del demuxer, lo desalinean y a partir de ahi
		// se pierde casi toda la media (medido: 102 frames offline contra 5 en
		// vivo por la misma sesion). Solo se emiten payloads de frames KCP.
		//
		// Si la cabeza del buffer no es un marco valido y ya se acumulo de mas,
		// se avanza un byte para resincronizar en vez de arrastrarlo para
		// siempre.
		if frames == 0 {
			if len(t.rxKcpBuffer) >= kcpResyncLimit {
				t.rxKcpBuffer = t.rxKcpBuffer[1:]
			}
			if len(t.rxKcpBuffer) > kcpMaxBuffer {
				log.Printf("tunel: %d bytes sin marco valido, se descartan", len(t.rxKcpBuffer))
				t.rxKcpBuffer = nil
			}
		}
	}
	// ACK acumulativo, tambien para duplicados y desordenados.
	t.sendControl(ctrlEstablished, readU32(raw, 0x08), readU32(raw, 0x04), t.laneLocalID, t.rxStreamNextRemoteID)
	t.rxStreamPackets++
}

func (t *rbTunnel) dispatchKcpFrame(frame []byte) {
	switch readU32(frame, 0x14) {
	case rbCmdConnect:
		t.handleConnectFrame(frame)
	case rbCmdData:
		data, err := parseRbData(frame)
		if err != nil {
			return
		}
		if isRbDataAck(frame) && data.Seq == 0 {
			t.quiiSetupSent = true
			t.emitQuii(data.Payload)
			return
		}
		if !isRbDataAck(frame) {
			t.emitQuii(data.Payload)
		}
	}
}

func (t *rbTunnel) handleConnectFrame(frame []byte) {
	resp, err := parseKcpConnectResponse(frame)
	if err != nil {
		return
	}
	if resp.ConnectID != rbSrcIDBase || resp.TypeFlag != 1 || resp.DestID == 0 {
		return
	}
	t.quiiDestID = resp.DestID
	if t.setupProbeSent {
		return
	}
	t.setupProbeSent = true
	t.quiiSetupSent = true
	// El setup de QUII va en claro, tal cual, como primer paquete de datos.
	t.sendWrappedData(buildQuiiSetup(), 0, rbDataTag)
}

func (t *rbTunnel) handleWrapped(raw []byte) {
	if len(raw) == 164 {
		return // frame de sonda
	}
	inner, _, err := parseRbWrapped(raw)
	if err != nil {
		return
	}
	data, err := parseRbData(inner)
	if err != nil {
		return
	}
	if isRbDataAck(inner) && data.Seq == 0 {
		t.quiiSetupSent = true
		t.emitQuii(data.Payload)
		return
	}
	switch data.Command {
	case rbCmdData:
		t.emitQuii(data.Payload)
	case rbCmdConnect:
		t.handleConnectFrame(inner)
	}
}

func (t *rbTunnel) dispatchControl(c *rbControl) {
	if debugOn() {
		log.Printf("tunel ctrl 0x%08x local=%d remote=%d lane=%d/%d", c.StatusWord, c.LocalID, c.RemoteID, t.laneLocalID, t.laneRemoteID)
	}
	if c.StatusWord == ctrlSynAck && c.RemoteID != 0 {
		t.laneWord4 = c.Word8
		t.laneWord8 = 0x01000000
		t.laneLocalID = c.RemoteID
		t.laneRemoteID = c.RemoteID
		t.sendControl(ctrlEstablished, t.laneWord4, t.laneWord8, t.laneLocalID, t.laneRemoteID)
		t.sendWrappedConnect()
		t.connectedOnce.Do(func() { close(t.connected) })
	}
	if c.StatusWord == ctrlPostConnect {
		t.peerLogicID = c.Word4
		t.sendOffsetAck(ctrlPostConnectAck, 0x38, c)
	}

	// El ack del play no tiene constante propia en el cliente oficial: llega
	// con status16 0x0900 y el remote-id igual al local-id con el que quedamos
	// al mandar el play. El cliente que funciono en el movil no le responde
	// nada y deja que el play sync arranque con el ESTABLISHED siguiente, que
	// trae los ids del lane ya avanzados. Responder aqui arrancaba el play
	// sync con ids atrasados y el timbre contestaba PLAY_LATE en bucle.
	if c.StatusWord&0xFFFF == 0x0900 && t.quiiPlaySent && !t.quiiPlayAcked &&
		t.quiiPlayEndLocalID > 0 && c.RemoteID == t.quiiPlayEndLocalID && c.LocalID > 1 {
		t.quiiPlayAcked = true
		if debugOn() {
			log.Printf("tunel: ack del play local=%d remote=%d", c.LocalID, c.RemoteID)
		}
	} else {
		switch c.StatusWord {
		case ctrlEstablished:
			t.handleEstablished(c)
		case ctrlProgress:
			t.handleProgress(c)
		case ctrlPostSetup:
			t.sendOffsetAck(ctrlPostSetupAck, 0x30, c)
			t.quiiSetupSent = true
		case ctrlPostPlay:
			t.sendOffsetAck(ctrlPostPlayAck, 0x34, c)
			t.quiiPlaySent = true
		case ctrlStreamReady:
			t.sendOffsetAck(ctrlStreamReadyAck, 0x3C, c)
			t.startPlaySync(c.LocalID)
		case ctrlPlaySync:
			t.handlePlaySync(c)
		case ctrlPlayLate:
			// El timbre pide el ack del play sync. Si aun no habia arrancado,
			// se arranca aqui: sin esto se reenviaria una etapa inventada.
			if t.playSyncActive {
				t.resendPlayStageAck()
			} else {
				t.startPlaySync(c.LocalID)
			}
		default:
			if c.StatusWord&0xFFFF == 0x0D00 {
				t.handleHeartbeat(c)
			}
		}
	}

	if t.pendingEstablished {
		t.pendingEstablished = false
		t.sendControl(ctrlEstablished, t.laneWord4, t.laneWord8, t.laneLocalID, t.laneRemoteID)
	}
}

func (t *rbTunnel) handleHeartbeat(c *rbControl) {
	if c.LocalID > 1 && c.RemoteID > 1 {
		t.sendDataAck(0, 0)
		t.sendControl(ctrlHeartbeatRequest, c.Word8, c.Word4, t.laneLocalID, t.laneRemoteID)
		return
	}
	t.handleFlow(c)
}

func (t *rbTunnel) handleFlow(c *rbControl) {
	if c.LocalID == 1 && t.peerLogicID != 0 && !t.flowSent {
		t.sendControl(ctrlFlow, t.laneWord4, t.laneWord8, t.peerLogicID, 1)
		t.flowSent = true
	}
}

func (t *rbTunnel) handleEstablished(c *rbControl) {
	if t.quiiPlaySent && c.LocalID > 1 && c.RemoteID > 1 {
		t.sendControl(ctrlPlayEstablishedAck, t.laneWord4, t.laneWord8, c.RemoteID, c.LocalID)
		t.startPlaySync(c.LocalID)
		return
	}
	if !t.connectSent {
		t.pendingEstablished = true
	}
}

func (t *rbTunnel) handleProgress(c *rbControl) {
	if t.peerLogicID == 0 {
		return
	}
	if c.RemoteID == t.peerLogicID && !t.progressSent && !t.setupProbeSent {
		t.sendWrappedConnect()
		t.setupProbeSent = true
		return
	}
	if c.LocalID == t.peerLogicID && c.RemoteID == t.peerLogicID && !t.progressSent {
		t.sendControl(ctrlProgress, t.laneWord4, t.laneWord8, t.peerLogicID, t.peerLogicID)
		t.progressSent = true
		t.sendWrappedConnect()
	}
}

// --- play sync ---

func (t *rbTunnel) startPlaySync(base uint32) {
	if t.playSyncActive || base <= 1 {
		return
	}
	t.playSyncActive = true
	if debugOn() {
		log.Printf("tunel: play sync arrancado local=%d remote=%d", base+playSyncLocalBias, base+playSyncStep)
	}
	t.playSyncLocalID = base + playSyncLocalBias
	t.playSyncRemoteID = base + playSyncStep
	t.playSyncStage = 0
	t.playSyncSentCount = 0
	t.probeStage = 0
	t.wg.Add(1)
	go t.playSyncLoop()
}

func (t *rbTunnel) playSyncLoop() {
	defer t.wg.Done()
	ticker := time.NewTicker(100 * time.Millisecond)
	defer ticker.Stop()
	for {
		select {
		case <-t.stop:
			return
		case <-t.quiiReady:
			return
		case <-ticker.C:
			t.mu.Lock()
			if t.playSyncActive {
				t.playSyncSentCount++
				t.maybeSendPlayProbe()
			}
			t.mu.Unlock()
		}
	}
}

func (t *rbTunnel) handlePlaySync(c *rbControl) {
	if t.playSyncStage >= len(playSyncStageStatuses) {
		t.markQuiiReady()
		return
	}
	t.sendPlayStageAck(playSyncStageStatuses[t.playSyncStage])
	t.playSyncStage++
	if debugOn() {
		log.Printf("tunel: play sync etapa %d", t.playSyncStage)
	}
	switch t.playSyncStage {
	case 1:
		t.playSyncRemoteID += stage2RemoteBias
	case 2:
		t.playSyncRemoteID += stage3RemoteBias
	case 3:
		t.playSyncLocalID += stage4LocalDelta
		t.playSyncRemoteID += stage4RemoteBias
	case 4:
		t.playSyncRemoteID += stage5RemoteBias
	case 5:
		t.playSyncRemoteID += stage6RemoteBias
	}
	if t.playSyncStage >= len(playSyncStageStatuses) {
		t.markQuiiReady()
	}
}

func (t *rbTunnel) markQuiiReady() {
	t.quiiReadyOnce.Do(func() {
		log.Printf("tunel: quii listo, play sync completo en %d etapas", t.playSyncStage)
		close(t.quiiReady)
	})
}

func (t *rbTunnel) resendPlayStageAck() {
	idx := t.playSyncStage - 1
	if idx < 0 {
		idx = 0
	}
	if idx >= len(playSyncStageStatuses) {
		idx = len(playSyncStageStatuses) - 1
	}
	t.sendPlayStageAck(playSyncStageStatuses[idx])
}

func (t *rbTunnel) sendPlayStageAck(status uint32) {
	t.sendControl(status, t.laneWord4, t.laneWord8, t.playSyncLocalID, t.playSyncRemoteID)
}

func (t *rbTunnel) maybeSendPlayProbe() {
	switch t.probeStage {
	case 0:
		if t.playSyncSentCount >= 18 {
			t.sendPlayProbe(2, -0x88, 0x118, playProbe2)
			t.probeStage = 1
		}
	case 1:
		if t.playSyncSentCount >= 35 {
			t.playSyncLocalID += stage2LocalDelta
			t.playSyncRemoteID += stage2RemoteBias
			t.sendPlayProbe(3, 0, -0x12, playProbe3)
			t.probeStage = 2
		}
	case 2:
		if t.playSyncSentCount >= 48 {
			t.sendPlayProbe(4, 0, -0x152, playProbe4)
			t.probeStage = 3
		}
	}
}

func (t *rbTunnel) sendPlayProbe(seq uint32, localDelta, remoteDelta int, probe []byte) {
	inner := buildRbData(probe, rbSrcIDBase, rbProbeInnerDest, seq)
	t.sendUDP(buildRbWrapped(inner, t.laneWord4, t.laneWord8,
		uint32(int(t.playSyncLocalID)+localDelta),
		uint32(int(t.playSyncRemoteID)+remoteDelta),
		rbWrappedStatus, rbProbeTag))
}

// --- QUII dentro del tunel ---

// sendQuiiSetup manda el setup en claro (32 bytes con 0xA9 delante).
func (t *rbTunnel) sendQuiiSetup() error {
	t.mu.Lock()
	defer t.mu.Unlock()
	if t.quiiDestID == 0 {
		return fmt.Errorf("sin destino KCP todavia")
	}
	t.sendWrappedData(buildQuiiSetup(), 0, rbDataTag)
	t.quiiSetupSent = true
	return nil
}

// sendQuiiPlay manda el paquete de play cifrado.
func (t *rbTunnel) sendQuiiPlay() error {
	t.mu.Lock()
	defer t.mu.Unlock()
	return t.sendQuiiPlayLocked(1)
}

// sendQuiiPlayAgain reenvia el play con una secuencia nueva. Con la misma
// secuencia el timbre lo descarta como duplicado y no reinicia el stream, que
// es lo unico que provoca un IDR nuevo. Medido: 21 reenvios con seq 1 y cero
// keyframes.
func (t *rbTunnel) sendQuiiPlayAgain() error {
	t.mu.Lock()
	defer t.mu.Unlock()
	return t.sendQuiiPlayLocked(t.quiiNextSeq)
}

func (t *rbTunnel) sendQuiiPlayLocked(seq uint32) error {
	if t.quiiDestID == 0 {
		return fmt.Errorf("sin destino KCP todavia")
	}
	if len(t.key) != 32 {
		return fmt.Errorf("data-encode-key de %d bytes", len(t.key))
	}
	path := fmt.Sprintf("/mode=real&idc=%d&ids=%d&ap=2", t.channel, t.stream)
	// El camino remoto del cliente que funciono en el movil usa adminapp; el
	// adminapp2 es el de la sesion LAN y del CGI.
	body := buildPlayBody("adminapp", t.password, path)
	packet, err := encryptPacketAt(t.key, 0x01, body,
		byte(t.channel&0xFF), byte((t.channel>>8)&0xFF), 1, byte((t.stream-1)&0xFF), 0,
		uint64(time.Now().Unix()))
	if err != nil {
		return err
	}
	t.sendWrappedData(packet, seq, rbPlayTag)
	t.quiiPlaySent = true
	// El ack del timbre trae este id como remote-id, asi que se guarda para
	// reconocerlo: su status no tiene constante propia.
	t.quiiPlayEndLocalID = t.laneLocalID
	t.quiiPlayAcked = false
	return nil
}

// sendQuii manda un paquete QUII ya construido por el canal de datos.
func (t *rbTunnel) sendQuii(packet []byte) error {
	t.mu.Lock()
	defer t.mu.Unlock()
	if t.quiiDestID == 0 {
		return fmt.Errorf("sin destino KCP todavia")
	}
	t.sendWrappedData(packet, t.quiiNextSeq, rbDataTag)
	t.quiiNextSeq++
	return nil
}

func (t *rbTunnel) incoming() <-chan []byte { return t.quiiIncoming }

func allFill(b []byte) bool {
	for _, v := range b {
		if v != 0xBB {
			return false
		}
	}
	return true
}

// sendQuiiKeepalive mantiene viva la sesion QUII dentro del tunel. El cliente
// oficial lo manda cada 10 s desde que se envia el play, y sin el el timbre no
// llega a emitir: se queda esperando antes de STREAM_READY.
func (t *rbTunnel) sendQuiiKeepalive() error {
	t.mu.Lock()
	defer t.mu.Unlock()
	if t.quiiDestID == 0 {
		return fmt.Errorf("sin destino KCP todavia")
	}
	packet, err := encryptPacketAt(t.key, 0x00, nil, 0, 0, 0, 0, 0, uint64(time.Now().Unix()))
	if err != nil {
		return err
	}
	t.sendWrappedData(packet, t.quiiNextSeq, rbDataTag)
	t.quiiNextSeq++
	return nil
}
