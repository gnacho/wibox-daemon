package main

import (
	"crypto/aes"
	"crypto/cipher"
	"crypto/sha256"
	"encoding/binary"
	"errors"
	"fmt"
	"io"
	"net"
	"time"
)

// IV fija de '0' replicada del cliente oficial (AES/CBC/NoPadding).
var quiiIV = []byte("0000000000000000")

func zeroPad(b []byte) []byte {
	if rem := len(b) % 16; rem != 0 {
		return append(b, make([]byte, 16-rem)...)
	}
	return b
}

func aesCBC(key, plain []byte, encrypt bool) ([]byte, error) {
	if len(plain) == 0 || len(plain)%16 != 0 {
		return nil, fmt.Errorf("bloque de %d bytes, no es multiplo de 16", len(plain))
	}
	block, err := aes.NewCipher(key)
	if err != nil {
		return nil, err
	}
	out := make([]byte, len(plain))
	if encrypt {
		cipher.NewCBCEncrypter(block, quiiIV).CryptBlocks(out, plain)
	} else {
		cipher.NewCBCDecrypter(block, quiiIV).CryptBlocks(out, plain)
	}
	return out, nil
}

func quiiHeader(packetType byte, ts uint64, cmdPayloadSize, rawSize uint16, b13, b14, f15, f16, f17 byte) []byte {
	h := make([]byte, 32)
	h[0] = packetType
	binary.LittleEndian.PutUint64(h[1:], ts)
	binary.LittleEndian.PutUint16(h[9:], cmdPayloadSize)
	binary.LittleEndian.PutUint16(h[11:], rawSize)
	h[13] = b13
	h[14] = b14
	h[15] = f15
	h[16] = f16
	h[17] = f17
	return h
}

// encryptPacket: cabecera cifrada entera y cuerpo cifrado con el SHA-256 de
// (cabecera clara + cuerpo) pegado detras.
func encryptPacket(key []byte, packetType byte, body []byte, b13, b14, f15, f16, f17 byte) ([]byte, error) {
	return encryptPacketAt(key, packetType, body, b13, b14, f15, f16, f17, uint64(time.Now().Unix()))
}

// encryptPacketAt es encryptPacket con marca de tiempo fija, para poder
// comparar paquetes byte a byte en los tests.
func encryptPacketAt(key []byte, packetType byte, body []byte, b13, b14, f15, f16, f17 byte, ts uint64) ([]byte, error) {
	extLen := len(zeroPad(append(append([]byte{}, body...), make([]byte, 32)...)))
	clear := quiiHeader(packetType, ts, uint16(extLen), uint16(len(body)), b13, b14, f15, f16, f17)

	digest := sha256.Sum256(append(append([]byte{}, clear...), body...))
	plainBody := append(append([]byte{}, body...), digest[:]...)

	encHeader, err := aesCBC(key, clear, true)
	if err != nil {
		return nil, err
	}
	encBody, err := aesCBC(key, zeroPad(plainBody), true)
	if err != nil {
		return nil, err
	}
	return append(encHeader, encBody...), nil
}

// encodeDevicePassword no transforma las cadenas de 64 caracteres, que es el
// caso del auth-code del token.
func encodeDevicePassword(v string) string {
	if len(v) < 64 {
		sum := sha256.Sum256([]byte(v))
		return fmt.Sprintf("%x", sum)
	}
	return v
}

type QuiiClient struct {
	conn     net.Conn
	key      []byte
	password string
	channel  int
	stream   int
}

func DialQuii(addr string, key []byte, password string, channel, stream int, timeout time.Duration) (*QuiiClient, error) {
	conn, err := net.DialTimeout("tcp", addr, timeout)
	if err != nil {
		return nil, err
	}
	if tcp, ok := conn.(*net.TCPConn); ok {
		tcp.SetNoDelay(true)
	}
	c := &QuiiClient{conn: conn, key: key, password: password, channel: channel, stream: stream}
	if err := c.setup(); err != nil {
		conn.Close()
		return nil, err
	}
	return c, nil
}

// Close cuelga la sesion de forma limpia antes de cerrar el socket: si se
// corta en seco el timbre se queda ocupado y responde BUSY a la conexion
// siguiente.
func (c *QuiiClient) Close() error {
	_, _ = c.sendTransparent([]byte{0}, 1, 2*time.Second)
	return c.conn.Close()
}

// CloseNow suelta el socket sin esperar respuesta.
func (c *QuiiClient) CloseNow() error {
	return c.conn.Close()
}

// buildQuiiSetup devuelve el setup de 32 bytes que abre el canal QUII
// (0xA9 delante, resto ceros).
func buildQuiiSetup() []byte {
	setup := make([]byte, 32)
	setup[0] = 0xA9
	return setup
}

