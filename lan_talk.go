package main

import (
	"crypto/sha256"
	"encoding/binary"
	"fmt"
	"io"
	"net"
	"sync"
	"time"
)

// Audio de vuelta (hablar al visitante). Solo existe por la via LAN: el
// cliente de referencia lo dice de forma explicita ("no cloud fallback"), y
// en el tunel remoto no hay canal de altavoz conocido. Asi que esto se usa
// cuando el daemon corre en la red del edificio.
//
// El canal de talk es una conexion QUII aparte, con el byte 9 del setup a 2
// en vez de 0, y su propio juego de paquetes: open 0x0B, peticiones 0x0C
// (enviamos) y 0x0D (pedimos el audio entrante) y audio 0xA2.

const (
	talkSamplesPerFrame = 320
	talkFrameSeconds    = 0.04
	talkCodecALaw       = 4
	talkCodecULaw       = 5
)

// talkChannel replica el cliente nativo: el canal 0 viaja como 65535.
func talkChannel(v int) uint16 {
	if v <= 0 || v > 65535 {
		return 65535
	}
	return uint16(v)
}

func buildTalkSetup() []byte {
	pkt := make([]byte, 32)
	pkt[0] = 0xA9
	pkt[9] = 2
	return pkt
}

func buildTalkOpen(channel uint16, password string, key []byte, ts uint64) ([]byte, error) {
	body := buildPlayBody("adminapp2", password, "")
	body = append(body, 0)
	return encryptPacketAt(key, 0x0B, body, byte(channel&0xFF), byte(channel>>8), 0, 0, 0, ts)
}

func buildTalkRequest(channel uint16, sending, enabled bool, codec int, key []byte, ts uint64) ([]byte, error) {
	header := make([]byte, 32)
	if sending {
		header[0] = 0x0C
	} else {
		header[0] = 0x0D
	}
	binary.LittleEndian.PutUint64(header[1:], ts)
	binary.LittleEndian.PutUint16(header[9:], 32)
	binary.LittleEndian.PutUint16(header[11:], channel)
	if enabled {
		header[13] = 1
	}
	if sending {
		header[14] = byte(codec - 4)
		binary.LittleEndian.PutUint16(header[15:], 8000)
	}

	digest := sha256.Sum256(header)
	encHeader, err := aesCBC(key, header, true)
	if err != nil {
		return nil, err
	}
	encDigest, err := aesCBC(key, digest[:], true)
	if err != nil {
		return nil, err
	}
	return append(encHeader, encDigest...), nil
}

// buildTalkAudio arma un paquete de audio. El cuerpo compacto es el que usa
// el cliente nativo; el completo (cpacket con marca de tiempo) se deja por si
// algun firmware solo acepta ese dialecto.
func buildTalkAudio(encoded []byte, codec int, fullPacket bool, key []byte, ts uint64) ([]byte, error) {
	if len(encoded) != talkSamplesPerFrame {
		return nil, fmt.Errorf("el audio de talk lleva %d muestras, llegaron %d", talkSamplesPerFrame, len(encoded))
	}

	var body []byte
	if fullPacket {
		now := time.Now()
		stamp := uint32(now.Year()-2000)<<26 | uint32(now.Month())<<22 |
			uint32(now.Day())<<17 | uint32(now.Hour())<<12 |
			uint32(now.Minute())<<6 | uint32(now.Second())
		body = make([]byte, 20, 20+len(encoded))
		copy(body[0:4], []byte{0x00, 0x00, 0x01, 0xE3})
		binary.LittleEndian.PutUint32(body[4:], uint32(len(encoded)))
		binary.LittleEndian.PutUint32(body[8:], stamp)
		binary.LittleEndian.PutUint16(body[12:], uint16(now.Nanosecond()/1_000_000))
		body[14] = byte(codec)
		body[15] = 1
		binary.LittleEndian.PutUint16(body[16:], 8000)
		binary.LittleEndian.PutUint16(body[18:], 0)
	} else {
		body = make([]byte, 8, 8+len(encoded))
		binary.LittleEndian.PutUint32(body[0:], 0xF0010000)
		if codec == talkCodecALaw {
			body[4] = 0x0E
		} else {
			body[4] = 0x0A
		}
		body[5] = 2
		binary.LittleEndian.PutUint16(body[6:], uint16(len(encoded)))
	}
	body = append(body, encoded...)

	header := make([]byte, 32)
	header[0] = 0xA2
	binary.LittleEndian.PutUint64(header[1:], ts)
	binary.LittleEndian.PutUint16(header[9:], 32)
	binary.LittleEndian.PutUint32(header[11:], uint32(len(body)))

	encHeader, err := aesCBC(key, header, true)
	if err != nil {
		return nil, err
	}
	// Los 32 primeros bytes del cuerpo van cifrados, el resto en claro.
	encBody, err := aesCBC(key, body[:32], true)
	if err != nil {
		return nil, err
	}
	out := append(encHeader, encBody...)
	return append(out, body[32:]...), nil
}

// alawEncode convierte una muestra PCM16 en un byte G.711 A-law.
// alawEncode comprime una muestra PCM16 a G.711 A-law con el algoritmo
// clasico (Sun g711.c): mascara 0xD5 para positivas y 0x55 para negativas,
// segmentos de 16 pasos y mantisa de 4 bits.
func alawEncode(sample int16) byte {
	segEnd := [8]int{0xFF, 0x1FF, 0x3FF, 0x7FF, 0xFFF, 0x1FFF, 0x3FFF, 0x7FFF}
	v := int(sample)
	var mask byte
	if v >= 0 {
		mask = 0xD5
	} else {
		mask = 0x55
		v = -v - 8
	}
	if v < 0 {
		v = 0
	}
	seg := 0
	for ; seg < 8; seg++ {
		if v <= segEnd[seg] {
			break
		}
	}
	if seg >= 8 {
		return 0x7F ^ mask
	}
	aval := seg << 4
	if seg < 2 {
		aval |= (v >> 4) & 0x0F
	} else {
		aval |= (v >> (seg + 3)) & 0x0F
	}
	return byte(aval) ^ mask
}

