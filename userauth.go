package main

import (
	"bytes"
	"crypto/tls"
	"encoding/xml"
	"fmt"
	"io"
	"log"
	"net/http"
	"os"
	"regexp"
	"strconv"
	"strings"
	"sync"
	"time"
)

// El gateway de Qualvision presenta un certificado autofirmado sin CN, asi que
// no hay forma de validarlo contra un almacen de confianza. La app oficial
// tampoco lo valida (usa un TrustManager que acepta todo).
func insecureClient() *http.Client {
	return &http.Client{
		Timeout: 60 * time.Second,
		Transport: &http.Transport{
			TLSClientConfig: &tls.Config{InsecureSkipVerify: true},
		},
	}
}

type node struct {
	XMLName xml.Name
	Text    string `xml:",chardata"`
	Nodes   []node `xml:",any"`
}

func (n node) child(name string) (node, bool) {
	for _, c := range n.Nodes {
		if c.XMLName.Local == name {
			return c, true
		}
	}
	return node{}, false
}

func (n node) text(name string) string {
	if c, ok := n.child(name); ok {
		return strings.TrimSpace(c.Text)
	}
	return ""
}

// UserAuth habla el canal duplex del proveedor: un long-poll de bajada y un
// POST de subida, con una cookie jsessionid que hay que conseguir antes.
type UserAuth struct {
	baseURL  string
	clientID string
	client   *http.Client

	mu        sync.Mutex
	jsession  string
	session   string
	seq       int
	accountID string
}

func NewUserAuth(baseURL, clientID string) *UserAuth {
	return &UserAuth{
		baseURL:  strings.TrimSuffix(baseURL, "/"),
		clientID: clientID,
		client:   insecureClient(),
	}
}

func (u *UserAuth) downURL() string   { return u.baseURL + "/auth/nologin;jus_duplex=down" }
func (u *UserAuth) nologinUp() string { return u.baseURL + "/auth/nologin;jus_duplex=up" }
func (u *UserAuth) userUp() string    { return u.baseURL + "/auth/user;jus_duplex=up" }

func debugOn() bool { return os.Getenv("WIBOX_DEBUG") != "" }

func truncate(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n] + "..."
}

var jsessionRe = regexp.MustCompile(`jsessionid=([^;]+)`)

func (u *UserAuth) ensureSession() error {
	if u.jsession != "" {
		return nil
	}
	var last error
	for attempt := 0; attempt < 2; attempt++ {
		req, err := http.NewRequest("GET", u.downURL(), nil)
		if err != nil {
			return err
		}
		resp, err := u.client.Do(req)
		if err != nil {
			last = err
			time.Sleep(time.Duration(600*(attempt+1)) * time.Millisecond)
			continue
		}
		if debugOn() {
			log.Printf("down(cookie) status=%d setcookie=%v", resp.StatusCode, resp.Header.Values("Set-Cookie"))
		}
		var found string
		for _, c := range resp.Header.Values("Set-Cookie") {
			if m := jsessionRe.FindStringSubmatch(c); m != nil {
				found = m[1]
			}
		}
		resp.Body.Close()
		if found == "" {
			last = fmt.Errorf("userauth down sin cookie jsessionid")
			time.Sleep(time.Duration(600*(attempt+1)) * time.Millisecond)
			continue
		}
		u.jsession = found
		return nil
	}
	return last
}

// buildEnvelope replica el XStream del cliente oficial byte a byte.
func buildEnvelope(command, contentClass string, fields [][2]string, session string, seq int) string {
	var b strings.Builder
	b.WriteString(`<?xml version="1.0" encoding="UTF-8"?><envelope>` + "\n")
	b.WriteString(`   <content class="` + esc(contentClass) + `">` + "\n")
	for _, f := range fields {
		b.WriteString(`      <` + f[0] + `>` + esc(f[1]) + `</` + f[0] + `>` + "\n")
	}
	b.WriteString("   </content>\n")
	b.WriteString("   <header>\n")
	b.WriteString("      <client>\n")
	b.WriteString("         <app>2021</app>\n")
	b.WriteString("         <id>" + esc(u2(clientID)) + "</id>\n")
	b.WriteString("         <oem>G0021</oem>\n")
	b.WriteString("         <type>3</type>\n")
	b.WriteString("      </client>\n")
	b.WriteString("      <command>" + esc(command) + "</command>\n")
	b.WriteString("      <flag>tdkcloud</flag>\n")
	b.WriteString("      <seq>" + strconv.Itoa(seq) + "</seq>\n")
	if session != "" {
		b.WriteString("      <session>" + esc(session) + "</session>\n")
	}
	b.WriteString("      <user-data></user-data>\n")
	b.WriteString("      <version></version>\n")
	b.WriteString("   </header>\n")
	b.WriteString("</envelope>\n")
	return b.String()
}

