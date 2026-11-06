package main

import (
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"os"
	"os/exec"
	"time"
)

// warmPoll es cada cuanto se mira quien esta viendo el directo. Un sondeo cada
// dos segundos basta: la ventana se mide en minutos.
var warmPoll = 2 * time.Second

// warmRetry es lo que se espera antes de volver a conectar el guardian cuando
// ffmpeg no arranca. Sin esto, un ffmpeg ausente daria un intento cada sondeo.
var warmRetry = 30 * time.Second

// keeperStopTimeout acota lo que se espera al guardian para que cierre su
// sesion RTSP antes de matarlo.
var keeperStopTimeout = 5 * time.Second

// warmKeeper es el espectador extra que mantiene vivo el productor de go2rtc.
type warmKeeper interface {
	stop()
	done() <-chan struct{}
}

// warmWindow mantiene el directo caliente. go2rtc para el productor `exec:` en
// cuanto se queda sin consumidores (no espera: mata el proceso con la senal de
// #killsignal), asi que cada apertura empieza de cero con natcheck, p2pconnect
// y la espera del keyframe. Mientras hay un consumidor propio enganchado, el
// productor y su tunel siguen vivos, y quien abre el directo se cuelga de la
// sesion ya montada en vez de montarla otra vez.
//
// El guardian se suelta cuando pasan `seconds` sin que haya nadie mas viendo,
// porque el timbre solo admite una sesion: dejarlo abierto bloquea la app del
// movil y a los vecinos.
type warmWindow struct {
	apiURL  string
	stream  string
	seconds time.Duration
	poll    time.Duration

	// start lanza el guardian. Los tests lo sustituyen por un doble.
	start func() (warmKeeper, error)
	// setActive publica en /status si la ventana esta abierta.
	setActive func(bool)
	client    *http.Client
}

// warmStep es el estado de la ventana entre sondeos.
type warmStep struct {
	active    bool
	idleSince time.Time
	retryAt   time.Time
}

// decide traduce un sondeo en lo que hay que hacer. Separado del bucle para
// poder probarlo sin relojes ni gorutinas.
func (w *warmWindow) decide(st *warmStep, viewers int, now time.Time) (attach, detach bool) {
	if !st.active {
		if viewers == 0 || now.Before(st.retryAt) {
			return false, false
		}
		return true, false
	}
	// Con el guardian conectado, los consumidores son el guardian y quien
	// este mirando. Uno solo significa que ya no hay espectador real.
	if viewers >= 2 {
		st.idleSince = now
		return false, false
	}
	if now.Sub(st.idleSince) < w.seconds {
		return false, false
	}
	return false, true
}

