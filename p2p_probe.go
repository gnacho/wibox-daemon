package main

import (
	"encoding/binary"
	"fmt"
	"math/rand"
	"net"
	"strings"
	"time"
)

// Constantes del sondeo P2P (port de UdpP2PProbe.kt / quii-helper
// protocols/p2p/test_packets.py).
const (
	p2pMarker  uint32 = 0xFFABEFC1
	p2pCmdTest uint32 = 0x00120002

	p2pProbeSeqLAN   = 0
	p2pProbeSeqP2P   = 1
	p2pProbeSeqTrans = 2

	p2pFrameSize = 0xA4
)

func putU32(p []byte, off int, v uint32) { binary.LittleEndian.PutUint32(p[off:], v) }
func putU16(p []byte, off int, v uint16) { binary.LittleEndian.PutUint16(p[off:], v) }
func readU32(p []byte, off int) uint32   { return binary.LittleEndian.Uint32(p[off:]) }
func readU16(p []byte, off int) uint16   { return binary.LittleEndian.Uint16(p[off:]) }

// putCString escribe una cadena UTF-8 truncada a max-1 bytes y con terminador.
func putCString(p []byte, off, max int, s string) {
	b := []byte(s)
	if len(b) > max-1 {
		b = b[:max-1]
	}
	copy(p[off:off+max], b)
}

func readCString(p []byte, off, max int) string {
	end := off
	for end < off+max && end < len(p) && p[end] != 0 {
		end++
	}
	return string(p[off:end])
}

// p2pTestResponse es la respuesta de un peer al sondeo. Hay dos formatos segun
// si el peer contesta con el layout de transporte (flag en 0x2E) o plano.
type p2pTestResponse struct {
	ResultCode  int
	SessionFlag string
	Address     string
	Port        int
	StatusCode  int
	TestID      uint32
}

func (r p2pTestResponse) ok() bool { return r.ResultCode == 0 && r.StatusCode == 0 }

// p2pTransportFrame es un frame de transporte P2P: mismo layout 0xA4 que el
// sondeo, con el flag de tipo en 0x2E (0 = request del timbre, 1 = respuesta).
type p2pTransportFrame struct {
	PacketTypeFlag int
	Seq            uint32
	SessionFlag    string
	RemoteIP       string
	RemotePort     int
	TailCode       uint32
}

func (f p2pTransportFrame) isRequest() bool { return f.PacketTypeFlag == 0 }

// buildP2pTestPacket arma el frame de sondeo de 164 bytes. Los offsets estan
// verificados contra la captura del app oficial.
func buildP2pTestPacket(probeSeq int, sessionFlag string, responseSessionID int64, localUDPPort int, targetIP string, targetUDPPort, testMode int) []byte {
	p := make([]byte, p2pFrameSize)
	putU32(p, 0x00, p2pMarker)
	putU32(p, 0x1C, 0xFFFFFFFF)
	putU32(p, 0x20, 0x88)
	putU16(p, 0x1A, p2pFrameSize)
	putU16(p, 0x2A, uint16(rand.Intn(0x10000)))
	putU16(p, 0x2C, 0x100)
	putU16(p, 0x2E, 0)
	putU32(p, 0x30, p2pCmdTest)
	putU32(p, 0x38, uint32(probeSeq))
	putU32(p, 0x40, 0x60)
	putCString(p, 0x44, 0x40, sessionFlag)
	putU16(p, 0x84, 0)
	putU16(p, 0x86, uint16(localUDPPort))
	putU16(p, 0x88, 0)
	putU16(p, 0x8A, uint16(localUDPPort))
	putCString(p, 0x8C, 0x10, targetIP)
	putU16(p, 0x9C, uint16(targetUDPPort))
	putU32(p, 0xA0, uint32(testMode))
	return p
}

