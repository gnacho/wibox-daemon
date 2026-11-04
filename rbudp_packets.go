package main

import (
	"encoding/hex"
	"fmt"
)

// mustHex convierte un hex literal de constantes del protocolo.
func mustHex(s string) []byte {
	b, err := hex.DecodeString(s)
	if err != nil {
		panic("hex invalido en constante del protocolo: " + err.Error())
	}
	return b
}

// Constantes del protocolo RB-UDP del fabricante.
const (
	rbMarker           uint32 = 0xFFABEFC1
	rbSrcIDBase        uint32 = 0x04000006
	rbCmdConnect       uint32 = 0x00120101
	rbCmdDisconnect    uint32 = 0x00120102
	rbCmdData          uint32 = 0x00120103
	rbWrappedStatus    uint32 = 0x00001900
	rbStreamDataStatus uint32 = 0x00000500
)

// Palabras de estado del canal de control.
const (
	ctrlBootstrap          uint32 = 0xFFFF4100
	ctrlEstablished        uint32 = 0xFFFF0900
	ctrlSynAck             uint32 = 0xFFFF4900
	ctrlFlow               uint32 = 0xFFFF0D00
	ctrlHeartbeatRequest   uint32 = 0xFFFF0500
	ctrlProgress           uint32 = 0xFFB40900
	ctrlPostConnect        uint32 = 0xFFA80900
	ctrlPostConnectAck     uint32 = 0xFF700900
	ctrlPostSetup          uint32 = 0xFED80900
	ctrlPostSetupAck       uint32 = 0xFEB00900
	ctrlPostPlay           uint32 = 0xFEB80900
	ctrlPostPlayAck        uint32 = 0xFE900900
	ctrlStreamReady        uint32 = 0xFF480900
	ctrlStreamReadyAck     uint32 = 0xFF100900
	ctrlPlaySync           uint32 = 0xFF780900
	ctrlPlayLate           uint32 = 0xFF880900
	ctrlPlaySyncAck        uint32 = 0xFA740900
	ctrlPlayEstablishedAck uint32 = 0xFB9E0900
)

// Tags de 8 bytes que viajan en el hueco 0x24 del paquete envuelto.
var (
	rbConnectTag = []byte("ion::Ope")
	rbDataTag    = []byte("ad()  st")
)

// nativeChecksum16 replica la suma del stack nativo: u16 little endian sobre
// todo el paquete, con el propio campo de checksum a cero, plegado y negado.
func nativeChecksum16(packet []byte) uint16 {
	buf := packet
	if len(buf) >= 0x1A {
		buf = make([]byte, len(packet))
		copy(buf, packet)
		buf[0x18] = 0
		buf[0x19] = 0
	}
	var sum uint32
	i := 0
	for ; i+1 < len(buf); i += 2 {
		sum += uint32(buf[i]) | uint32(buf[i+1])<<8
	}
	if i < len(buf) {
		sum += uint32(buf[i])
	}
	folded := (sum >> 16) + (sum & 0xFFFF)
	folded += folded >> 16
	return ^uint16(folded)
}

// rbControl es un paquete de control de 28 bytes.
type rbControl struct {
	Word4      uint32
	Word8      uint32
	LocalID    uint32
	RemoteID   uint32
	StatusWord uint32
}

func buildRbControl(word4, word8, localID, remoteID, statusWord uint32) []byte {
	p := make([]byte, 28)
	putU32(p, 0x00, rbMarker)
	putU32(p, 0x04, word4)
	putU32(p, 0x08, word8)
	putU32(p, 0x0C, localID)
	putU32(p, 0x10, remoteID)
	putU32(p, 0x14, statusWord)
	putU16(p, 0x1A, 28)
	putU16(p, 0x18, nativeChecksum16(p))
	return p
}

func parseRbControl(packet []byte) (*rbControl, error) {
	if len(packet) < 28 {
		return nil, fmt.Errorf("control RB corto: %d bytes", len(packet))
	}
	return &rbControl{
		Word4:      readU32(packet, 0x04),
		Word8:      readU32(packet, 0x08),
		LocalID:    readU32(packet, 0x0C),
		RemoteID:   readU32(packet, 0x10),
		StatusWord: readU32(packet, 0x14),
	}, nil
}

// buildRbWrapped mete un paquete KCP dentro de un envoltorio RB. El checksum
// solo cubre los primeros 0x1C bytes.
func buildRbWrapped(inner []byte, word4, word8, localID, remoteID, statusWord uint32, tag8 []byte) []byte {
	total := 0x24 + 8 + (len(inner) - 0x10)
	p := make([]byte, total)
	putU32(p, 0x00, rbMarker)
	putU32(p, 0x04, word4)
	putU32(p, 0x08, word8)
	putU32(p, 0x0C, localID)
	putU32(p, 0x10, remoteID)
	putU32(p, 0x14, statusWord)
	putU16(p, 0x1A, uint16(total))
	putU32(p, 0x1C, 0xFFFFFFFF)
	putU32(p, 0x20, uint32(len(inner)))
	copy(p[0x24:0x2C], tag8)
	copy(p[0x2C:], inner[0x10:])
	putU16(p, 0x18, nativeChecksum16(p[:0x1C]))
	return p
}

