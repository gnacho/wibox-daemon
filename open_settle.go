package main

import (
	"log"
	"os"
	"strconv"
	"strings"
	"time"
)

// El timbre admite una sola sesion QUII. Cuando la API acaba de abrir, su
// sesion sigue viva un rato y el proceso de video no consigue montar el tunel:
// medido en mas de 45 s sin pasar del p2pconnect. La API deja la hora del ultimo
// open en un fichero junto al cerrojo del video y el proceso de video espera a
// que esa ventana pase, en vez de gastar el presupuesto remoto a ciegas.
const lastOpenSuffix = ".lastopen"

// defaultOpenSettle es la ventana que se le da al timbre para soltar la sesion
// de una apertura reciente.
const defaultOpenSettle = 75 * time.Second

func lastOpenPath(lockPath string) string {
	return lockPath + lastOpenSuffix
}

// markOpen deja constancia de una apertura para el proceso de video.
func markOpen(lockPath string, at time.Time) error {
	if lockPath == "" {
		return nil
	}
	return os.WriteFile(lastOpenPath(lockPath), []byte(strconv.FormatInt(at.UnixNano(), 10)+"\n"), 0o644)
}

// pendingOpenSettle dice cuanto queda por esperar de la ventana de una apertura
// reciente. Cero significa que se puede conectar ya.
func pendingOpenSettle(lockPath string, window time.Duration, now time.Time) time.Duration {
	if lockPath == "" || window <= 0 {
		return 0
	}
	raw, err := os.ReadFile(lastOpenPath(lockPath))
	if err != nil {
		return 0
	}
	nanos, err := strconv.ParseInt(strings.TrimSpace(string(raw)), 10, 64)
	if err != nil {
		return 0
	}
	elapsed := now.Sub(time.Unix(0, nanos))
	if elapsed < 0 || elapsed >= window {
		return 0
	}
	return window - elapsed
}

func awaitOpenSettle(lockPath string, window time.Duration) {
	wait := pendingOpenSettle(lockPath, window, time.Now())
	if wait <= 0 {
		return
	}
	log.Printf("video: apertura reciente, el timbre aun tiene la sesion; espero %s", wait.Round(time.Second))
	time.Sleep(wait)
}
