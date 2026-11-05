package main

import (
	"crypto/sha256"
	"encoding/binary"
	"fmt"
	"io"
	"net"
	"testing"
	"time"
)

// fakePlayBell acepta una conexion, hace el setup y guarda el primer paquete
// cifrado que llega, que es el play. Se queda con la cabecera descifrada y el
// cuerpo para que el test compruebe que el PLAY sale como el timbre espera.
type fakePlayBell struct {
	key     []byte
	packet  chan packet
	failure chan error
}

func startFakePlayBell(t *testing.T, bell *fakePlayBell) string {
	t.Helper()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	t.Cleanup(func() { ln.Close() })
	go func() {
		conn, aerr := ln.Accept()
		if aerr != nil {
			bell.failure <- aerr
			return
		}
		defer conn.Close()
		conn.SetDeadline(time.Now().Add(5 * time.Second))

		setup := make([]byte, 32)
		if _, rerr := io.ReadFull(conn, setup); rerr != nil {
			bell.failure <- fmt.Errorf("setup: %w", rerr)
			return
		}
		if setup[0] != 0xA9 {
			bell.failure <- fmt.Errorf("setup type=0x%02x", setup[0])
			return
		}
		reply := make([]byte, 32)
		reply[0] = 0xA9
		reply[9] = 0
		reply[10] = 2
		reply[11] = 1
		if _, werr := conn.Write(reply); werr != nil {
			bell.failure <- fmt.Errorf("reply: %w", werr)
			return
		}

		header := make([]byte, 32)
		if _, rerr := io.ReadFull(conn, header); rerr != nil {
			bell.failure <- fmt.Errorf("header: %w", rerr)
			return
		}
		clear, derr := aesCBC(bell.key, header, false)
		if derr != nil {
			bell.failure <- fmt.Errorf("cabecera: %w", derr)
			return
		}
		size := int(binary.LittleEndian.Uint16(clear[9:]))
		rawSize := int(binary.LittleEndian.Uint16(clear[11:]))
		body := make([]byte, size)
		if _, rerr := io.ReadFull(conn, body); rerr != nil {
			bell.failure <- fmt.Errorf("cuerpo: %w", rerr)
			return
		}
		plain, derr := aesCBC(bell.key, body, false)
		if derr != nil {
			bell.failure <- fmt.Errorf("cuerpo cifrado: %w", derr)
			return
		}
		bell.packet <- packet{
			Type:    clear[0],
			B13:     clear[13],
			B14:     clear[14],
			Command: plain[:rawSize],
		}
	}()
	return ln.Addr().String()
}

func TestPlaySendsTheExpectedBody(t *testing.T) {
	key := make([]byte, 32)
	for i := range key {
		key[i] = byte(i * 5)
	}
	bell := &fakePlayBell{key: key, packet: make(chan packet, 1), failure: make(chan error, 1)}
	addr := startFakePlayBell(t, bell)

	c, err := DialQuii(addr, key, "secreto", 1, 1, 2*time.Second)
	if err != nil {
		t.Fatalf("dial: %v", err)
	}
	if err := c.Play(); err != nil {
		t.Fatalf("play: %v", err)
	}
	defer c.CloseNow()

	select {
	case perr := <-bell.failure:
		t.Fatalf("timbre falso: %v", perr)
	case p := <-bell.packet:
		if p.Type != 0x01 {
			t.Fatalf("tipo=%d, se esperaba 0x01", p.Type)
		}
		if p.B13 != 1 || p.B14 != 0 {
			t.Fatalf("canal b13=%d b14=%d", p.B13, p.B14)
		}
		want := "adminapp2&&secreto\x00/mode=real&idc=1&ids=1&ap=2\x00"
		if string(p.Command) != want {
			t.Fatalf("cuerpo=%q, se esperaba %q", p.Command, want)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("el timbre falso no recibio el play")
	}
}

func TestPlayPacketKeepsTheDigest(t *testing.T) {
	key := make([]byte, 32)
	for i := range key {
		key[i] = byte(i)
	}
	pkt, err := encryptPacketAt(key, 0x01, []byte("cuerpo"), 1, 0, 1, 0, 0, 1234)
	if err != nil {
		t.Fatalf("encrypt: %v", err)
	}
	if len(pkt) < 32+32 {
		t.Fatalf("paquete corto: %d", len(pkt))
	}
	clear, err := aesCBC(key, pkt[:32], false)
	if err != nil {
		t.Fatalf("cabecera: %v", err)
	}
	size := int(binary.LittleEndian.Uint16(clear[9:]))
	plain, err := aesCBC(key, pkt[32:32+size], false)
	if err != nil {
		t.Fatalf("cuerpo: %v", err)
	}
	digest := sha256.Sum256(append(clear, []byte("cuerpo")...))
	if string(plain[6:38]) != string(digest[:]) {
		t.Fatal("el digest no coincide con cabecera+cuerpo")
	}
}