// buildP2pTransportPacket arma un frame de transporte (ack o active) con el
// mismo layout que el sondeo y el flag de tipo en 0x2E.
func buildP2pTransportPacket(sessionFlag string, seq uint32, localUDPPort int, remoteIP string, remotePort int, tailCode uint32, packetTypeFlag int) []byte {
	p := make([]byte, p2pFrameSize)
	putU32(p, 0x00, p2pMarker)
	putU16(p, 0x1A, p2pFrameSize)
	putU32(p, 0x1C, 0xFFFFFFFF)
	putU32(p, 0x20, 0x88)
	putU16(p, 0x2A, uint16(rand.Intn(0x10000)))
	putU16(p, 0x2C, 0x100)
	putU16(p, 0x2E, uint16(packetTypeFlag))
	putU32(p, 0x30, p2pCmdTest)
	putU32(p, 0x38, seq)
	putU32(p, 0x40, 0x60)
	putCString(p, 0x44, 0x40, sessionFlag)
	putU16(p, 0x84, 0)
	putU16(p, 0x86, uint16(localUDPPort))
	putU16(p, 0x88, 0)
	putU16(p, 0x8A, uint16(localUDPPort))
	putCString(p, 0x8C, 0x10, remoteIP)
	putU16(p, 0x9C, uint16(remotePort))
	putU32(p, 0xA0, tailCode)
	return p
}

func buildP2pActivePacket(sessionFlag string, seq uint32, localUDPPort int, tailCode uint32) []byte {
	return buildP2pTransportPacket(sessionFlag, seq, localUDPPort, "", 0, tailCode, 0)
}

func buildP2pTransportAck(f p2pTransportFrame, localUDPPort int) []byte {
	return buildP2pTransportPacket(f.SessionFlag, f.Seq, localUDPPort, f.RemoteIP, f.RemotePort, f.TailCode, 1)
}

func parseP2pTestResponse(packet []byte) (p2pTestResponse, bool) {
	if len(packet) >= p2pFrameSize && readU32(packet, 0x30) == p2pCmdTest {
		flag := readU16(packet, 0x2E)
		rc := 1
		status := int(flag)
		if flag == 1 {
			rc = 0
			status = 0
		}
		return p2pTestResponse{
			ResultCode:  rc,
			SessionFlag: readCString(packet, 0x44, 0x40),
			Address:     readCString(packet, 0x8C, 0x10),
			Port:        int(readU16(packet, 0x9C)),
			StatusCode:  status,
			TestID:      readU32(packet, 0xA0),
		}, true
	}
	if len(packet) >= 0x88 {
		return p2pTestResponse{
			ResultCode:  int(readU32(packet, 0x20)),
			SessionFlag: readCString(packet, 0x28, 0x40),
			Address:     readCString(packet, 0x70, 0x10),
			Port:        int(readU16(packet, 0x80)),
			StatusCode:  int(readU16(packet, 0x82)),
			TestID:      readU32(packet, 0x84),
		}, true
	}
	return p2pTestResponse{}, false
}

func parseP2pTransportFrame(packet []byte) (p2pTransportFrame, bool) {
	if len(packet) < p2pFrameSize || readU32(packet, 0x30) != p2pCmdTest {
		return p2pTransportFrame{}, false
	}
	return p2pTransportFrame{
		PacketTypeFlag: int(readU16(packet, 0x2E)),
		Seq:            readU32(packet, 0x38),
		SessionFlag:    readCString(packet, 0x44, 0x40),
		RemoteIP:       readCString(packet, 0x8C, 0x10),
		RemotePort:     int(readU16(packet, 0x9C)),
		TailCode:       readU32(packet, 0xA0),
	}, true
}

func probeSeqFor(kind string) int {
	switch kind {
	case "lan":
		return p2pProbeSeqLAN
	case "p2p":
		return p2pProbeSeqP2P
	default:
		return p2pProbeSeqTrans
	}
}

// isPrivateIP replica InetAddress.isSiteLocalAddress de Java: 10/8, 172.16/12,
// 192.168/16. Importa para el ranking de peers.
func isPrivateIP(ip string) bool {
	parsed := net.ParseIP(ip)
	if parsed == nil {
		return false
	}
	v4 := parsed.To4()
	if v4 == nil {
		return false
	}
	switch {
	case v4[0] == 10:
		return true
	case v4[0] == 172 && v4[1] >= 16 && v4[1] <= 31:
		return true
	case v4[0] == 192 && v4[1] == 168:
		return true
	}
	return false
}

