package main

import (
	"io"
	"log"
	"os"
)

// Los hijos que lanza go2rtc no llegan al log del contenedor: go2rtc se queda
// con su stderr. Con WIBOX_LOG_FILE el daemon escribe ademas en un fichero, que
// es la unica forma de ver que hace el proceso de video dentro del add-on.
func setupLogFile(path string) (func(), error) {
	if path == "" {
		return func() {}, nil
	}
	f, err := os.OpenFile(path, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o644)
	if err != nil {
		return func() {}, err
	}
	log.SetOutput(io.MultiWriter(os.Stderr, f))
	return func() { f.Close() }, nil
}
