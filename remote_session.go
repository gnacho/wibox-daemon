package main

import (
	"fmt"
	"log"
	"net"
	"time"
)

// remoteSession es la cadena remota ya establecida hasta tener un peer UDP
// vivo: discovery, broker UST, p2pconnect, natcheck y sonda de handshake.
type remoteSession struct {
	conn        *net.UDPConn
	mqtt        *ustMqtt
	services    *ustServices
	info        *p2pConnectInfo
	sessionFlag string
	probe       *probeResult
	localPort   int
}

func (s *remoteSession) close() {
	if s.mqtt != nil {
		s.mqtt.close()
	}
	if s.conn != nil {
		s.conn.Close()
	}
}

// establishRemote recorre la cadena remota hasta dejar un peer listo para el
// tunel. El socket UDP es el mismo para natcheck, sonda y tunel: su puerto
// local se anuncia en update-netinfo antes de sondear, y el timbre responde
// ahi.
func establishRemote(cfg config, session string) (*remoteSession, error) {
	if cfg.deviceID == "" {
		return nil, fmt.Errorf("falta WIBOX_DEVICE_ID")
	}

	log.Printf("discovery en %s", cfg.baseURL)
	services, err := queryUstServices(cfg.baseURL, cfg.clientID, ustOEM, ustApp, 15*time.Second)
	if err != nil {
		return nil, err
	}
	broker := services.mqttURL()
	log.Printf("broker UST %s", brokerHost(broker))
	if nat := services.testAddress(); nat != "" {
		log.Printf("natcheck %s", nat)
	}

	conn, err := net.ListenUDP("udp4", &net.UDPAddr{})
	if err != nil {
		return nil, fmt.Errorf("socket UDP: %w", err)
	}
	localPort := conn.LocalAddr().(*net.UDPAddr).Port
	localIPs := localIPv4s()
	log.Printf("puerto UDP local %d, ips %v", localPort, localIPs)

	m := newUstMqtt(ustMqttConfig{
		BrokerURL: broker,
		ClientID:  cfg.clientID,
		DeviceID:  cfg.deviceID,
		Session:   session,
		Attempts:  20,
	})
	info, sessionFlag, err := m.connectAndP2PConnect(localIPs, localPort, "")
	if err != nil {
		m.close()
		conn.Close()
		return nil, err
	}

	s := &remoteSession{
		conn:        conn,
		mqtt:        m,
		services:    services,
		info:        info,
		sessionFlag: sessionFlag,
		localPort:   localPort,
	}

	// Nuestro mapping externo, que el timbre necesita para responder.
	if mapped := natcheck(conn, sessionFlag, info.ResponseSessionID, services.testAddress()); mapped != nil {
		log.Printf("natcheck mapped=%s", mapped)
		if err := m.publishNetinfo(localIPs, localPort, mapped.IP.String(), mapped.Port); err != nil {
			log.Printf("update-netinfo tras natcheck: %v", err)
		}
	}

	probe, err := runProbeWithHandshake(conn, info, sessionFlag, info.ResponseSessionID, localPort, 1500*time.Millisecond, 3)
	if err != nil {
		s.close()
		return nil, err
	}
	s.probe = probe

	// Si el peer no es de red local y no nos ha sondeado el, hay que abrir el
	// agujero del NAT esperando su sondeo y contestandolo.
	if probe.rank < 3 && !probe.acked {
		if peer := runActiveHandshake(conn, sessionFlag, 8*time.Second, probe.peer); peer != nil {
			log.Printf("handshake activo peer=%s", peer)
			probe.peer = peer
		}
	}
	return s, nil
}