// parseRbWrapped saca el paquete interior. Ojo: un envoltorio construido por
// nosotros cae por la rama directa y no lleva tag.
func parseRbWrapped(packet []byte) (inner []byte, tag8 []byte, err error) {
	if len(packet) < 0x20 {
		return nil, nil, fmt.Errorf("envoltorio RB corto: %d bytes", len(packet))
	}
	innerTotal := int(readU32(packet, 0x20))
	if readU32(packet, 0x1C) == 0xFFFFFFFF && len(packet) >= 0x1C+innerTotal {
		return packet[0x1C : 0x1C+innerTotal], nil, nil
	}
	payload := packet[0x24:]
	if len(payload) != 8+(innerTotal-0x10) {
		return nil, nil, fmt.Errorf("envoltorio RB con tamano inconsistente: %d", len(payload))
	}
	inner = make([]byte, innerTotal)
	putU32(inner, 0x00, 0xFFFFFFFF)
	putU32(inner, 0x04, uint32(innerTotal))
	copy(inner[0x10:], payload[8:])
	return inner, payload[:8], nil
}

// buildKcpConnect pide el canal KCP contra el timbre.
func buildKcpConnect(srcID uint32, channel, connType int) []byte {
	p := make([]byte, 0x4C)
	putU32(p, 0x00, 0xFFFFFFFF)
	putU32(p, 0x04, 0x4C)
	putU16(p, 0x10, 0x0100)
	p[0x12] = 0
	putU32(p, 0x14, rbCmdConnect)
	putU32(p, 0x24, 0x24)
	putU32(p, 0x28, srcID)
	p[0x40] = byte((channel&0x0F)<<4 | (connType&1)<<3)
	return p
}

type kcpConnectResponse struct {
	ResultCode uint32
	ConnectID  uint32
	DestID     uint32
	TypeFlag   byte
	Command    uint32
}

func parseKcpConnectResponse(packet []byte) (*kcpConnectResponse, error) {
	if len(packet) < 0x1C {
		return nil, fmt.Errorf("respuesta de connect corta: %d bytes", len(packet))
	}
	packetLength := readU32(packet, 0x04)
	out := &kcpConnectResponse{
		TypeFlag:   packet[0x12],
		Command:    readU32(packet, 0x14),
		ResultCode: readU32(packet, 0x10),
	}
	if packetLength >= 0x4C && uint32(len(packet)) >= packetLength {
		out.ConnectID = readU32(packet, 0x28)
		out.DestID = readU32(packet, int(packetLength)-4)
		return out, nil
	}
	out.ConnectID = readU32(packet, 0x18)
	out.DestID = readU32(packet, 0x38)
	return out, nil
}

func buildKcpDisconnect(srcID, destID uint32) []byte {
	p := make([]byte, 0x30)
	putU32(p, 0x00, 0xFFFFFFFF)
	putU32(p, 0x04, 0x30)
	putU16(p, 0x10, 0x0100)
	p[0x12] = 0
	putU32(p, 0x14, rbCmdDisconnect)
	putU32(p, 0x24, 0x08)
	putU32(p, 0x28, destID)
	putU32(p, 0x2C, srcID)
	return p
}

func buildRbData(payload []byte, srcID, destID, seq uint32) []byte {
	total := 0x38 + len(payload)
	p := make([]byte, total)
	putU32(p, 0x00, 0xFFFFFFFF)
	putU32(p, 0x04, uint32(total))
	putU16(p, 0x10, 0x0100)
	p[0x12] = 0
	putU32(p, 0x14, rbCmdData)
	putU32(p, 0x18, seq)
	putU32(p, 0x24, uint32(total-0x28))
	putU32(p, 0x28, destID)
	putU32(p, 0x2C, srcID)
	putU32(p, 0x30, uint32(total-0x28-0x10))
	copy(p[0x38:], payload)
	return p
}

func buildRbDataAck(payloadLength, seq, srcID, destID uint32) []byte {
	p := make([]byte, 0x38)
	putU32(p, 0x00, 0xFFFFFFFF)
	putU32(p, 0x04, 0x38)
	putU16(p, 0x10, 0x0100)
	p[0x12] = 0x01
	putU32(p, 0x14, rbCmdData)
	putU32(p, 0x18, seq)
	putU32(p, 0x24, 0x10)
	putU32(p, 0x28, destID)
	putU32(p, 0x2C, srcID)
	putU32(p, 0x30, payloadLength)
	return p
}

type rbDataPacket struct {
	PacketLength  uint32
	TypeFlag      byte
	Command       uint32
	Seq           uint32
	PayloadLength uint32
	DestID        uint32
	SrcID         uint32
	Payload       []byte
}

func parseRbData(packet []byte) (*rbDataPacket, error) {
	if len(packet) < 0x38 {
		return nil, fmt.Errorf("frame RB data corto: %d bytes", len(packet))
	}
	out := &rbDataPacket{
		PacketLength:  readU32(packet, 0x04),
		TypeFlag:      packet[0x12],
		Command:       readU32(packet, 0x14),
		Seq:           readU32(packet, 0x18),
		PayloadLength: readU32(packet, 0x24),
		DestID:        readU32(packet, 0x28),
		SrcID:         readU32(packet, 0x2C),
	}
	end := int(out.PacketLength)
	if end > len(packet) || end < 0x38 {
		end = len(packet)
	}
	out.Payload = packet[0x38:end]
	return out, nil
}

func isRbDataAck(packet []byte) bool {
	return len(packet) > 0x12 && packet[0x12] == 0x01
}
