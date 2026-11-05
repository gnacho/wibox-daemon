package main

import (
	"net"
	"testing"
	"time"
)

// fakeBell escucha en un puerto UDP local y, por cada sondeo que recibe,
// contesta con otro frame de sonda marcado como request (flag 0), que es lo
// que hace el timbre cuando quiere que le abramos el agujero del NAT.
func fakeBell(t *testing.T, sessionFlag string) (*net.UDPConn, int) {
	t.Helper()
	srv, err := net.ListenUDP("udp4", &net.UDPAddr{IP: net.IPv4(127, 0, 0, 1)})
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	t.Cleanup(func() { srv.Close() })
	port := srv.LocalAddr().(*net.UDPAddr).Port

	go func() {
		buf := make([]byte, 2048)
		for {
			_, sender, rerr := srv.ReadFromUDP(buf)
			if rerr != nil {
				return
			}
			reply := buildP2pTestPacket(p2pProbeSeqP2P, sessionFlag, 0, port, "127.0.0.1", port, 0)
			srv.WriteToUDP(reply, sender)
		}
	}()
	return srv, port
}

func TestProbeFindsBellInParallel(t *testing.T) {
	const flag = "abcdefghijklmnopqrstuvwxyz0123456789abcdefghijklmnopqrstuvwxyz01"
	_, bellPort := fakeBell(t, flag)

	conn, err := net.ListenUDP("udp4", &net.UDPAddr{IP: net.IPv4(127, 0, 0, 1)})
	if err != nil {
		t.Fatalf("socket: %v", err)
	}
	defer conn.Close()
	localPort := conn.LocalAddr().(*net.UDPAddr).Port

	info := &p2pConnectInfo{
		PublicIP:      "127.0.0.1",
		PublicUDPPort: bellPort,
		NetType:       0,
	}

	start := time.Now()
	res, perr := runProbeWithHandshake(conn, info, flag, 0, localPort, 300*time.Millisecond, 2)
	elapsed := time.Since(start)
	if perr != nil {
		t.Fatalf("probe: %v", perr)
	}
	if res == nil || res.peer == nil {
		t.Fatal("sin peer")
	}
	if !res.acked {
		t.Fatalf("se esperaba el ACK del request, res=%+v", res)
	}
	if res.rank != 2 {
		t.Fatalf("rank=%d, se esperaban 2", res.rank)
	}
	if res.peer.Port != bellPort {
		t.Fatalf("peer=%s, se esperaba el puerto %d", res.peer, bellPort)
	}
	// Se sondean todos los targets a la vez, asi que no se paga un intento
	// entero por cada uno: con 300 ms y 2 intentos sobra margen.
	if elapsed > 900*time.Millisecond {
		t.Fatalf("la sonda tardo %s, se esperaba menos de 900 ms", elapsed)
	}
}

func TestProbeGivesUpWithoutAnswer(t *testing.T) {
	srv, err := net.ListenUDP("udp4", &net.UDPAddr{IP: net.IPv4(127, 0, 0, 1)})
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	port := srv.LocalAddr().(*net.UDPAddr).Port
	srv.Close()

	conn, err := net.ListenUDP("udp4", &net.UDPAddr{IP: net.IPv4(127, 0, 0, 1)})
	if err != nil {
		t.Fatalf("socket: %v", err)
	}
	defer conn.Close()

	info := &p2pConnectInfo{PublicIP: "127.0.0.1", PublicUDPPort: port, NetType: 0}
	if _, perr := runProbeWithHandshake(conn, info, "flag", 0, conn.LocalAddr().(*net.UDPAddr).Port, 100*time.Millisecond, 1); perr == nil {
		t.Fatal("se esperaba error sin respuesta")
	}
}

func TestProbeWithoutTargets(t *testing.T) {
	conn, err := net.ListenUDP("udp4", &net.UDPAddr{IP: net.IPv4(127, 0, 0, 1)})
	if err != nil {
		t.Fatalf("socket: %v", err)
	}
	defer conn.Close()

	if _, perr := runProbeWithHandshake(conn, &p2pConnectInfo{NetType: 3}, "flag", 0, 0, time.Second, 1); perr == nil {
		t.Fatal("se esperaba error sin targets")
	}
}