func rankOf(ip string) int {
	switch {
	case isPrivateIP(ip):
		return 3
	case strings.HasPrefix(ip, "127."):
		return 2
	default:
		return 1
	}
}

// flagMatches compara el session-flag tolerando el truncado a 63 chars que
// hace el servidor.
func flagMatches(expected, received string) bool {
	if expected == "" || received == "" {
		return false
	}
	return expected == received || strings.HasPrefix(expected, received) || strings.HasPrefix(received, expected)
}

type probeResult struct {
	peer  *net.UDPAddr
	rank  int
	acked bool
}

// runProbeWithHandshake sondea los targets y, si el timbre nos sondea primero
// (frame de request), lo ACKea y le manda los paquetes active con las colas
// 200 y 102. Sin ese ACK no hay agujero abierto en el NAT.
func runProbeWithHandshake(conn *net.UDPConn, info *p2pConnectInfo, sessionFlag string, responseSessionID int64, localUDPPort int, timeout time.Duration, attemptsPerTarget int) (*probeResult, error) {
	targets := iterTargets(info, 0)
	if len(targets) == 0 {
		return nil, fmt.Errorf("sin targets de probe")
	}
	buf := make([]byte, 4096)

	for _, target := range targets {
		addr, err := net.ResolveUDPAddr("udp", fmt.Sprintf("%s:%d", target.IP, target.Port))
		if err != nil {
			continue
		}
		packet := buildP2pTestPacket(probeSeqFor(target.Kind), sessionFlag, responseSessionID, localUDPPort, target.IP, target.Port, target.TestMode)
		acked := false

		for attempt := 0; attempt < attemptsPerTarget && !acked; attempt++ {
			if _, err := conn.WriteToUDP(packet, addr); err != nil {
				continue
			}
			deadline := time.Now().Add(timeout)
			for time.Now().Before(deadline) {
				conn.SetReadDeadline(deadline)
				n, sender, err := conn.ReadFromUDP(buf)
				if err != nil {
					break
				}
				data := buf[:n]
				frame, ok := parseP2pTransportFrame(data)
				if !ok || !flagMatches(sessionFlag, frame.SessionFlag) {
					continue
				}
				if frame.PacketTypeFlag == 1 {
					parsed, ok := parseP2pTestResponse(data)
					if !ok || !parsed.ok() || parsed.Address == "" || parsed.Port <= 0 {
						continue
					}
					peerAddr, err := net.ResolveUDPAddr("udp", fmt.Sprintf("%s:%d", parsed.Address, parsed.Port))
					if err != nil {
						continue
					}
					return &probeResult{peer: peerAddr, rank: rankOf(parsed.Address), acked: acked}, nil
				}
				// Request del timbre: ACK y active completan el punch.
				ack := buildP2pTransportAck(frame, localUDPPort)
				conn.WriteToUDP(ack, sender)
				for _, tail := range []uint32{200, 102} {
					act := buildP2pActivePacket(sessionFlag, uint32(rand.Int31()), localUDPPort, tail)
					conn.WriteToUDP(act, sender)
				}
				acked = true
				return &probeResult{peer: sender, rank: 2, acked: true}, nil
			}
		}
	}
	return nil, fmt.Errorf("p2p probe: ningun peer contesto")
}

