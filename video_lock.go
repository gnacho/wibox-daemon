package main

import (
	"fmt"
	"os"
	"syscall"
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
	return func() {
		syscall.Flock(int(f.Fd()), syscall.LOCK_UN)
		f.Close()
	}, nil
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
