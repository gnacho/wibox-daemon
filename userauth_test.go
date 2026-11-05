package main

import (
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"
)

// fakeGateway imita el canal duplex del fabricante: la primera bajada da la
// cookie, la subida responde 200 vacio y la respuesta se recoge en la bajada
// siguiente. Es el orden que costo encontrar contra el gateway real.
type fakeGateway struct {
	mu       sync.Mutex
	pending  string
	session  string
	commands []string
	result   int
}

func (g *fakeGateway) envelope(command, content string) string {
	return `<?xml version="1.0" encoding="UTF-8"?><envelope><header><flag>tdkcloud</flag>` +
		`<version>v1.24</version><command>` + command + `</command><seq>1</seq>` +
		`<session><id>` + g.session + `</id><timeout>0</timeout></session>` +
		fmt.Sprintf(`<result>%d</result></header><content>`, g.result) + content + `</content></envelope>`
}

func (g *fakeGateway) serveHTTP(w http.ResponseWriter, r *http.Request) {
	g.mu.Lock()
	defer g.mu.Unlock()

	if strings.Contains(r.URL.Path, "jus_duplex=down") {
		if _, err := r.Cookie("jsessionid"); err != nil {
			http.SetCookie(w, &http.Cookie{Name: "jsessionid", Value: "abc123"})
			return
		}
		w.Header().Set("Content-Type", "application/xml")
		w.Write([]byte(g.pending))
		g.pending = ""
		return
	}

	body := make([]byte, 8192)
	n, _ := r.Body.Read(body)
	resp, err := parseResponse(body[:n])
	if err != nil {
		w.WriteHeader(http.StatusBadRequest)
		return
	}
	g.commands = append(g.commands, resp.Command)
	switch resp.Command {
	case "login":
		g.pending = g.envelope("login", `<account-id>1000001</account-id>`)
	case "get-device-list":
		g.pending = g.envelope("get-device-list",
			`<devices><device><id>bell1</id><name>Portal</name><model>IDS7938</model>`+
				`<online>1</online></device></devices>`)
	case "get-device-token":
		g.pending = g.envelope("get-device-token",
			`<auth-code>`+strings.Repeat("a", 64)+`</auth-code>`+
				`<data-encode-key>`+strings.Repeat("k", 32)+`</data-encode-key>`+
				`<dynamic-password>`+strings.Repeat("d", 108)+`</dynamic-password>`+
				`<password-expired>2026-12-01 10:00:00</password-expired>`)
	default:
		g.pending = g.envelope(resp.Command, "")
	}
	w.WriteHeader(http.StatusOK)
}

func TestUserAuthTalksToTheGateway(t *testing.T) {
	g := &fakeGateway{session: "SESION"}
	srv := httptest.NewServer(http.HandlerFunc(g.serveHTTP))
	defer srv.Close()

	u := NewUserAuth(srv.URL, "003-2021-87dad215f30f1f7e")
	login, err := u.Login("cuenta", "clave")
	if err != nil {
		t.Fatalf("login: %v", err)
	}
	if login.Session != "SESION" {
		t.Fatalf("sesion=%q", login.Session)
	}
	if login.Content["account-id"] != "1000001" {
		t.Fatalf("account-id=%q", login.Content["account-id"])
	}

	list, err := u.DeviceList()
	if err != nil {
		t.Fatalf("lista: %v", err)
	}
	if len(list.Devices) != 1 || list.Devices[0]["id"] != "bell1" || list.Devices[0]["name"] != "Portal" {
		t.Fatalf("dispositivos=%v", list.Devices)
	}

	tok, err := u.DeviceToken("bell1")
	if err != nil {
		t.Fatalf("token: %v", err)
	}
	if len(tok.AuthCode) != 64 || len(tok.DataEncodeKey) != 32 {
		t.Fatalf("token authCode=%d key=%d", len(tok.AuthCode), len(tok.DataEncodeKey))
	}
	if len(tok.DynamicPassword) != 108 {
		t.Fatalf("dynamic=%d", len(tok.DynamicPassword))
	}

	want := []string{"login", "get-device-list", "get-device-token"}
	if strings.Join(g.commands, ",") != strings.Join(want, ",") {
		t.Fatalf("comandos=%v", g.commands)
	}
}

func TestUserAuthRejectsAFailedResult(t *testing.T) {
	g := &fakeGateway{session: "SESION", result: 100100003}
	srv := httptest.NewServer(http.HandlerFunc(g.serveHTTP))
	defer srv.Close()

	u := NewUserAuth(srv.URL, "c")
	u.client.Timeout = 2 * time.Second
	if _, err := u.Login("cuenta", "mala"); err == nil {
		t.Fatal("se esperaba error con result distinto de cero")
	}
}