// clientID es un global de configuracion para no arrastrarlo por todas las firmas.
var clientID string

func u2(s string) string { return s }

func esc(s string) string {
	var b bytes.Buffer
	xml.EscapeText(&b, []byte(s))
	return b.String()
}

type Response struct {
	Command string
	Result  int
	Session string
	Content map[string]string
	Devices []map[string]string
}

func (r *Response) IsSuccess() bool { return r.Result == 0 }

var doctypeRe = regexp.MustCompile(`(?is)<!DOCTYPE[^>]*>`)
var entityRe = regexp.MustCompile(`(?is)<!ENTITY[^>]*>`)

func parseResponse(raw []byte) (*Response, error) {
	clean := doctypeRe.ReplaceAllString(string(raw), "")
	clean = entityRe.ReplaceAllString(clean, "")
	var root node
	if err := xml.Unmarshal([]byte(clean), &root); err != nil {
		return nil, fmt.Errorf("respuesta xml ilegible: %w", err)
	}
	out := &Response{Content: map[string]string{}}
	if h, ok := root.child("header"); ok {
		out.Command = h.text("command")
		if v := h.text("result"); v != "" {
			out.Result, _ = strconv.Atoi(v)
		}
		if s, ok := h.child("session"); ok {
			if len(s.Nodes) > 0 {
				out.Session = s.text("id")
			} else {
				out.Session = strings.TrimSpace(s.Text)
			}
		}
	}
	if c, ok := root.child("content"); ok {
		for _, ch := range c.Nodes {
			out.Content[ch.XMLName.Local] = strings.TrimSpace(ch.Text)
		}
		// Los dispositivos pueden venir dentro de un <devices> envolvente, asi
		// que se buscan en cualquier profundidad.
		var walk func(n node)
		walk = func(n node) {
			for _, ch := range n.Nodes {
				if ch.XMLName.Local == "device" {
					d := map[string]string{}
					for _, f := range ch.Nodes {
						d[f.XMLName.Local] = strings.TrimSpace(f.Text)
					}
					out.Devices = append(out.Devices, d)
					continue
				}
				walk(ch)
			}
		}
		walk(c)
	}
	return out, nil
}