// quiiHandshake manda el setup en claro de 32 bytes y valida la respuesta.
func quiiHandshake(conn net.Conn, timeout time.Duration) error {
	conn.SetDeadline(time.Now().Add(timeout))
	setup := buildQuiiSetup()
	if _, err := conn.Write(setup); err != nil {
		return err
	}
	reply := make([]byte, 32)
	if _, err := io.ReadFull(conn, reply); err != nil {
		return fmt.Errorf("setup sin respuesta: %w", err)
	}
	if reply[0] != 0xA9 {
		return fmt.Errorf("setup reply inesperada type=0x%02x", reply[0])
	}
	if reply[9] != 0 {
		return fmt.Errorf("setup status=%d", reply[9])
	}
	if reply[10] != 2 || reply[11] != 1 {
		return fmt.Errorf("setup crypto=%d sha=%d", reply[10], reply[11])
	}
	return nil
}

// ProbeQuii comprueba si en esa direccion hay un timbre QUII vivo. Solo hace el
// setup y cierra: no abre nada ni manda video.
func ProbeQuii(addr string, timeout time.Duration) error {
	conn, err := net.DialTimeout("tcp", addr, timeout)
	if err != nil {
		return err
	}
	defer conn.Close()
	return quiiHandshake(conn, timeout)
}

func (c *QuiiClient) setup() error {
	return quiiHandshake(c.conn, 8*time.Second)
}

func (c *QuiiClient) Play() error {
	target := fmt.Sprintf("/mode=real&idc=%d&ids=%d&ap=2", c.channel, c.stream)
	body := buildPlayBody(c.username(), c.password, target)
	pkt, err := encryptPacket(c.key, 0x01, body, byte(c.channel), byte(c.channel>>8), 1, byte(c.stream-1), 0)
	if err != nil {
		return err
	}
	_, err = c.conn.Write(pkt)
	return err
}

func (c *QuiiClient) username() string { return "adminapp2" }

func buildPlayBody(user, password, target string) []byte {
	out := []byte{}
	out = append(out, []byte(user)...)
	out = append(out, []byte("&&")...)
	out = append(out, []byte(password)...)
	out = append(out, 0)
	out = append(out, []byte(target)...)
	out = append(out, 0)
	return out
}

type packet struct {
	Type    byte
	B13     byte
	B14     byte
	RawSize int
	Command []byte
	Media   []byte
}

// readPacket lee un envelope completo. El primer paquete tras el setup es la
// respuesta en claro de 32 bytes, que ya consume setup().
func (c *QuiiClient) readPacket(deadline time.Time) (*packet, error) {
	c.conn.SetReadDeadline(deadline)
	encHeader := make([]byte, 32)
	if _, err := io.ReadFull(c.conn, encHeader); err != nil {
		return nil, err
	}
	clear, err := aesCBC(c.key, encHeader, false)
	if err != nil {
		return nil, err
	}
	p := &packet{Type: clear[0], B13: clear[13], B14: clear[14]}
	// Una cabecera descifrada de un flujo desincronizado da bytes aleatorios y
	// a veces un tamano creible. Validar el tipo evita consumir basura como si
	// fuera un paquete.
	if !plausibleQuiiType(p.Type) {
		return nil, fmt.Errorf("tipo de paquete QUII desconocido: 0x%02x", p.Type)
	}
	cmdSize := int(binary.LittleEndian.Uint16(clear[9:]))
	rawSize := int(binary.LittleEndian.Uint16(clear[11:]))
	mediaSize := int(binary.LittleEndian.Uint32(clear[11:]))

	isMedia := p.Type >= 0xA0 && p.Type <= 0xA3
	size := cmdSize
	if isMedia {
		size = mediaSize
	}
	if size < 0 || size > 5*1024*1024 {
		return nil, fmt.Errorf("tamano de paquete invalido: %d", size)
	}
	body := make([]byte, size)
	if size > 0 {
		if _, err := io.ReadFull(c.conn, body); err != nil {
			return nil, err
		}
	}
	if isMedia {
		// Prefijo de comando cifrado y media en claro detras.
		prefix := cmdSize
		if prefix > len(body) {
			prefix = len(body)
		}
		if prefix > 0 && prefix%16 == 0 {
			if dec, derr := aesCBC(c.key, body[:prefix], false); derr == nil {
				p.Command = dec[:min(rawSize, len(dec))]
			}
		}
		p.Media = body[prefix:]
		return p, nil
	}
	dec, err := aesCBC(c.key, body, false)
	if err != nil {
		return nil, err
	}
	if rawSize > 0 && rawSize <= len(dec) {
		p.Command = dec[:rawSize]
	} else {
		p.Command = dec
	}
	p.RawSize = rawSize
	return p, nil
}

func min(a, b int) int {
	if a < b {
		return a
	}
	return b
}

var errShortPacket = errors.New("paquete QUII incompleto")

// decodeQuiiPacket parsea un paquete QUII que llega por un flujo de bytes, como
// el que entrega el tunel remoto, y dice cuantos bytes consumio. readPacket hace
// lo mismo pero leyendo de un socket.
// plausibleQuiiType son los tipos que el timbre manda de verdad: keepalive,
// play, setup, media y ordenes transparentes.
func plausibleQuiiType(t byte) bool {
	switch {
	case t == 0x00, t == 0x01, t == 0xA9, t == 0xFE:
		return true
	case t >= 0xA0 && t <= 0xA3:
		return true
	}
	return false
}

