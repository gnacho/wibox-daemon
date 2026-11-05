package main

import (
	"encoding/binary"
	"encoding/hex"
	"io"
	"net"
	"testing"
	"time"
)

// Valores de referencia generados con ffmpeg:
//
//	printf los PCM16 de abajo | ffmpeg -f s16le -ar 8000 -f alaw
//
// Entradas: -32768 -20000 -10000 -1000 -1 0 1 1000 10000 20000 32767
const alawReferenceHex = "2a26367a55d5d5fab6a6aa"

func TestEncodeALawMatchesReference(t *testing.T) {
	inputs := []int16{-32768, -20000, -10000, -1000, -1, 0, 1, 1000, 10000, 20000, 32767}
	want, err := hex.DecodeString(alawReferenceHex)
	if err != nil {
		t.Fatalf("referencia ilegible: %v", err)
	}
	if len(want) != len(inputs) {
		t.Fatalf("referencia de %d bytes para %d muestras", len(want), len(inputs))
	}
	for i, v := range inputs {
		if got := alawEncode(v); got != want[i] {
			t.Fatalf("muestra %d (%d): alaw 0x%02x, se esperaba 0x%02x", i, v, got, want[i])
		}
	}
}

func TestEncodeALawProducesOneBytePerSample(t *testing.T) {
	pcm := make([]byte, 640)
	for i := 0; i < 320; i++ {
		binary.LittleEndian.PutUint16(pcm[i*2:], uint16(int16(i*100-16000)))
	}
	enc := encodeALaw(pcm)
	if len(enc) != 320 {
		t.Fatalf("320 muestras deberian dar 320 bytes, salieron %d", len(enc))
	}
	if enc[0] == enc[319] {
		t.Fatalf("muestras muy distintas codificadas igual: 0x%02x", enc[0])
	}
}

// falseBell acepta una conexion de talk, valida la secuencia y responde el
// 0x0D nativo. Sirve para probar el handshake sin el timbre delante.
func falseBell(t *testing.T, key []byte, password string) (string, <-chan []byte, func()) {
	t.Helper()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	audio := make(chan []byte, 16)
	done := make(chan struct{})
	go func() {
		defer close(done)
		conn, aerr := ln.Accept()
		if aerr != nil {
			return
		}
		defer conn.Close()

		setup := make([]byte, 32)
		if _, rerr := io.ReadFull(conn, setup); rerr != nil {
			return
		}
		if setup[0] != 0xA9 || setup[9] != 2 {
			t.Errorf("setup inesperada: type=0x%02x flag=%d", setup[0], setup[9])
			return
		}
		reply := make([]byte, 32)
		reply[0] = 0xA9
		reply[7] = 2
		reply[10] = 2
		reply[11] = 1
		conn.Write(reply)

		open, rerr := readEncryptedPacket(conn, key)
		if rerr != nil {
			return
		}
		if open.Type != 0x0B {
			t.Errorf("open tipo 0x%02x, se esperaba 0x0B", open.Type)
			return
		}
		wantBody := "adminapp2&&" + password
		if len(open.Command) < len(wantBody) || string(open.Command[:len(wantBody)]) != wantBody {
			t.Errorf("cuerpo del open %q", open.Command)
			return
		}

		for i := 0; i < 2; i++ {
			req, rerr := readEncryptedPacket(conn, key)
			if rerr != nil {
				return
			}
			if req.Type != 0x0C && req.Type != 0x0D {
				t.Errorf("request tipo 0x%02x", req.Type)
				return
			}
		}

		// El timbre manda el 0x0D cifrado, como cualquier cabecera.
		ready := make([]byte, 32)
		ready[0] = 0x0D
		encReady, eerr := aesCBC(key, ready, true)
		if eerr != nil {
			t.Errorf("cifrando el 0x0D: %v", eerr)
			return
		}
		conn.Write(encReady)

		for {
			pkt, rerr := readEncryptedPacket(conn, key)
			if rerr != nil {
				return
			}
			if pkt.Type == 0xA2 {
				select {
				case audio <- pkt.Command:
				default:
				}
			}
		}
	}()
	return ln.Addr().String(), audio, func() {
		ln.Close()
		<-done
	}
}

