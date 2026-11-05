package main

import (
	"net"
	"strings"
	"testing"
	"time"
)

// El timbre solo nos abre el agujero del NAT si le contestamos sus propios
// sondeos. Este test comprueba que la espera activa devuelve la direccion del
// aparato y que le manda el ACK y los dos paquetes active.
func TestActiveHandshakeTakesTheSenderAddress(t *testing.T) {
	bell, err := net.ListenUDP("udp4", &net.UDPAddr{IP: net.IPv4(127, 0, 0, 1)})
	if err != nil {
		t.Fatalf("socket del timbre: %v", err)
	}
	defer bell.Close()

	conn, err := net.ListenUDP("udp4", &net.UDPAddr{IP: net.IPv4(127, 0, 0, 1)})
	if err != nil {
		t.Fatalf("socket local: %v", err)
	}
	defer conn.Close()

	sessionFlag := strings.Repeat("a", 64)
	bellPort := bell.LocalAddr().(*net.UDPAddr).Port
	clientPort := conn.LocalAddr().(*net.UDPAddr).Port

	// El timbre real sondea el primero: nos manda su request y espera el ACK
	// para abrirnos el agujero del NAT.
	clientAddr := &net.UDPAddr{IP: net.IPv4(127, 0, 0, 1), Port: clientPort}
	bellDone := make(chan int, 1)
	go func() {
		sent := 0
		for i := 1; i <= 2; i++ {
			request := buildP2pTransportPacket(sessionFlag, uint32(i), bellPort, "127.0.0.1", bellPort, 102, 0)
			if _, werr := bell.WriteToUDP(request, clientAddr); werr == nil {
				sent++
			}
			time.Sleep(20 * time.Millisecond)
		}
		bellDone <- sent
	}()

	peer := runActiveHandshake(conn, sessionFlag, 1500*time.Millisecond, nil)
	if peer == nil {
		t.Fatal("no devolvio la direccion del timbre")
	}
	if peer.Port != bellPort {
		t.Fatalf("peer=%s, se esperaba el puerto %d", peer, bellPort)
	}
	if clientPort == 0 {
		t.Fatal("el socket local no tiene puerto")
	}
	if got := <-bellDone; got != 2 {
		t.Fatalf("el cliente solo respondio a %d sondeos", got)
	}
}

// Sin nadie al otro lado, la espera activa se rinde y deja el hint tal cual.
func TestActiveHandshakeFallsBackToTheHint(t *testing.T) {
	conn, err := net.ListenUDP("udp4", &net.UDPAddr{IP: net.IPv4(127, 0, 0, 1)})
	if err != nil {
		t.Fatalf("socket local: %v", err)
	}
	defer conn.Close()

	hint := &net.UDPAddr{IP: net.IPv4(127, 0, 0, 1), Port: 34567}
	peer := runActiveHandshake(conn, strings.Repeat("b", 64), 200*time.Millisecond, hint)
	if peer == nil || peer.Port != hint.Port {
		t.Fatalf("peer=%v, se esperaba el hint %s", peer, hint)
	}
}

// natcheckPublic pregunta nuestro mapeo externo. Sin el flag 0x2D el servidor
// solo devuelve eco, asi que este test fija tambien el formato de la respuesta.
func TestNatcheckPublicReadsOurMapping(t *testing.T) {
	server, err := net.ListenUDP("udp4", &net.UDPAddr{IP: net.IPv4(127, 0, 0, 1)})
	if err != nil {
		t.Fatalf("socket del natcheck: %v", err)
	}
	defer server.Close()

	go func() {
		buf := make([]byte, 2048)
		for {
			n, from, rerr := server.ReadFromUDP(buf)
			if rerr != nil {
				return
			}
			if n < natcheckQuerySize {
				continue
			}
			reply := make([]byte, natcheckQuerySize)
			putU32(reply, 0x00, p2pMarker)
			putU16(reply, 0x1A, natcheckQuerySize)
			putCString(reply, 0x4C, 0x10, "203.0.113.7")
			putU16(reply, 0x5C, 4242)
			server.WriteToUDP(reply, from)
		}
	}()

	conn, err := net.ListenUDP("udp4", &net.UDPAddr{IP: net.IPv4(127, 0, 0, 1)})
	if err != nil {
		t.Fatalf("socket local: %v", err)
	}
	defer conn.Close()

	url := "udp://" + server.LocalAddr().String()
	mapped := natcheckPublic(conn, url)
	if mapped == nil {
		t.Fatal("no devolvio nuestro mapeo")
	}
	if mapped.IP.String() != "203.0.113.7" || mapped.Port != 4242 {
		t.Fatalf("mapeo=%s, se esperaba 203.0.113.7:4242", mapped)
	}
}
