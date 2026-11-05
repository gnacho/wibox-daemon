package main

import (
	"bytes"
	"crypto/sha256"
	"encoding/binary"
	"encoding/hex"
	"io"
	"net"
	"testing"
	"time"
)

// fakeQuiiServer levanta un timbre de mentira que sigue el protocolo al pie de
// la letra: responde al setup en claro, descifra la orden transparente y
// comprueba el SHA-256 pegado al cuerpo antes de contestar.
type fakeQuiiServer struct {
	key       []byte
	gotBody   []byte
	gotType   byte
	gotB13    byte
	reply     []byte
	handshake chan struct{}
}

func startFakeQuii(t *testing.T, srv *fakeQuiiServer) string {
	t.Helper()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	t.Cleanup(func() { ln.Close() })

	go func() {
		conn, err := ln.Accept()
		if err != nil {
			return
		}
		defer conn.Close()
		conn.SetDeadline(time.Now().Add(10 * time.Second))

		setup := make([]byte, 32)
		if _, err := io.ReadFull(conn, setup); err != nil {
			return
		}
		if setup[0] != 0xA9 {
			t.Errorf("setup magic = %02x", setup[0])
			return
		}
		reply := make([]byte, 32)
		reply[0] = 0xA9
		reply[9] = 0
		reply[10] = 2
		reply[11] = 1
		if _, err := conn.Write(reply); err != nil {
			return
		}

		encHeader := make([]byte, 32)
		if _, err := io.ReadFull(conn, encHeader); err != nil {
			return
		}
		clear, err := aesCBC(srv.key, encHeader, false)
		if err != nil {
			t.Errorf("descifrar cabecera: %v", err)
			return
		}
		srv.gotType = clear[0]
		srv.gotB13 = clear[13]
		size := int(binary.LittleEndian.Uint16(clear[9:]))
		rawSize := int(binary.LittleEndian.Uint16(clear[11:]))

		encBody := make([]byte, size)
		if _, err := io.ReadFull(conn, encBody); err != nil {
			return
		}
		plain, err := aesCBC(srv.key, encBody, false)
		if err != nil {
			t.Errorf("descifrar cuerpo: %v", err)
			return
		}
		want := sha256.Sum256(append(append([]byte{}, clear...), plain[:rawSize]...))
		if !bytes.Equal(plain[rawSize:rawSize+32], want[:]) {
			t.Errorf("sha256 del cuerpo no cuadra")
			return
		}
		srv.gotBody = plain[:rawSize]
		close(srv.handshake)

		// Respuesta de exito: paquete transparente con b13 = comando.
		pkt, err := encryptPacketAt(srv.key, 0xFE, srv.reply, srv.gotB13, 0, 0, 0, 0, 1700000000)
		if err != nil {
			t.Errorf("cifrar respuesta: %v", err)
			return
		}
		conn.Write(pkt)
		// Deja al cliente leer la respuesta y cerrar.
		io.Copy(io.Discard, conn)
	}()
	return ln.Addr().String()
}

// Prueba de punta a punta del camino LAN: DialQuii hace el setup, Unlock manda
// la orden 4 cifrada y el servidor de loopback la descifra y contesta exito.
func TestUnlockOverSocket(t *testing.T) {
	key := make([]byte, 32)
	for i := range key {
		key[i] = byte(i * 3)
	}
	srv := &fakeQuiiServer{key: key, reply: []byte{0, 0}, handshake: make(chan struct{})}
	addr := startFakeQuii(t, srv)

	client, err := DialQuii(addr, key, encodeDevicePassword("token-de-64-caracteres"), 1, 1, 5*time.Second)
	if err != nil {
		t.Fatalf("DialQuii: %v", err)
	}
	defer client.Close()

	rc, err := client.Unlock("1379", 1, 1)
	if err != nil {
		t.Fatalf("Unlock: %v", err)
	}
	if rc != 0 {
		t.Fatalf("rc=%d", rc)
	}

	select {
	case <-srv.handshake:
	case <-time.After(5 * time.Second):
		t.Fatal("el servidor no recibio la orden")
	}
	if srv.gotType != 0xFE {
		t.Fatalf("tipo de paquete = %02x", srv.gotType)
	}
	if srv.gotB13 != 4 {
		t.Fatalf("comando = %d, esperaba 4", srv.gotB13)
	}
	sum := sha256.Sum256([]byte("1379"))
	want := append([]byte{1, 0, 1, 1}, make([]byte, 12)...)
	want = append(want, []byte(hex.EncodeToString(sum[:]))...)
	if !bytes.Equal(srv.gotBody, want) {
		t.Fatalf("cuerpo de apertura:\n got % x\nwant % x", srv.gotBody, want)
	}
}

// Si el timbre contesta error de contrasena (resp[1] == 2) el cliente lo
// traduce al codigo del fabricante en vez de dar exito.
func TestUnlockRejectedPassword(t *testing.T) {
	key := bytes.Repeat([]byte{7}, 32)
	srv := &fakeQuiiServer{key: key, reply: []byte{1, 2}, handshake: make(chan struct{})}
	addr := startFakeQuii(t, srv)

	client, err := DialQuii(addr, key, "x", 1, 1, 5*time.Second)
	if err != nil {
		t.Fatalf("DialQuii: %v", err)
	}
	defer client.Close()

	rc, err := client.Unlock("0000", 1, 1)
	if err == nil {
		t.Fatal("esperaba error de contrasena rechazada")
	}
	if rc != -10029 {
		t.Fatalf("rc=%d, esperaba -10029", rc)
	}
}

// El setup tiene que fallar si el timbre responde un status distinto de 0 en
// vez de colgarse esperando paquetes.
func TestSetupRejectsBadStatus(t *testing.T) {
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	defer ln.Close()
	go func() {
		conn, err := ln.Accept()
		if err != nil {
			return
		}
		defer conn.Close()
		buf := make([]byte, 32)
		io.ReadFull(conn, buf)
		reply := make([]byte, 32)
		reply[0] = 0xA9
		reply[9] = 3
		conn.Write(reply)
		io.Copy(io.Discard, conn)
	}()

	if _, err := DialQuii(ln.Addr().String(), bytes.Repeat([]byte{1}, 32), "p", 1, 1, 3*time.Second); err == nil {
		t.Fatal("esperaba error de setup con status=3")
	}
}
