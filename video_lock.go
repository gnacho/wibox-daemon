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