// runActiveHandshake espera a que el timbre nos sondee (peer publico: el device
// inicia el punch), ACKea sus requests y manda los active. Devuelve la direccion
// de transporte autoritativa, o el hint si no llega nada.
func runActiveHandshake(conn *net.UDPConn, sessionFlag string, timeout time.Duration, peerHint *net.UDPAddr) *net.UDPAddr {
	localUDPPort := conn.LocalAddr().(*net.UDPAddr).Port
	deadline := time.Now().Add(timeout)
	var peer *net.UDPAddr
	activeSent := false
	seenRequests := 0
	buf := make([]byte, 4096)

	sendActive := func(addr *net.UDPAddr) {
		for _, tail := range []uint32{200, 102} {
			pkt := buildP2pActivePacket(sessionFlag, uint32(rand.Int31()), localUDPPort, tail)
			conn.WriteToUDP(pkt, addr)
		}
		activeSent = true
	}

	for time.Now().Before(deadline) {
		conn.SetReadDeadline(time.Now().Add(500 * time.Millisecond))
		n, sender, err := conn.ReadFromUDP(buf)
		if err != nil {
			if !activeSent && peer != nil {
				sendActive(peer)
			}
			continue
		}
		frame, ok := parseP2pTransportFrame(buf[:n])
		if !ok || !flagMatches(sessionFlag, frame.SessionFlag) {
			continue
		}
		if !frame.isRequest() || frame.RemoteIP == "" || frame.RemotePort <= 0 {
			continue
		}
		seenRequests++
		addr, rerr := net.ResolveUDPAddr("udp", fmt.Sprintf("%s:%d", frame.RemoteIP, frame.RemotePort))
		if rerr != nil {
			addr = sender
		}
		peer = addr
		ack := buildP2pTransportAck(frame, localUDPPort)
		conn.WriteToUDP(ack, peer)
		if !activeSent {
			sendActive(peer)
		}
		if seenRequests >= 2 {
			break
		}
	}
	if peer == nil {
		peer = peerHint
		if !activeSent && peerHint != nil {
			sendActive(peerHint)
		}
	}
	return peer
}

// natcheck pregunta al servidor de test UDP cual es nuestro mapping externo y
// devuelve la direccion publica. Sin esto el timbre no sabe donde responder.

// natcheckQuerySize es el tamano de la consulta de mapeo externo que manda el
// app oficial. Es mas corta que el frame de sonda y no lleva session-flag.
const natcheckQuerySize = 0x70

// buildNatcheckQuery arma la consulta de mapeo externo replicando byte a byte
// la que manda el cliente oficial (capturada el 4-Nov-2026 en la red local).
func buildNatcheckQuery() []byte {
	pkt := make([]byte, natcheckQuerySize)
	putU32(pkt, 0x00, p2pMarker)
	putU16(pkt, 0x1A, natcheckQuerySize)
	putU32(pkt, 0x1C, 0xFFFFFFFF)
	putU32(pkt, 0x20, 0x54)
	// 0x2D es el flag de "dame mi mapeo": con 0 el servidor solo hace eco y no
	// rellena la IP publica ni el puerto.
	pkt[0x2D] = 0x01
	putU16(pkt, 0x30, 0x0001)
	pkt[0x32] = 0x11
	putU16(pkt, 0x40, 0x2C)
	copy(pkt[0x44:], []byte{0x23, 0x8B, 0x2A, 0xEE})
	return pkt
}

// parseNatcheckResponse saca nuestro mapeo externo de la respuesta del
// natcheck: IP publica como cadena en 0x4C y puerto en u16 LE en 0x5C. El
// puerto del propio servidor y su IP vienen detras y no sirven para nada.
func parseNatcheckResponse(packet []byte) (string, int, bool) {
	if len(packet) < natcheckQuerySize {
		return "", 0, false
	}
	if readU32(packet, 0x00) != p2pMarker {
		return "", 0, false
	}
	ip := readCString(packet, 0x4C, 0x10)
	port := int(readU16(packet, 0x5C))
	if ip == "" || port <= 0 || port > 65535 {
		return "", 0, false
	}
	return ip, port, true
}

// natcheckPublic pide al servidor natcheck nuestro mapeo externo.
func natcheckPublic(conn *net.UDPConn, natURL string) *net.UDPAddr {
	if natURL == "" {
		return nil
	}
	addr, err := net.ResolveUDPAddr("udp", strings.TrimPrefix(natURL, "udp://"))
	if err != nil {
		return nil
	}
	pkt := buildNatcheckQuery()
	buf := make([]byte, 4096)
	for attempt := 0; attempt < 4; attempt++ {
		if _, err := conn.WriteToUDP(pkt, addr); err != nil {
			continue
		}
		conn.SetReadDeadline(time.Now().Add(1200 * time.Millisecond))
		n, _, err := conn.ReadFromUDP(buf)
		if err != nil {
			continue
		}
		ip, port, ok := parseNatcheckResponse(buf[:n])
		if !ok {
			continue
		}
		mapped, rerr := net.ResolveUDPAddr("udp", fmt.Sprintf("%s:%d", ip, port))
		if rerr == nil {
			return mapped
		}
	}
	return nil
}
