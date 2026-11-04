package main

import (
	"net"
	"testing"
	"time"
)

// probeHosts tiene que quedarse solo con la direccion donde hay un timbre QUII
// y descartar los puertos cerrados sin bloquearse.
func TestProbeHostsFindsDoorbell(t *testing.T) {
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	defer ln.Close()
	go func() {
		for {
			conn, err := ln.Accept()
			if err != nil {
				return
			}
			go func(c net.Conn) {
				defer c.Close()
				buf := make([]byte, 32)
				if _, err := c.Read(buf); err != nil {
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

	// Puerto cerrado: se coge uno efimero y se cierra enseguida.
	dead, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen dead: %v", err)
	}
	deadAddr := dead.Addr().String()
	dead.Close()

	found := probeHosts([]string{deadAddr, ln.Addr().String()}, 500*time.Millisecond, 4)
	if len(found) != 1 || found[0] != ln.Addr().String() {
		t.Fatalf("encontrados %v, esperaba solo %s", found, ln.Addr().String())
	}
}

// localHosts no debe devolver la IP de la propia maquina ni loopback.
func TestLocalHostsSkipsSelf(t *testing.T) {
	hosts := localHosts(34567)
	for _, h := range hosts {
		host, _, err := net.SplitHostPort(h)
		if err != nil {
			t.Fatalf("direccion mal formada %q", h)
		}
		if host == "127.0.0.1" {
			t.Fatalf("incluyo loopback: %s", h)
		}
	}
}
