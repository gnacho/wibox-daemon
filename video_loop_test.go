package main

import (
	"errors"
	"testing"
	"time"
)

func TestVideoLoopRetriesUntilStopped(t *testing.T) {
	old := videoRetryDelay
	videoRetryDelay = time.Millisecond
	defer func() { videoRetryDelay = old }()

	calls := 0
	stop := make(chan struct{})
	fetch := func() (*videoCreds, error) {
		calls++
		if calls == 3 {
			close(stop)
		}
		return nil, errors.New("cloud caido")
	}

	done := make(chan error, 1)
	go func() { done <- runVideoLoop(config{}, fetch, nil, nil, stop) }()

	select {
	case err := <-done:
		if err != nil {
			t.Fatalf("el bucle devolvio error: %v", err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("el bucle no paro con la senal")
	}
	if calls < 3 {
		t.Fatalf("solo hubo %d intentos", calls)
	}
}

func TestVideoLoopStopsBeforeTheFirstAttempt(t *testing.T) {
	stop := make(chan struct{})
	close(stop)
	called := false
	err := runVideoLoop(config{}, func() (*videoCreds, error) {
		called = true
		return nil, nil
	}, nil, nil, stop)
	if err != nil {
		t.Fatalf("error inesperado: %v", err)
	}
	if called {
		t.Fatal("no deberia pedir credenciales con la senal ya cerrada")
	}
}

func TestNextVideoDelayDoublesUpToTheCeiling(t *testing.T) {
	delay := videoRetryDelay
	var got []time.Duration
	for i := 0; i < 5; i++ {
		delay = nextVideoDelay(delay, videoRetryMax)
		got = append(got, delay)
	}
	want := []time.Duration{10 * time.Second, 20 * time.Second, 40 * time.Second, 60 * time.Second, 60 * time.Second}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("paso %d: %v, se esperaba %v", i, got[i], want[i])
		}
	}
	if nextVideoDelay(0, videoRetryMax) != videoRetryDelay {
		t.Fatal("una espera de cero debe volver a la inicial")
	}
}
