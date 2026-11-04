package main

import (
	"fmt"
	"net/url"
)

// brokerHost devuelve solo host:puerto, para poder loguear el broker sin
// arrastrar el JWT de las credenciales que viaja en el param de la URL.
func brokerHost(raw string) string {
	u, err := url.Parse(raw)
	if err != nil {
		return "(ilegible)"
	}
	return u.Host
}

// probeRemote recorre la cadena remota hasta tener un peer sondeado y sale. No
// abre la puerta ni pide video.
func probeRemote(cfg config, session string) error {
	s, err := establishRemote(cfg, session)
	if err != nil {
		return err
	}
	defer s.close()

	fmt.Printf("p2pconnect ok session-flag=%s\n", s.sessionFlag)
	fmt.Printf("nettype=%d netsubtype=%d pub-ip=%s:%d utd=%s:%d\n",
		s.info.NetType, s.info.NetSubType, s.info.PublicIP, s.info.PublicUDPPort,
		s.info.UTDPublicIP, s.info.UTDPublicUDPPort)
	fmt.Printf("loc-ip=%v loc-udpport=%d\n", s.info.LocalIPs, s.info.LocalUDPPort)
	for _, t := range iterTargets(s.info, 0) {
		fmt.Printf("target %s %s:%d testmode=%d\n", t.Kind, t.IP, t.Port, t.TestMode)
	}
	fmt.Printf("probe ok peer=%s rank=%d acked=%v\n", s.probe.peer, s.probe.rank, s.probe.acked)
	return nil
}
