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
func (c *cameraAuto) stillURL() string {
	host := c.stream
	if u, err := url.Parse(c.stream); err == nil && u.Hostname() != "" {
		host = u.Hostname()
	}
	return fmt.Sprintf("http://%s:%d/snapshot.jpg", host, c.apiPort)
}

// exists mira si ya hay una camara con "wibox" en el nombre: la integracion no
// deja leer las opciones de una entrada, asi que no hay otra forma barata.
func (c *cameraAuto) exists() bool {
	raw, code, err := c.call("GET", "/api/states", nil)
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

func (c *cameraAuto) create() error {
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
	// La imagen fija la sirve el propio daemon en el mismo host que el stream:
	// /health responde siempre, incluso con el timbre dormido.
	still := c.stillURL()
	body := map[string]any{
		"stream_source":   c.stream,
		"still_image_url": still,
		"advanced": map[string]any{
			"framerate":      2.0,
			"verify_ssl":     false,
			"rtsp_transport": "tcp",
		},
	}
	raw, code, err = c.call("POST", "/api/config/config_entries/flow/"+flow.FlowID, body)
	if err != nil {
		return err
	}
	if !bytes.Contains(raw, []byte(`"type":"create_entry"`)) && !bytes.Contains(raw, []byte(`"type": "create_entry"`)) {
		return fmt.Errorf("el alta no confirmo create_entry (http %d): %s", code, truncate(string(raw), 200))
	}
	return nil
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
