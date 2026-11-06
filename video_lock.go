package main

import (
	"fmt"
	"log"
	"os"
	"strconv"
	"strings"
	"syscall"
	"time"
)

// El timbre solo admite una sesion QUII a la vez. Un segundo proceso de video
// (un espectador viejo que no murio, un vigilante de pruebas, dos fuentes de
// go2rtc) montaria otro tunel y el timbre contestaria BUSY o dejaria al otro
// sin media. El cerrojo es del sistema operativo, asi que se libera solo si el
// proceso muere, sin dejar ficheros huerfanos que bloqueen el arranque.
func acquireVideoLock(path string) (func(), error) {
	f, err := os.OpenFile(path, os.O_CREATE|os.O_RDWR, 0o600)
	if err != nil {
		return nil, err
	}
	if err := syscall.Flock(int(f.Fd()), syscall.LOCK_EX|syscall.LOCK_NB); err != nil {
		f.Close()
		return nil, fmt.Errorf("ya hay otra sesion de video en este equipo (%s ocupado)", path)
	}
	// Se apunta el pid dentro del fichero: el cerrojo no lo guarda en ningun
	// sitio y sin esto no hay forma de saber quien lo tiene desde /status.
	if err := f.Truncate(0); err == nil {
		if _, err := f.WriteAt([]byte(strconv.Itoa(os.Getpid())+"\n"), 0); err == nil {
			f.Sync()
		}
	}
	return func() {
		syscall.Flock(int(f.Fd()), syscall.LOCK_UN)
		f.Close()
	}, nil
}

// videoLockState devuelve si el cerrojo esta libre y, cuando no lo esta, el pid
// que el dueno dejo escrito en el fichero (0 si no se pudo leer).
func videoLockState(path string) (bool, int) {
	if path == "" {
		path = defaultVideoLock
	}
	release, err := acquireVideoLock(path)
	if err == nil {
		release()
		return true, 0
	}
	raw, rerr := os.ReadFile(path)
	if rerr != nil {
		return false, 0
	}
	pid, perr := strconv.Atoi(strings.TrimSpace(string(raw)))
	if perr != nil {
		return false, 0
	}
	return false, pid
}

// videoLockFree dice si el cerrojo esta libre sin quedarselo: si otro proceso
// de video lo tiene, /status puede explicar por que no hay sesion local.
func videoLockFree(path string) bool {
	if path == "" {
		path = defaultVideoLock
	}
	release, err := acquireVideoLock(path)
	if err != nil {
		return false
	}
	release()
	return true
}

// acquireVideoLockWait espera a que se libere el cerrojo en vez de rendirse al
// primer intento. Mientras espera, quien llama sigue alimentando el tubo, asi
// que un espectador nuevo ve la pista publicada y engancha la sesion real en
// cuanto la anterior acaba.
func acquireVideoLockWait(path string, wait time.Duration) (func(), error) {
	deadline := time.Now().Add(wait)
	for {
		release, err := acquireVideoLock(path)
		if err == nil {
			return release, nil
		}
		if wait <= 0 || !time.Now().Before(deadline) {
			return nil, err
		}
		log.Printf("video: %v; espero a que termine la otra sesion", err)
		time.Sleep(500 * time.Millisecond)
	}
}

// lockWait lee WIBOX_LOCK_WAIT en segundos (defecto 300). Cero o negativo hace
// que un cerrojo ocupado falle de inmediato, como antes.
func lockWait() time.Duration {
	secs, _ := strconv.Atoi(env("WIBOX_LOCK_WAIT", "300"))
	if secs <= 0 {
		return 0
	}
	return time.Duration(secs) * time.Second
}
