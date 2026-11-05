package main

import (
	"net/http"
	"time"
)

// newHTTPServer deja el servidor con los tiempos puestos: sin
// ReadHeaderTimeout, una conexion que abre y no manda nada retiene un
// descriptor para siempre. WriteTimeout se queda a cero a proposito, porque
// /open puede pasar minutos buscando el timbre y un limite lo cortaria.
func newHTTPServer(addr string, handler http.Handler) *http.Server {
	return &http.Server{
		Addr:              addr,
		Handler:           handler,
		ReadHeaderTimeout: 10 * time.Second,
		IdleTimeout:       60 * time.Second,
	}
}
