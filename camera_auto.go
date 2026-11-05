package main

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net/http"
	"net/url"
	"strings"
	"time"
)

// La integracion de camara generica valida el stream abriendo: antes del primer
// keyframe responde {"stream_source": "timeout"}. Como el daemon es quien sabe
// si ya hay imagen (contadores del proceso de video), es tambien quien puede
// crear la entrada en el momento justo, sin depender de un script aparte.
type cameraAuto struct {
	haURL   string
	token   string
	stream  string
	apiPort int
	client  *http.Client
}

func newCameraAuto(cfg config) *cameraAuto {
	return &cameraAuto{
		haURL:   strings.TrimSuffix(cfg.haURL, "/"),
		token:   cfg.haToken,
		stream:  cfg.haStream,
		apiPort: cfg.apiPort,
		client:  &http.Client{Timeout: 60 * time.Second},
	}
}

func (c *cameraAuto) call(method, path string, body any) ([]byte, int, error) {
	var reader io.Reader
	if body != nil {
		raw, err := json.Marshal(body)
		if err != nil {
			return nil, 0, err
		}
		reader = bytes.NewReader(raw)
	}
	req, err := http.NewRequest(method, c.haURL+path, reader)
	if err != nil {
		return nil, 0, err
	}
	req.Header.Set("Authorization", "Bearer "+c.token)
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	resp, err := c.client.Do(req)
	if err != nil {
		return nil, 0, err
	}
	defer resp.Body.Close()
	raw, _ := io.ReadAll(resp.Body)
	return raw, resp.StatusCode, nil
}

// stillURL compone http://<host del stream>:<puerto de la api>/health.
func (c *cameraAuto) host() string {
	if u, err := url.Parse(c.stream); err == nil && u.Hostname() != "" {
		return u.Hostname()
	}
	return ""
}

func (c *cameraAuto) stillURL() string {
	host := c.stream
	if h := c.host(); h != "" {
		host = h
	}
	return fmt.Sprintf("http://%s:%d/snapshot.jpg", host, c.apiPort)
}

// exists mira si ya hay una entrada de camara generica nuestra. Se mira la
// lista de config entries (dominio generic con el host del stream en el
// titulo, que es como la nombra Home Assistant) y, por si acaso, las camaras
// que lleven "wibox" en el nombre.
func (c *cameraAuto) exists() bool {
	raw, code, err := c.call("GET", "/api/config/config_entries/entry", nil)
	if err == nil && code == 200 {
		var entries []struct {
			Domain string `json:"domain"`
			Title  string `json:"title"`
		}
		if json.Unmarshal(raw, &entries) == nil {
			for _, e := range entries {
				if e.Domain != "generic" {
					continue
				}
				title := strings.ToLower(e.Title)
				if strings.Contains(title, "wibox") || (c.host() != "" && strings.Contains(title, strings.ReplaceAll(c.host(), ".", "_"))) {
					return true
				}
			}
		}
	}

	raw, code, err = c.call("GET", "/api/states", nil)
	if err != nil || code != 200 {
		return false
	}
	var states []struct {
		EntityID   string         `json:"entity_id"`
		Attributes map[string]any `json:"attributes"`
	}
	if err := json.Unmarshal(raw, &states); err != nil {
		return false
	}
	for _, s := range states {
		if !strings.HasPrefix(s.EntityID, "camera.") {
			continue
		}
		name, _ := s.Attributes["friendly_name"].(string)
		if strings.Contains(strings.ToLower(name), "wibox") {
			return true
		}
	}
	return false
}

// create intenta el alta con stream y, si Home Assistant no consigue validarlo,
// la repite apoyada solo en la imagen fija: en ese caso el flujo pide una
// confirmacion (paso user_confirm) y crea la entrada igual.
func (c *cameraAuto) create() error {
	if err := c.createAttempt(true); err != nil {
		log.Printf("camara: el alta con stream no valido (%v), se intenta solo con la imagen fija", err)
		return c.createAttempt(false)
	}
	return nil
}

// createAttempt recorre el config flow generico. advanced es obligatorio
// siempre, aunque no se mande stream_source.
func (c *cameraAuto) createAttempt(withStream bool) error {
	raw, code, err := c.call("POST", "/api/config/config_entries/flow", map[string]string{"handler": "generic"})
	if err != nil {
		return err
	}
	var flow struct {
		FlowID string `json:"flow_id"`
	}
	if err := json.Unmarshal(raw, &flow); err != nil || flow.FlowID == "" {
		return fmt.Errorf("flujo generic sin flow_id (http %d)", code)
	}

	body := map[string]any{
		"still_image_url": c.stillURL(),
		"advanced": map[string]any{
			"framerate":      2.0,
			"verify_ssl":     false,
			"rtsp_transport": "tcp",
		},
	}
	if withStream {
		body["stream_source"] = c.stream
	}

	for step := 0; step < 3; step++ {
		raw, code, err = c.call("POST", "/api/config/config_entries/flow/"+flow.FlowID, body)
		if err != nil {
			return err
		}
		if bytes.Contains(raw, []byte(`"type":"create_entry"`)) || bytes.Contains(raw, []byte(`"type": "create_entry"`)) {
			return nil
		}
		var resp struct {
			Type   string            `json:"type"`
			Step   string            `json:"step_id"`
			Errors map[string]string `json:"errors"`
		}
		if jerr := json.Unmarshal(raw, &resp); jerr != nil {
			return fmt.Errorf("respuesta del flujo ilegible (http %d): %s", code, truncate(string(raw), 200))
		}
		switch resp.Type {
		case "form":
			if resp.Step == "user_confirm" {
				// Confirmacion de "no hay stream, se crea con la imagen fija".
				body = map[string]any{"confirmed_ok": true}
				continue
			}
			if len(resp.Errors) > 0 {
				return fmt.Errorf("el flujo devolvio errores: %v", resp.Errors)
			}
			return fmt.Errorf("formulario inesperado en el paso %q", resp.Step)
		case "abort":
			return fmt.Errorf("el flujo aborto: %s", truncate(string(raw), 200))
		default:
			return fmt.Errorf("respuesta inesperada (http %d): %s", code, truncate(string(raw), 200))
		}
	}
	return fmt.Errorf("el flujo de la camara no termino en tres pasos")
}

// ensure crea la entrada si hace falta. Es idempotente: si ya existe, sale sin
// tocar nada.
func (c *cameraAuto) ensure() error {
	if c.exists() {
		return nil
	}
	return c.create()
}

// watchCameraEntry espera a que el proceso de video cuente keyframes y entonces
// crea la entrada. Solo se arranca si hay token y URL del stream configurados.
func watchCameraEntry(cfg config, interval time.Duration, stop <-chan struct{}) {
	if cfg.haToken == "" || cfg.haStream == "" {
		return
	}
	if interval <= 0 {
		interval = time.Minute
	}
	auto := newCameraAuto(cfg)
	log.Printf("camara: se creara la entrada en %s cuando llegue el primer keyframe", cfg.haURL)
	for {
		select {
		case <-stop:
			return
		case <-time.After(interval):
		}
		_, keys, _, ok := controlStats(cfg.controlSock)
		if !ok || keys <= 0 {
			continue
		}
		if err := auto.ensure(); err != nil {
			log.Printf("camara: %v", err)
			continue
		}
		log.Printf("camara: entrada lista en %s", cfg.haURL)
		return
	}
}