// command envia el envelope por el canal de subida y recoge la respuesta
// sondeando el canal de bajada. El orden importa: si el long-poll esta en
// vuelo cuando llega la subida, el gateway responde "BeginResponseGenerator
// return failed". Primero se sube y despues se recoge.
func (u *UserAuth) command(command, contentClass string, fields [][2]string, timeout time.Duration) (*Response, error) {
	u.mu.Lock()
	defer u.mu.Unlock()

	if err := u.ensureSession(); err != nil {
		return nil, err
	}
	u.seq++
	body := buildEnvelope(command, contentClass, fields, u.session, u.seq)
	cookie := "jsessionid=" + u.jsession

	upURL := u.nologinUp()
	if u.session != "" {
		upURL = u.userUp()
	}
	req, err := http.NewRequest("POST", upURL, strings.NewReader(body))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Cookie", cookie)
	req.Header.Set("charset", "utf-8")
	req.Header.Set("Accept-Language", "en-US")
	req.Header.Set("Connection", "Keep-Alive")
	req.Header.Set("User-Agent", "okhttp/3.9.0")
	req.Header.Set("Content-Type", "application/xml")
	upResp, err := u.client.Do(req)
	if err != nil {
		return nil, err
	}
	upRaw, _ := io.ReadAll(upResp.Body)
	upResp.Body.Close()
	if debugOn() {
		log.Printf("up cmd=%s status=%d len=%d body=%q", command, upResp.StatusCode, len(upRaw), truncate(string(upRaw), 300))
	}
	// Camino rapido: a veces el gateway contesta en el propio POST.
	if len(bytes.TrimSpace(upRaw)) > 0 {
		if parsed, perr := parseResponse(upRaw); perr == nil && parsed.Command == command {
			return parsed, nil
		}
	}

	// Recogida: la respuesta queda encolada y se lee con un down posterior.
	deadline := time.Now().Add(timeout)
	delay := 150 * time.Millisecond
	for time.Now().Before(deadline) {
		time.Sleep(delay)
		if delay < time.Second {
			delay *= 2
		}
		dreq, derr := http.NewRequest("GET", u.downURL(), nil)
		if derr != nil {
			return nil, derr
		}
		dreq.Header.Set("Cookie", cookie)
		dreq.Header.Set("User-Agent", "okhttp/3.9.0")
		cli := insecureClient()
		cli.Timeout = 10 * time.Second
		dresp, derr := cli.Do(dreq)
		if derr != nil {
			continue
		}
		dBody, _ := io.ReadAll(dresp.Body)
		dresp.Body.Close()
		if debugOn() {
			log.Printf("down status=%d len=%d body=%q", dresp.StatusCode, len(dBody), truncate(string(dBody), 300))
		}
		if len(bytes.TrimSpace(dBody)) == 0 {
			continue
		}
		parsed, perr := parseResponse(dBody)
		if perr != nil {
			continue
		}
		if parsed.Command == command {
			return parsed, nil
		}
	}
	return nil, fmt.Errorf("timeout esperando %q", command)
}

func (u *UserAuth) Login(account, password string) (*Response, error) {
	fields := [][2]string{
		{"account", account},
		{"auth-code", ""},
		{"ip-region-id", "1"},
		{"password", password},
		{"auth-type", "0"},
	}
	resp, err := u.command("login", "com.quvii.qvweb.userauth.bean.request.LoginReqContent", fields, 20*time.Second)
	if err != nil {
		return nil, err
	}
	if !resp.IsSuccess() {
		return nil, fmt.Errorf("login rechazado result=%d", resp.Result)
	}
	u.session = resp.Session
	u.accountID = resp.Content["account-id"]
	if u.accountID == "" {
		u.accountID = resp.Content["id"]
	}
	return resp, nil
}

func (u *UserAuth) DeviceList() (*Response, error) {
	resp, err := u.command("get-device-list", "com.quvii.qvweb.userauth.bean.request.DevListReqContent", nil, 30*time.Second)
	if err != nil {
		return nil, err
	}
	if !resp.IsSuccess() {
		return nil, fmt.Errorf("get-device-list rechazado result=%d", resp.Result)
	}
	return resp, nil
}

type DeviceToken struct {
	AuthCode        string
	DataEncodeKey   string
	DynamicPassword string
	Expired         string
}

func (u *UserAuth) DeviceToken(deviceID string) (*DeviceToken, error) {
	fields := [][2]string{{"device-id", deviceID}}
	resp, err := u.command("get-device-token", "com.quvii.qvweb.userauth.bean.request.DevDynamicPwdGetReqContent", fields, 30*time.Second)
	if err != nil {
		return nil, err
	}
	if !resp.IsSuccess() {
		return nil, fmt.Errorf("get-device-token rechazado result=%d", resp.Result)
	}
	t := &DeviceToken{
		AuthCode:        firstOf(resp.Content, "auth-code", "out-auth-code"),
		DataEncodeKey:   resp.Content["data-encode-key"],
		DynamicPassword: resp.Content["dynamic-password"],
		Expired:         resp.Content["password-expired"],
	}
	return t, nil
}

func firstOf(m map[string]string, keys ...string) string {
	for _, k := range keys {
		if v := m[k]; v != "" {
			return v
		}
	}
	return ""
}
