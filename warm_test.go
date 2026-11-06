package main

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

// fakeKeeper sustituye a ffmpeg en los tests de la ventana.
type fakeKeeper struct {
	quit chan struct{}
	once sync.Once
	// stopped se marca cuando la ventana suelta el guardian.
	stopped atomic.Bool
}

func newFakeKeeper() *fakeKeeper {
	return &fakeKeeper{quit: make(chan struct{})}
}

func (f *fakeKeeper) stop() {
	f.stopped.Store(true)
	f.once.Do(func() { close(f.quit) })
}

func (f *fakeKeeper) done() <-chan struct{} { return f.quit }

// streamsServer sirve /api/streams con un numero de consumidores que el test
// puede cambiar en caliente.
type streamsServer struct {
	srv       *httptest.Server
	consumers atomic.Int64
	hits      atomic.Int64
}

func newStreamsServer(t *testing.T, consumers int) *streamsServer {
	t.Helper()
	s := &streamsServer{}
	s.consumers.Store(int64(consumers))
	s.srv = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		s.hits.Add(1)
		if r.URL.Path != "/api/streams" {
			http.NotFound(w, r)
			return
		}
		n := int(s.consumers.Load())
		list := make([]map[string]any, 0, n)
		for i := 0; i < n; i++ {
			list = append(list, map[string]any{"id": i + 1, "format_name": "rtsp"})
		}
		_ = json.NewEncoder(w).Encode(map[string]any{
			"wibox": map[string]any{
				"producers": []map[string]any{{"id": 10, "protocol": "pipe"}},
				"consumers": list,
			},
		})
	}))
	t.Cleanup(s.srv.Close)
	return s
}

func TestWarmWindowCountsTheConsumersOfItsStream(t *testing.T) {
	s := newStreamsServer(t, 2)
	w := &warmWindow{apiURL: s.srv.URL, stream: "wibox", client: s.srv.Client()}

	viewers, err := w.viewers()
	if err != nil {
		t.Fatalf("viewers: %v", err)
	}
	if viewers != 2 {
		t.Fatalf("viewers = %d, se esperaban 2", viewers)
	}
}

func TestWarmWindowIgnoresAnUnknownStream(t *testing.T) {
	s := newStreamsServer(t, 3)
	w := &warmWindow{apiURL: s.srv.URL, stream: "otro", client: s.srv.Client()}

	viewers, err := w.viewers()
	if err != nil {
		t.Fatalf("viewers: %v", err)
	}
	if viewers != 0 {
		t.Fatalf("viewers = %d, se esperaba 0 para un stream que no existe", viewers)
	}
}

func TestWarmWindowReportsAG2rtcFailure(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, "no", http.StatusBadGateway)
	}))
	defer srv.Close()
	w := &warmWindow{apiURL: srv.URL, stream: "wibox", client: srv.Client()}

	if _, err := w.viewers(); err == nil {
		t.Fatal("se esperaba error con go2rtc devolviendo 502")
	}
}

func TestWarmWindowReportsAnUnreadableAnswer(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte("<html>no es json</html>"))
	}))
	defer srv.Close()
	w := &warmWindow{apiURL: srv.URL, stream: "wibox", client: srv.Client()}

	if _, err := w.viewers(); err == nil {
		t.Fatal("se esperaba error con una respuesta que no es JSON")
	}
}

func TestWarmDecideAttachesOnlyWhenSomebodyWatches(t *testing.T) {
	w := &warmWindow{seconds: time.Minute}
	now := time.Now()

	var st warmStep
	if attach, _ := w.decide(&st, 0, now); attach {
		t.Fatal("sin consumidores no se engancha el guardian")
	}
	if attach, _ := w.decide(&st, 1, now); !attach {
		t.Fatal("con un espectador hay que engancharlo")
	}
}

func TestWarmDecideRespectsTheRetryWindow(t *testing.T) {
	w := &warmWindow{seconds: time.Minute}
	now := time.Now()
	st := warmStep{retryAt: now.Add(30 * time.Second)}

	if attach, _ := w.decide(&st, 1, now); attach {
		t.Fatal("tras un fallo de ffmpeg no se reintenta antes de tiempo")
	}
	if attach, _ := w.decide(&st, 1, now.Add(31*time.Second)); !attach {
		t.Fatal("pasado el margen hay que reintentar")
	}
}

func TestWarmDecideKeepsTheWindowWhileSomebodyWatches(t *testing.T) {
	w := &warmWindow{seconds: 10 * time.Second}
	now := time.Now()
	st := warmStep{active: true, idleSince: now.Add(-time.Hour)}

	if _, detach := w.decide(&st, 2, now); detach {
		t.Fatal("con dos consumidores (guardian y espectador) no se suelta")
	}
	if !st.idleSince.Equal(now) {
		t.Fatalf("idleSince = %v, se esperaba %v", st.idleSince, now)
	}
}

func TestWarmDecideDropsTheWindowWhenItGoesIdle(t *testing.T) {
	w := &warmWindow{seconds: 10 * time.Second}
	now := time.Now()
	st := warmStep{active: true, idleSince: now}

	if _, detach := w.decide(&st, 1, now.Add(5*time.Second)); detach {
		t.Fatal("no se suelta antes de que pase la ventana")
	}
	if _, detach := w.decide(&st, 1, now.Add(11*time.Second)); !detach {
		t.Fatal("pasada la ventana con un solo consumidor hay que soltar")
	}
}