func encodeALaw(pcm []byte) []byte {
	out := make([]byte, len(pcm)/2)
	for i := range out {
		sample := int16(binary.LittleEndian.Uint16(pcm[i*2:]))
		out[i] = alawEncode(sample)
	}
	return out
}

// talkSession mantiene la conexion de talk abierta y manda bloques de 40 ms.
type talkSession struct {
	conn    net.Conn
	key     []byte
	channel uint16
	codec   int
	full    bool

	mu     sync.Mutex
	closed bool
}

func dialTalk(addr string, key []byte, password string, channel int, timeout time.Duration) (*talkSession, error) {
	if len(key) != 32 {
		return nil, fmt.Errorf("la clave de talk debe ser de 32 bytes, es de %d", len(key))
	}
	conn, err := net.DialTimeout("tcp", addr, timeout)
	if err != nil {
		return nil, err
	}
	if tcp, ok := conn.(*net.TCPConn); ok {
		tcp.SetNoDelay(true)
	}

	t := &talkSession{conn: conn, key: key, channel: talkChannel(channel), codec: talkCodecALaw}
	if err := t.handshake(password, timeout); err != nil {
		conn.Close()
		return nil, err
	}
	return t, nil
}

func (t *talkSession) handshake(password string, timeout time.Duration) error {
	deadline := time.Now().Add(timeout)
	t.conn.SetDeadline(deadline)

	if _, err := t.conn.Write(buildTalkSetup()); err != nil {
		return err
	}
	reply := make([]byte, 32)
	if _, err := io.ReadFull(t.conn, reply); err != nil {
		return fmt.Errorf("talk sin respuesta al setup: %w", err)
	}
	if reply[0] != 0xA9 || reply[9] != 0 {
		return fmt.Errorf("talk: setup rechazado (type=0x%02x status=%d)", reply[0], reply[9])
	}
	if reply[10] != 2 || reply[11] != 1 {
		return fmt.Errorf("talk: el timbre pide cifrado %d/%d, se necesita AES-256/SHA256", reply[10], reply[11])
	}

	open, err := buildTalkOpen(t.channel, password, t.key, uint64(time.Now().Unix()))
	if err != nil {
		return err
	}
	if _, err := t.conn.Write(open); err != nil {
		return err
	}

	// El cliente nativo espera a conocer el codec antes de pedir el canal.
	// Aqui el codec conocido del timbre es A-law, asi que se pide ese sin
	// bloquear el arranque; si llega media, el codec se corrige.
	t.codec = talkCodecALaw
	for _, sending := range []bool{true, false} {
		req, rerr := buildTalkRequest(t.channel, sending, true, t.codec, t.key, uint64(time.Now().Unix()))
		if rerr != nil {
			return rerr
		}
		if _, werr := t.conn.Write(req); werr != nil {
			return werr
		}
	}

	// Se espera el 0x0D nativo, pero sin bloquear el arranque para siempre:
	// el audio de ida puede empezar igualmente.
	t.conn.SetReadDeadline(time.Now().Add(3 * time.Second))
	header := make([]byte, 32)
	for {
		if _, rerr := io.ReadFull(t.conn, header); rerr != nil {
			break
		}
		clear, derr := aesCBC(t.key, header, false)
		if derr != nil {
			break
		}
		if clear[0] == 0x0D {
			break
		}
		size := int(binary.LittleEndian.Uint32(clear[11:]))
		if size > 0 && size < 64*1024 {
			rest := make([]byte, size)
			io.ReadFull(t.conn, rest)
		}
	}
	// El deadline del handshake afecta tambien a las escrituras: se limpia
	// entero o el primer audio vence en cuanto se acaba el margen.
	t.conn.SetDeadline(time.Time{})
	return nil
}

// SendPCM envia PCM16 LE de 8 kHz. Acepta cualquier longitud multiplo de dos
// muestras y lo trocea en tramas de 40 ms.
func (t *talkSession) SendPCM(pcm []byte) error {
	if len(pcm)%2 != 0 {
		return fmt.Errorf("el PCM debe ser de 16 bits, llegaron %d bytes", len(pcm))
	}
	samples := len(pcm) / 2
	for off := 0; off < samples; off += talkSamplesPerFrame {
		end := off + talkSamplesPerFrame
		if end > samples {
			end = samples
		}
		chunk := pcm[off*2 : end*2]
		if len(chunk) < talkSamplesPerFrame*2 {
			pad := make([]byte, talkSamplesPerFrame*2-len(chunk))
			chunk = append(chunk, pad...)
		}
		encoded := encodeALaw(chunk)
		pkt, err := buildTalkAudio(encoded, t.codec, t.full, t.key, uint64(time.Now().Unix()))
		if err != nil {
			return err
		}
		t.mu.Lock()
		if t.closed {
			t.mu.Unlock()
			return fmt.Errorf("la sesion de talk esta cerrada")
		}
		_, werr := t.conn.Write(pkt)
		t.mu.Unlock()
		if werr != nil {
			return werr
		}
		time.Sleep(time.Duration(talkFrameSeconds * float64(time.Second)))
	}
	return nil
}

func (t *talkSession) Close() error {
	t.mu.Lock()
	defer t.mu.Unlock()
	if t.closed {
		return nil
	}
	t.closed = true
	return t.conn.Close()
}