func TestDialTalkHandshakeAndAudio(t *testing.T) {
	key := make([]byte, 32)
	for i := range key {
		key[i] = byte(i + 1)
	}
	addr, audio, stop := falseBell(t, key, "secreto")
	defer stop()

	sess, err := dialTalk(addr, key, "secreto", 1, 3*time.Second)
	if err != nil {
		t.Fatalf("dialTalk: %v", err)
	}
	defer sess.Close()

	pcm := make([]byte, 640)
	binary.LittleEndian.PutUint16(pcm[0:], 2000)
	binary.LittleEndian.PutUint16(pcm[2:], 3000)
	if err := sess.SendPCM(pcm); err != nil {
		t.Fatalf("SendPCM: %v", err)
	}

	select {
	case body := <-audio:
		if len(body) < 8+320 {
			t.Fatalf("paquete de audio de %d bytes", len(body))
		}
		if got := binary.LittleEndian.Uint32(body[:4]); got != 0xF0010000 {
			t.Fatalf("marca del cuerpo 0x%08x", got)
		}
		if body[4] != 0x0E {
			t.Fatalf("codec del cuerpo 0x%02x, se esperaba A-law", body[4])
		}
		if n := binary.LittleEndian.Uint16(body[6:8]); n != 320 {
			t.Fatalf("longitud declarada %d", n)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("el timbre falso no recibio audio")
	}
}

func TestTalkChannelFallsBackToBroadcast(t *testing.T) {
	if got := talkChannel(0); got != 65535 {
		t.Fatalf("canal 0 -> %d, se esperaba 65535", got)
	}
	if got := talkChannel(3); got != 3 {
		t.Fatalf("canal 3 -> %d", got)
	}
}

// readEncryptedPacket lee un paquete QUII de un socket falso de pruebas:
// cabecera de 32 bytes cifrada, cuerpo cifrado del tamano que anuncia la
// cabecera y recortado al tamano real.
func readEncryptedPacket(conn net.Conn, key []byte) (*packet, error) {
	conn.SetReadDeadline(time.Now().Add(3 * time.Second))
	enc := make([]byte, 32)
	if _, err := io.ReadFull(conn, enc); err != nil {
		return nil, err
	}
	header, err := aesCBC(key, enc, false)
	if err != nil {
		return nil, err
	}
	typ := header[0]
	// El tamano vive en sitios distintos segun el tipo: en los paquetes de
	// audio (0xA2) el cuerpo se declara con u32 en 11, y en el resto con
	// u16 en 9.
	var size int
	if typ == 0xA2 {
		size = int(binary.LittleEndian.Uint32(header[11:]))
	} else {
		size = int(binary.LittleEndian.Uint16(header[9:]))
		if size == 0 {
			size = int(binary.LittleEndian.Uint16(header[11:]))
		}
	}
	body := make([]byte, 0, size)
	if size > 0 {
		encBody := make([]byte, size)
		if _, err := io.ReadFull(conn, encBody); err != nil {
			return nil, err
		}
		if typ == 0xA2 {
			// En el audio de talk solo van cifrados los 32 primeros bytes.
			cryptLen := 32
			if size < cryptLen {
				cryptLen = size - size%16
			}
			if cryptLen > 0 {
				dec, derr := aesCBC(key, encBody[:cryptLen], false)
				if derr != nil {
					return nil, derr
				}
				body = append(body, dec...)
			}
			body = append(body, encBody[cryptLen:]...)
		} else {
			dec, derr := aesCBC(key, encBody, false)
			if derr != nil {
				return nil, derr
			}
			body = dec
		}
	}
	return &packet{Type: typ, B13: header[13], B14: header[14], Command: body}, nil
}
