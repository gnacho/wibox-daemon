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

// videoRetryMax es el techo del backoff: con el timbre horas dormido no tiene
// sentido montar un tunel nuevo cada cinco segundos.
var videoRetryMax = 60 * time.Second

type videoCreds struct {
	Session  string
	Key      []byte
	Password string
}

func runVideoLoop(cfg config, fetch func() (*videoCreds, error), out io.Writer, audioOut io.Writer, stop <-chan struct{}) error {
	delay := videoRetryDelay
	for attempt := 1; ; attempt++ {
		select {
		case <-stop:
			return nil
		default:
		}

		clean := false
		creds, err := fetch()
		if err != nil {
			log.Printf("video: sin credenciales frescas en el intento %d: %v", attempt, err)
		} else if err := runVideo(cfg, creds.Session, creds.Key, creds.Password, out, audioOut); err != nil {
			log.Printf("video: la sesion termino en el intento %d: %v", attempt, err)
		} else {
			log.Printf("video: la sesion se cerro limpiamente en el intento %d", attempt)
			clean = true
		}

		if clean {
			delay = videoRetryDelay
		} else {
			delay = nextVideoDelay(delay, videoRetryMax)
		}

		select {
		case <-stop:
			return nil
		case <-time.After(delay):
		}
	}
}

// nextVideoDelay duplica la espera anterior hasta el tope. Una espera de cero
// o negativa vuelve al valor inicial.
func nextVideoDelay(current, max time.Duration) time.Duration {
	if current <= 0 {
		return videoRetryDelay
	}
	next := current * 2
	if max > 0 && next > max {
		return max
	}
	return next
}