func decodeQuiiPacket(key, data []byte) (*packet, int, error) {
	if len(data) < 32 {
		return nil, 0, errShortPacket
	}
	clear, err := aesCBC(key, data[:32], false)
	if err != nil {
		return nil, 0, err
	}
	p := &packet{Type: clear[0], B13: clear[13], B14: clear[14]}
	// Una cabecera descifrada de un flujo desincronizado da bytes aleatorios y
	// a veces un tamano creible. Validar el tipo evita consumir basura como si
	// fuera un paquete.
	if !plausibleQuiiType(p.Type) {
		return nil, 0, fmt.Errorf("tipo de paquete QUII desconocido: 0x%02x", p.Type)
	}
	cmdSize := int(binary.LittleEndian.Uint16(clear[9:]))
	rawSize := int(binary.LittleEndian.Uint16(clear[11:]))
	mediaSize := int(binary.LittleEndian.Uint32(clear[11:]))

	isMedia := p.Type >= 0xA0 && p.Type <= 0xA3
	size := cmdSize
	if isMedia {
		size = mediaSize
	}
	if size < 0 || size > 5*1024*1024 {
		return nil, 0, fmt.Errorf("tamano de paquete invalido: %d", size)
	}
	if len(data) < 32+size {
		return nil, 0, errShortPacket
	}
	body := data[32 : 32+size]
	total := 32 + size

	if isMedia {
		// Del cuerpo solo va cifrado el prefijo que anuncia cmdSize (32 bytes
		// en el timbre real), que cubre la cabecera del cpacket y los primeros
		// bytes del bitstream. El resto del cpacket, H264 incluido, viaja en
		// claro. Descifrar el cuerpo entero convertia el video en ruido.
		prefix := cmdSize
		if prefix > size {
			prefix = size
		}
		aligned := prefix - prefix%16
		media := make([]byte, 0, size)
		if aligned > 0 {
			dec, derr := aesCBC(key, body[:aligned], false)
			if derr != nil {
				return nil, 0, derr
			}
			media = append(media, dec...)
		}
		media = append(media, body[prefix:]...)
		p.Media = media
		p.RawSize = size
		return p, total, nil
	}
	// Un cuerpo cifrado siempre es multiplo de 16: si no lo es, la cabecera
	// era basura de un flujo desincronizado, no un paquete.
	if size%16 != 0 {
		return nil, 0, fmt.Errorf("cuerpo de %d bytes no alineado a 16", size)
	}
	dec, err := aesCBC(key, body, false)
	if err != nil {
		return nil, 0, err
	}
	if rawSize > 0 && rawSize <= len(dec) {
		p.Command = dec[:rawSize]
	} else {
		p.Command = dec
	}
	p.RawSize = rawSize
	return p, total, nil
}

// sendTransparent envia una orden del canal transparente y espera su respuesta.
func (c *QuiiClient) sendTransparent(body []byte, command byte, timeout time.Duration) ([]byte, error) {
	pkt, err := encryptPacket(c.key, 0xFE, body, command, 0, 0, 0, 0)
	if err != nil {
		return nil, err
	}
	if _, err := c.conn.Write(pkt); err != nil {
		return nil, err
	}
	deadline := time.Now().Add(timeout)
	for {
		if time.Now().After(deadline) {
			return nil, fmt.Errorf("sin respuesta a la orden %d", command)
		}
		p, err := c.readPacket(deadline)
		if err != nil {
			return nil, err
		}
		if p.Type == 0xFE && (p.B13 == command || p.B14 == command) {
			return p.Command, nil
		}
	}
}

// unlockBody arma el cuerpo de la orden 4: numero de cerradura, 0, canal de
// apertura, marca de contrasena ya hasheada, y el SHA-256 hexadecimal del pin
// a partir del byte 16.
func unlockBody(pin string, lockNumber, unlockChannel int) []byte {
	password := []byte(encodeDevicePassword(pin))
	body := make([]byte, 16+len(password))
	body[0] = byte(lockNumber)
	body[1] = 0
	body[2] = byte(unlockChannel)
	body[3] = 1
	copy(body[16:], password)
	return body
}

// Unlock abre la puerta. Orden 4 del canal transparente, con el SHA-256 del
// pin en los primeros 16 bytes de cabecera del cuerpo.
func (c *QuiiClient) Unlock(pin string, lockNumber, unlockChannel int) (int, error) {
	body := unlockBody(pin, lockNumber, unlockChannel)
	resp, err := c.sendTransparent(body, 4, 8*time.Second)
	if err != nil {
		return -1, err
	}
	if len(resp) < 2 {
		return -1, fmt.Errorf("respuesta de apertura corta: % x", resp)
	}
	if resp[0] == 0 {
		return 0, nil
	}
	if resp[1] == 2 {
		return -10029, fmt.Errorf("contrasena de apertura rechazada")
	}
	return -1, fmt.Errorf("apertura rechazada: % x", resp)
}