func (w *warmWindow) run(stop <-chan struct{}) {
	if w.seconds <= 0 {
		return
	}
	if w.poll <= 0 {
		w.poll = warmPoll
	}
	if w.setActive == nil {
		w.setActive = func(bool) {}
	}
	if w.client == nil {
		w.client = &http.Client{Timeout: 3 * time.Second}
	}

	var keeper warmKeeper
	var failures int
	st := warmStep{}

	drop := func(why string) {
		if keeper == nil {
			return
		}
		keeper.stop()
		keeper = nil
		st.active = false
		w.setActive(false)
		log.Printf("caliente: %s", why)
	}

	for {
		var exited <-chan struct{}
		if keeper != nil {
			exited = keeper.done()
		}
		timer := time.NewTimer(w.poll)
		select {
		case <-stop:
			timer.Stop()
			drop("el add-on se esta cerrando, se suelta el timbre")
			return
		case <-exited:
			timer.Stop()
			// El guardian se cayo solo (un corte del tunel lo tumba). Se
			// vuelve a intentar en el siguiente sondeo.
			keeper = nil
			st.active = false
			w.setActive(false)
			log.Printf("caliente: el guardian se cerro por su cuenta, se reintenta")
			continue
		case <-timer.C:
		}

		viewers, err := w.viewers()
		if err != nil {
			// Sin go2rtc (o con la API caida) el sondeo falla cada dos
			// segundos: se avisa al principio, luego cada minuto, y se deja
			// de repetir en cuanto vuelve a responder.
			failures++
			if shouldLogWarmFailure(failures) {
				log.Printf("caliente: %v (fallo %d)", err, failures)
			}
			continue
		}
		if failures > 0 {
			log.Printf("caliente: go2rtc vuelve a responder tras %d fallos", failures)
			failures = 0
		}
		now := time.Now()
		attach, detach := w.decide(&st, viewers, now)
		switch {
		case detach:
			drop(fmt.Sprintf("sin espectadores desde hace %s, se suelta el timbre", now.Sub(st.idleSince).Round(time.Second)))
		case attach:
			k, kerr := w.start()
			if kerr != nil {
				log.Printf("caliente: no se pudo conectar el guardian: %v", kerr)
				st.retryAt = now.Add(warmRetry)
				continue
			}
			keeper = k
			st.active = true
			st.idleSince = now
			w.setActive(true)
			log.Printf("caliente: guardian conectado, el directo se mantiene %s tras el ultimo espectador", w.seconds)
		}
	}
}

// viewers lee cuantos consumidores tiene el stream en la API local de go2rtc.
func (w *warmWindow) viewers() (int, error) {
	resp, err := w.client.Get(w.apiURL + "/api/streams")
	if err != nil {
		return 0, fmt.Errorf("sin respuesta de go2rtc (%s): %w", w.apiURL, err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return 0, fmt.Errorf("go2rtc respondio %d", resp.StatusCode)
	}
	var all map[string]struct {
		Consumers []struct {
			ID int `json:"id"`
		} `json:"consumers"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&all); err != nil {
		return 0, fmt.Errorf("respuesta de go2rtc ilegible: %w", err)
	}
	return len(all[w.stream].Consumers), nil
}

// ffmpegKeeper es el guardian: ffmpeg abre el RTSP local, copia los paquetes a
// la nada y, de paso, mantiene el productor de go2rtc vivo. Se usa ffmpeg y no
// un cliente RTSP propio porque ya esta en la imagen para /snapshot.jpg.
type ffmpegKeeper struct {
	cmd    *exec.Cmd
	doneCh chan struct{}
	once   bool
}

func startFFmpegKeeper(rtspURL string) (warmKeeper, error) {
	cmd := exec.Command("ffmpeg", "-hide_banner", "-loglevel", "error",
		"-rtsp_transport", "tcp", "-i", rtspURL,
		"-c", "copy", "-f", "null", "-")
	cmd.Stderr = os.Stderr
	if err := cmd.Start(); err != nil {
		return nil, err
	}
	k := &ffmpegKeeper{cmd: cmd, doneCh: make(chan struct{})}
	go func() {
		_ = cmd.Wait()
		close(k.doneCh)
	}()
	return k, nil
}

func (k *ffmpegKeeper) done() <-chan struct{} {
	return k.doneCh
}

// stop pide a ffmpeg que cierre la sesion (SIGINT, asi go2rtc ve el TEARDOWN) y
// solo lo mata si no hace caso.
func (k *ffmpegKeeper) stop() {
	if k.once {
		return
	}
	k.once = true
	if k.cmd.Process != nil {
		_ = k.cmd.Process.Signal(os.Interrupt)
	}
	select {
	case <-k.doneCh:
	case <-time.After(keeperStopTimeout):
		if k.cmd.Process != nil {
			_ = k.cmd.Process.Kill()
		}
	}
}

// shouldLogWarmFailure limita los avisos de sondeo: el primero, y luego uno
// cada minuto (treinta sondeos de dos segundos).
func shouldLogWarmFailure(failures int) bool {
	return failures == 1 || failures%30 == 0
}
