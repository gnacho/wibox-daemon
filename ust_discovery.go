package main

import (
	"bytes"
	"encoding/xml"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"
)

// Servicios que anuncia el cloud para el canal P2P (query-hlrv2).
type ustServiceEntry struct {
	ServerType  string
	QueryResult int
	RegionID    int
	URL         string
	URI         string
	Param       string
}

type ustServices struct {
	P2PApp   *ustServiceEntry
	Natcheck *ustServiceEntry
}

// mqttURL es la URL del broker UST: url+uri y, si hay param, url+uri+"/?"+param.
func (s *ustServices) mqttURL() string {
	if s == nil || s.P2PApp == nil {
		return ""
	}
	if s.P2PApp.Param != "" {
		return s.P2PApp.URL + s.P2PApp.URI + "/?" + s.P2PApp.Param
	}
	return s.P2PApp.URL + s.P2PApp.URI
}

func (s *ustServices) testAddress() string {
	if s == nil || s.Natcheck == nil {
		return ""
	}
	return s.Natcheck.URL
}

func buildQueryXML(clientID, oem string, app int, serverTypes []string, regionID string, seq int) string {
	if len(serverTypes) == 0 {
		serverTypes = []string{"p2papp", "natcheck", "appinfo"}
	}
	var b strings.Builder
	b.WriteString(`<?xml version="1.0" encoding="UTF-8"?>`)
	b.WriteString("<envelope>")
	b.WriteString("<header><flag>tdkcloud</flag><command>query-hlrv2</command><seq>" + strconv.Itoa(seq) + "</seq></header>")
	b.WriteString("<content>")
	b.WriteString("<server-type>" + esc(strings.Join(serverTypes, ",")) + "</server-type>")
	b.WriteString("<oem>" + esc(oem) + "</oem>")
	b.WriteString("<devid></devid>")
	b.WriteString("<public-ip></public-ip>")
	b.WriteString("<client-id>" + esc(clientID) + "</client-id>")
	b.WriteString("<regionid>" + esc(regionID) + "</regionid>")
	b.WriteString("<version>4456</version>")
	b.WriteString("</content></envelope>")
	return b.String()
}

// parseUstServices recoge los <server> de la respuesta, esten donde esten.
func parseUstServices(raw []byte) ([]ustServiceEntry, error) {
	var root node
	if err := xml.Unmarshal(raw, &root); err != nil {
		return nil, fmt.Errorf("respuesta de query-hlrv2 ilegible: %w", err)
	}
	var out []ustServiceEntry
	var walk func(n node)
	walk = func(n node) {
		for _, ch := range n.Nodes {
			if ch.XMLName.Local == "server" {
				e := ustServiceEntry{
					ServerType: ch.text("server-type"),
					URL:        ch.text("url"),
					URI:        ch.text("uri"),
					Param:      ch.text("param"),
				}
				if v := ch.text("query-result"); v != "" {
					e.QueryResult, _ = strconv.Atoi(v)
				}
				if v := ch.text("regionid"); v != "" {
					e.RegionID, _ = strconv.Atoi(v)
				}
				out = append(out, e)
				continue
			}
			walk(ch)
		}
	}
	walk(root)
	return out, nil
}

// queryUstServices pide al cloud los servicios P2P y de natcheck.
func queryUstServices(serviceURL, clientID, oem string, app int, timeout time.Duration) (*ustServices, error) {
	body := buildQueryXML(clientID, oem, app, nil, "0", 0)
	endpoint := strings.TrimSuffix(serviceURL, "/") + "/mst/query"

	req, err := http.NewRequest("POST", endpoint, strings.NewReader(body))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", "application/xml;charset=utf-8")
	if u, perr := url.Parse(serviceURL); perr == nil && u.Host != "" {
		req.Host = u.Host
	}
	cli := insecureClient()
	cli.Timeout = timeout
	resp, err := cli.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	raw, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, err
	}
	if resp.StatusCode < 200 || resp.StatusCode > 299 {
		return nil, fmt.Errorf("query-hlrv2 http %d", resp.StatusCode)
	}
	entries, err := parseUstServices(raw)
	if err != nil {
		return nil, err
	}
	out := &ustServices{}
	for i := range entries {
		switch entries[i].ServerType {
		case "p2papp":
			if out.P2PApp == nil {
				out.P2PApp = &entries[i]
			}
		case "natcheck":
			if out.Natcheck == nil {
				out.Natcheck = &entries[i]
			}
		}
	}
	if out.P2PApp == nil || out.Natcheck == nil {
		return nil, fmt.Errorf("query-hlrv2 no devolvio p2papp/natcheck")
	}
	return out, nil
}

// percentDecodeOnly decodifica solo %XX. Nunca se usa QueryUnescape aqui: convierte
// '+' en espacio y corrompe el base64 de las credenciales.
func percentDecodeOnly(v string) string {
	var b bytes.Buffer
	for i := 0; i < len(v); i++ {
		if v[i] == '%' && i+2 < len(v) {
			if n, err := strconv.ParseUint(v[i+1:i+3], 16, 8); err == nil {
				b.WriteByte(byte(n))
				i += 2
				continue
			}
		}
		b.WriteByte(v[i])
	}
	return b.String()
}

// decodeMQTTCredentials saca usuario y contrasena del broker desde el param de la
// URL. La clave se deriva del client-id propio (el server ya lo sabe del query).
func decodeMQTTCredentials(mqttURL, srcid string) (string, string, error) {
	u, err := url.Parse(mqttURL)
	if err != nil {
		return "", "", fmt.Errorf("url del broker ilegible: %w", err)
	}
	rawQuery := strings.ReplaceAll(u.RawQuery, "&amp;", "&")
	params := map[string]string{}
	for _, part := range strings.Split(rawQuery, "&") {
		idx := strings.Index(part, "=")
		if idx <= 0 {
			continue
		}
		params[part[:idx]] = part[idx+1:]
	}
	if srcid == "" {
		return "", "", fmt.Errorf("credenciales del broker sin srcid")
	}
	tables, err := loadUSTTables()
	if err != nil {
		return "", "", err
	}
	user, err := tables.decodeCiphertext(percentDecodeOnly(params["username"]), srcid)
	if err != nil {
		return "", "", fmt.Errorf("usuario del broker: %w", err)
	}
	pass, err := tables.decodeCiphertext(percentDecodeOnly(params["password"]), srcid)
	if err != nil {
		return "", "", fmt.Errorf("contrasena del broker: %w", err)
	}
	return user, pass, nil
}
