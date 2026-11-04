package main

import (
	"errors"
	"fmt"
	"log"
	"time"
)

// waitQuiiSetup espera la respuesta del setup QUII, que son 32 bytes en claro
// con 0xA9 en el primer byte.
func waitQuiiSetup(in <-chan []byte, timeout time.Duration) ([]byte, error) {
	deadline := time.After(timeout)
	var buf []byte
	for {
		select {
		case chunk, ok := <-in:
			if !ok {
				return nil, fmt.Errorf("el tunel se cerro esperando el setup")
			}
			buf = append(buf, chunk...)
			if len(buf) >= 32 {
				return buf[:32], nil
			}
		case <-deadline:
			return nil, fmt.Errorf("timeout esperando la respuesta del setup QUII")
		}
	}
}

// remoteOpen abre la puerta por la cadena remota propia: tunel RB-UDP hasta el
// timbre y la misma orden QUII que por LAN. No depende del movil ni del SDK
// del fabricante.
func remoteOpen(cfg config, session string, key []byte, password string) (int, error) {
	if cfg.pin == "" {
		return -1, fmt.Errorf("falta WIBOX_UNLOCK_PASSWORD: el pin es obligatorio para abrir")
	}
	if len(key) != 32 {
		return -1, fmt.Errorf("data-encode-key de %d bytes, se esperaban 32", len(key))
	}
	s, err := establishRemote(cfg, session)
	if err != nil {
		return -1, err
	}
	defer s.close()
	if s.probe == nil || s.probe.peer == nil {
		return -1, fmt.Errorf("sin peer para el tunel")
	}

	tun := newRbTunnel(s.conn, s.probe.peer, s.sessionFlag, key, password, cfg.channel, cfg.stream)
	tun.start()
	defer tun.close()

	if err := tun.awaitConnected(20 * time.Second); err != nil {
		return -1, err
	}
	log.Printf("tunel RB-UDP conectado con %s", s.probe.peer)

	// El setup lo manda el propio tunel en cuanto el timbre le asigna canal; aqui
	// solo se espera el identificador y la respuesta.
	if _, err := tun.awaitQuiiDestID(15 * time.Second); err != nil {
		return -1, err
	}
	setup, err := waitQuiiSetup(tun.incoming(), 8*time.Second)
	if err != nil {
		return -1, err
	}
	if setup[0] != 0xA9 {
		return -1, fmt.Errorf("setup con tipo 0x%02x", setup[0])
	}
	if setup[9] != 0 {
		return -1, fmt.Errorf("setup rechazado status=%d", setup[9])
	}
	if setup[10] != 2 || setup[11] != 1 {
		return -1, fmt.Errorf("setup crypto=%d sha=%d", setup[10], setup[11])
	}
	log.Printf("setup QUII ok crypto=%d sha=%d", setup[10], setup[11])

	// El play va despues del setup, no antes: sin play el timbre no abre el
	// flujo ni completa el play sync.
	if err := tun.sendQuiiPlay(); err != nil {
		return -1, err
	}
	log.Printf("play remoto enviado")

	// La apertura va por el mismo canal transparente que en local: orden 4 con
	// el SHA-256 del pin. Lo unico que cambia es el transporte.
	pkt, err := encryptPacket(key, 0xFE, unlockBody(cfg.pin, 1, 1), 4, 0, 0, 0, 0)
	if err != nil {
		return -1, err
	}
	if err := tun.sendQuii(pkt); err != nil {
		return -1, err
	}

	deadline := time.After(20 * time.Second)
	var buf []byte
	for {
		select {
		case chunk, ok := <-tun.incoming():
			if !ok {
				return -1, fmt.Errorf("el tunel se cerro esperando la respuesta de apertura")
			}
			buf = append(buf, chunk...)
			for {
				p, used, derr := decodeQuiiPacket(key, buf)
				if derr != nil {
					if errors.Is(derr, errShortPacket) {
						break
					}
					return -1, derr
				}
				buf = buf[used:]
				if p.Type != 0xFE || (p.B13 != 4 && p.B14 != 4) {
					continue
				}
				if len(p.Command) < 2 {
					return -1, fmt.Errorf("respuesta de apertura corta: % x", p.Command)
				}
				if p.Command[0] == 0 {
					return 0, nil
				}
				if p.Command[1] == 2 {
					return -10029, fmt.Errorf("contrasena de apertura rechazada")
				}
				return -1, fmt.Errorf("apertura rechazada: % x", p.Command)
			}
		case <-deadline:
			return -1, fmt.Errorf("timeout esperando la respuesta de apertura")
		}
	}
}
