package main

import (
	"io"
	"log"
	"os"
	"sync"
	"time"
)

// Tope y periodo de la rotacion del log propio. El add-on guarda el log del
// proceso de video en /data/video.log y puede estar semanas en marcha, asi que
// el fichero se rota solo: al pasar del tope se guarda una copia y se empieza
// otro, sin tener que reiniciar el contenedor.
const (
	logRotateMax      = 2 * 1024 * 1024
	logRotateInterval = 30 * time.Second
)

type rotatingWriter struct {
	mu       sync.Mutex
	path     string
	max      int64
	interval time.Duration
	last     time.Time
	file     *os.File
}

func newRotatingWriter(path string, max int64, interval time.Duration) (*rotatingWriter, error) {
	f, err := os.OpenFile(path, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o644)
	if err != nil {
		return nil, err
	}
	return &rotatingWriter{path: path, max: max, interval: interval, last: time.Now(), file: f}, nil
}

func (w *rotatingWriter) Write(p []byte) (int, error) {
	w.mu.Lock()
	defer w.mu.Unlock()
	if w.max > 0 && time.Since(w.last) >= w.interval {
		w.last = time.Now()
		if info, err := w.file.Stat(); err == nil && info.Size() >= w.max {
			_ = w.rotateLocked()
		}
	}
	return w.file.Write(p)
}

func (w *rotatingWriter) rotateLocked() error {
	w.file.Close()
	if err := os.Rename(w.path, w.path+".1"); err != nil && !os.IsNotExist(err) {
		// Si no se puede renombrar se sigue escribiendo en el mismo fichero:
		// perder el log es peor que no rotarlo.
		f, oerr := os.OpenFile(w.path, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o644)
		if oerr != nil {
			return oerr
		}
		w.file = f
		return err
	}
	f, err := os.OpenFile(w.path, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o644)
	if err != nil {
		return err
	}
	w.file = f
	return nil
}

func (w *rotatingWriter) Close() error {
	w.mu.Lock()
	defer w.mu.Unlock()
	return w.file.Close()
}

// setupLogFile manda el log a stderr y, si se pide, tambien a un fichero que se
// rota solo. Devuelve la funcion de cierre.
func setupLogFile(path string) (func(), error) {
	if path == "" {
		return func() {}, nil
	}
	w, err := newRotatingWriter(path, logRotateMax, logRotateInterval)
	if err != nil {
		return func() {}, err
	}
	log.SetOutput(io.MultiWriter(os.Stderr, w))
	return func() {
		w.Close()
		log.SetOutput(os.Stderr)
	}, nil
}