// TestWarmRunAttachesKeepsAndDrops recorre el bucle entero con tiempos cortos.
func TestWarmRunAttachesKeepsAndDrops(t *testing.T) {
	s := newStreamsServer(t, 1)
	keeper := newFakeKeeper()
	started := make(chan struct{}, 4)
	active := make(chan bool, 8)

	w := &warmWindow{
		apiURL:  s.srv.URL,
		stream:  "wibox",
		seconds: 40 * time.Millisecond,
		poll:    5 * time.Millisecond,
		client:  s.srv.Client(),
		start: func() (warmKeeper, error) {
			started <- struct{}{}
			return keeper, nil
		},
		setActive: func(on bool) { active <- on },
	}
	stop := make(chan struct{})
	go w.run(stop)

	select {
	case <-started:
	case <-time.After(2 * time.Second):
		t.Fatal("no se engancho el guardian con un espectador")
	}
	if on := <-active; !on {
		t.Fatal("setActive deberia haber dicho que la ventana esta abierta")
	}

	// Un segundo espectador mantiene la ventana aunque pasen los segundos.
	s.consumers.Store(2)
	time.Sleep(120 * time.Millisecond)
	if keeper.stopped.Load() {
		t.Fatal("con espectador no se suelta el guardian")
	}

	// Al quedarse solo el guardian, la ventana se cierra sola.
	s.consumers.Store(1)
	select {
	case on := <-active:
		if on {
			t.Fatal("se esperaba el cierre de la ventana")
		}
	case <-time.After(2 * time.Second):
		t.Fatal("la ventana no se cerro al quedarse sin espectadores")
	}
	if !keeper.stopped.Load() {
		t.Fatal("el guardian deberia estar parado")
	}

	close(stop)
}

func TestWarmRunDoesNothingWhenDisabled(t *testing.T) {
	var hits atomic.Int64
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		hits.Add(1)
	}))
	defer srv.Close()

	w := &warmWindow{apiURL: srv.URL, stream: "wibox", seconds: 0, poll: time.Millisecond}
	stop := make(chan struct{})
	done := make(chan struct{})
	go func() { w.run(stop); close(done) }()

	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("con la ventana desactivada run debe volver de inmediato")
	}
	if hits.Load() != 0 {
		t.Fatalf("no deberia consultar go2rtc, hubo %d peticiones", hits.Load())
	}
}

func TestWarmRunSurvivesAG2rtcError(t *testing.T) {
	var hits atomic.Int64
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		hits.Add(1)
		http.Error(w, "no", http.StatusInternalServerError)
	}))
	defer srv.Close()

	w := &warmWindow{
		apiURL: srv.URL, stream: "wibox", seconds: time.Minute, poll: 2 * time.Millisecond,
		client:    srv.Client(),
		start:     func() (warmKeeper, error) { t.Fatal("no deberia arrancar el guardian"); return nil, nil },
		setActive: func(bool) {},
	}
	stop := make(chan struct{})
	go w.run(stop)
	time.Sleep(20 * time.Millisecond)
	close(stop)

	if hits.Load() < 2 {
		t.Fatalf("el bucle deberia seguir sondeando, hubo %d peticiones", hits.Load())
	}
}

func TestStartKeeperReportsAMissingFfmpeg(t *testing.T) {
	t.Setenv("PATH", t.TempDir())

	if _, err := startFFmpegKeeper("rtsp://127.0.0.1:1/wibox"); err == nil {
		t.Fatal("sin ffmpeg en el PATH se esperaba error")
	}
}

func TestLoadConfigReadsTheWarmWindow(t *testing.T) {
	t.Setenv("WIBOX_WARM_SECONDS", "180")

	cfg := loadConfig()
	if cfg.warmSeconds != 180*time.Second {
		t.Fatalf("warmSeconds = %v, se esperaban 180s", cfg.warmSeconds)
	}
	if cfg.warmStream != "wibox" {
		t.Fatalf("warmStream = %q", cfg.warmStream)
	}
	if !strings.HasPrefix(cfg.warmRTSP, "rtsp://") || !strings.HasPrefix(cfg.warmAPI, "http://") {
		t.Fatalf("extremos locales inesperados: %q %q", cfg.warmRTSP, cfg.warmAPI)
	}
}

func TestLoadConfigWarmWindowIsOffByDefault(t *testing.T) {
	t.Setenv("WIBOX_WARM_SECONDS", "")

	if cfg := loadConfig(); cfg.warmSeconds != 0 {
		t.Fatalf("warmSeconds = %v, sin la variable debe estar apagada", cfg.warmSeconds)
	}
}

// TestAddOnExposesTheWarmWindow es el guardia del cableado: la opcion puede
// existir en config.yaml y no llegar nunca al proceso (ya paso con las WIBOX_HA_*).
func TestAddOnExposesTheWarmWindow(t *testing.T) {
	run, err := os.ReadFile(filepath.Join("wibox", "run.sh"))
	if err != nil {
		t.Fatalf("run.sh: %v", err)
	}
	if !strings.Contains(string(run), "WIBOX_WARM_SECONDS=\"$(value warm_seconds)\"") {
		t.Fatal("run.sh no lee la opcion warm_seconds")
	}
	if !strings.Contains(string(run), "export WIBOX_WARM_SECONDS") {
		t.Fatal("run.sh no exporta WIBOX_WARM_SECONDS")
	}

	conf, err := os.ReadFile(filepath.Join("wibox", "config.yaml"))
	if err != nil {
		t.Fatalf("config.yaml: %v", err)
	}
	if !strings.Contains(string(conf), "warm_seconds: 180") {
		t.Fatal("config.yaml no declara warm_seconds con 180 por defecto")
	}
	if !strings.Contains(string(conf), "warm_seconds: int(0,3600)") {
		t.Fatal("config.yaml no declara el rango de warm_seconds")
	}
}
