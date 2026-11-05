package main

import (
	"io"
	"net"
	"testing"
	"time"
)

// bellStub acepta el handshake QUII y cierra: suficiente para ProbeQuii, que
// solo comprueba que el puerto habla el protocolo.
func bellStub(t *testing.T) string {
	t.Helper()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	t.Cleanup(func() { ln.Close() })
	go func() {
		for {
			conn, cerr := ln.Accept()
			if cerr != nil {
				return
			}
			go func(c net.Conn) {
				defer c.Close()
				c.SetDeadline(time.Now().Add(2 * time.Second))
				setup := make([]byte, 32)
				if _, rerr := io.ReadFull(c, setup); rerr != nil {
					return
				}
				reply := make([]byte, 32)
				reply[0] = 0xA9
				reply[10] = 2
				reply[11] = 1
				c.Write(reply)
			}(conn)
		}
	}()
	return ln.Addr().String()
}

func TestProbeHostsFindsTheBell(t *testing.T) {
	addr := bellStub(t)
	found := probeHosts([]string{addr, "127.0.0.1:1"}, 500*time.Millisecond, 4)
	if len(found) != 1 || found[0] != addr {
		t.Fatalf("encontrados=%v, se esperaba solo %s", found, addr)
	}
}

func TestProbeHostsWithoutBells(t *testing.T) {
	if found := probeHosts([]string{"127.0.0.1:1"}, 200*time.Millisecond, 2); len(found) != 0 {
		t.Fatalf("encontrados=%v, se esperaba ninguno", found)
	}
}

func TestLocalHostsSkipsLoopback(t *testing.T) {
	for _, h := range localHosts(34567) {
		if len(h) < 5 || h[:4] == "127." {
			t.Fatalf("host invalido en el escaneo: %s", h)
		}
	}
}
