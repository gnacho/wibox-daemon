package main

import (
	"encoding/json"
	"fmt"
	"log"
	"net"
	"os"
	"time"
)

// El timbre solo admite una sesion QUII a la vez. Cuando go2rtc tiene el
// stream abierto, el proceso de la API no puede abrir otro tunel para la
// puerta: se pelearían por la sesion y el timbre contestaria BUSY. La
// solucion es que la sesion de video preste su canal: el proceso de -video
// escucha en un socket local y ejecuta la orden de apertura por el tunel que
// ya tiene montado.

const defaultControlSock = "/tmp/wibox-video.sock"

type controlRequest struct {
	Command string `json:"cmd"`
}

type controlResponse struct {
	OK    bool   `json:"ok"`
	RC    int    `json:"rc"`
	Error string `json:"error,omitempty"`
}

// controlServer atiende peticiones de apertura sobre un socket unix. El pin no
// viaja por el socket: el proceso de video ya lo tiene en su configuracion.
type controlServer struct {
	path string
	ln   net.Listener
}

func controlSockPath(explicit string) string {
	if explicit != "" {
		return explicit
	}
	if v := os.Getenv("WIBOX_CONTROL_SOCK"); v != "" {
		return v
	}
	return defaultControlSock
}

func newControlServer(path string, unlock func() (int, error)) (*controlServer, error) {
	// Un fichero de socket viejo impide escuchar. Solo se borra si no hay
	// nadie detras: si otro proceso atiende, se devuelve error para no
	// robarle el canal.
	if _, err := os.Stat(path); err == nil {
		if conn, derr := net.DialTimeout("unix", path, 300*time.Millisecond); derr == nil {
			conn.Close()
			return nil, fmt.Errorf("ya hay un servidor de control en %s", path)
		}
		if rerr := os.Remove(path); rerr != nil {
			return nil, rerr
		}
	}
	ln, err := net.Listen("unix", path)
	if err != nil {
		return nil, err
	}
	if cerr := os.Chmod(path, 0o600); cerr != nil {
		ln.Close()
		return nil, cerr
	}
	s := &controlServer{path: path, ln: ln}
	go s.serve(unlock)
	return s, nil
}

func (s *controlServer) serve(unlock func() (int, error)) {
	for {
		conn, err := s.ln.Accept()
		if err != nil {
			return
		}
		go s.handle(conn, unlock)
	}
}

func (s *controlServer) handle(conn net.Conn, unlock func() (int, error)) {
	defer conn.Close()
	conn.SetDeadline(time.Now().Add(30 * time.Second))

	var req controlRequest
	if err := json.NewDecoder(conn).Decode(&req); err != nil {
		json.NewEncoder(conn).Encode(controlResponse{RC: -1, Error: "peticion ilegible"})
		return
	}
	if req.Command != "unlock" {
		json.NewEncoder(conn).Encode(controlResponse{RC: -1, Error: "orden desconocida"})
		return
	}
	rc, err := unlock()
	resp := controlResponse{OK: err == nil, RC: rc}
	if err != nil {
		resp.Error = err.Error()
		log.Printf("control: apertura fallida rc=%d: %v", rc, err)
	} else {
		log.Printf("control: apertura por la sesion de video rc=0")
	}
	json.NewEncoder(conn).Encode(resp)
}

func (s *controlServer) close() {
	if s == nil {
		return
	}
	s.ln.Close()
	os.Remove(s.path)
}

// controlUnlock intenta abrir la puerta por la sesion de video en curso. El
// segundo valor dice si habia alguien escuchando: si no, la API sigue con su
// propio camino.
func controlUnlock(path string) (int, bool, error) {
	conn, err := net.DialTimeout("unix", controlSockPath(path), 500*time.Millisecond)
	if err != nil {
		return -1, false, nil
	}
	defer conn.Close()
	// La apertura puede esperar a que la sesion de video termine de montarse.
	conn.SetDeadline(time.Now().Add(45 * time.Second))
	if err := json.NewEncoder(conn).Encode(controlRequest{Command: "unlock"}); err != nil {
		return -1, true, err
	}
	var resp controlResponse
	if err := json.NewDecoder(conn).Decode(&resp); err != nil {
		return -1, true, fmt.Errorf("respuesta de control ilegible: %w", err)
	}
	if !resp.OK {
		if resp.Error == "" {
			resp.Error = "apertura rechazada"
		}
		return resp.RC, true, fmt.Errorf("%s", resp.Error)
	}
	return resp.RC, true, nil
}
