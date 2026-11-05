package main

import (
	"io"
	"log"
	"time"
)

// El timbre duerme a los pocos segundos y la sesion se cae sola. Si el proceso
// de video muere, go2rtc no lo relanza hasta que alguien vuelve a pedir el
// stream, asi que quien mira la camara en Home Assistant se queda sin imagen.
// Este bucle vuelve a montar la sesion con credenciales frescas hasta que el
// proceso reciba una senal.
var videoRetryDelay = 5 * time.Second

type videoCreds struct {
	Session  string
	Key      []byte
	Password string
}

func runVideoLoop(cfg config, fetch func() (*videoCreds, error), out io.Writer, audioOut io.Writer, stop <-chan struct{}) error {
	for attempt := 1; ; attempt++ {
		select {
		case <-stop:
			return nil
		default:
		}

		creds, err := fetch()
		if err != nil {
			log.Printf("video: sin credenciales frescas en el intento %d: %v", attempt, err)
		} else if err := runVideo(cfg, creds.Session, creds.Key, creds.Password, out, audioOut); err != nil {
			log.Printf("video: la sesion termino en el intento %d: %v", attempt, err)
		} else {
			log.Printf("video: la sesion se cerro limpiamente en el intento %d", attempt)
		}

		select {
		case <-stop:
			return nil
		case <-time.After(videoRetryDelay):
		}
	}
}
