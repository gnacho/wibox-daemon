package main

import (
	"strings"
	"testing"
)

func TestVideoLockKeepsASecondSessionOut(t *testing.T) {
	path := t.TempDir() + "/video.lock"
	release, err := acquireVideoLock(path)
	if err != nil {
		t.Fatalf("primera sesion: %v", err)
	}
	if _, err := acquireVideoLock(path); err == nil {
		t.Fatal("la segunda sesion deberia rechazarse")
	} else if !strings.Contains(err.Error(), "ya hay otra sesion de video") {
		t.Fatalf("error poco claro: %v", err)
	}
	release()
	second, err := acquireVideoLock(path)
	if err != nil {
		t.Fatalf("tras liberar deberia entrar: %v", err)
	}
	second()
}
