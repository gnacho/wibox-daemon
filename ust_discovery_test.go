package main

import (
	"strings"
	"testing"
)

func TestBuildQueryXMLUsesDefaultServices(t *testing.T) {
	got := buildQueryXML("003-2021-87dad215f30f1f7e", "G0021", 2021, nil, "1", 0)
	if !strings.Contains(got, "<command>query-hlrv2</command>") {
		t.Fatalf("falta el comando: %s", got)
	}
	if !strings.Contains(got, "<server-type>p2papp,natcheck,appinfo</server-type>") {
		t.Fatalf("servicios por defecto: %s", got)
	}
	if !strings.Contains(got, "<seq>0</seq>") {
		t.Fatalf("seq: %s", got)
	}
	if !strings.Contains(got, "<version>4456</version>") {
		t.Fatalf("version del protocolo: %s", got)
	}
	if !strings.Contains(got, "<client-id>003-2021-87dad215f30f1f7e</client-id>") {
		t.Fatalf("client-id: %s", got)
	}
	if strings.Count(got, "\n") != 0 {
		t.Fatalf("la peticion va en una sola linea: %q", got)
	}
}

func TestBuildQueryXMLHonoursCustomServices(t *testing.T) {
	got := buildQueryXML("c1", "o&m", 2021, []string{"p2papp"}, "7", 3)
	if !strings.Contains(got, "<server-type>p2papp</server-type>") {
		t.Fatalf("server-type propio: %s", got)
	}
	if !strings.Contains(got, "<oem>o&amp;m</oem>") {
		t.Fatalf("el escape XML no se aplico: %s", got)
	}
	if !strings.Contains(got, "<seq>3</seq>") || !strings.Contains(got, "<regionid>7</regionid>") {
		t.Fatalf("seq o region: %s", got)
	}
}

const discoveryFixture = `<?xml version="1.0" encoding="UTF-8"?><envelope><header><flag>tdkcloud</flag><command>query-hlrv2</command><result>0</result></header><content>` +
	`<server><server-type>p2papp</server-type><query-result>0</query-result><regionid>1</regionid>` +
	`<url>mqtts://mqttsr1.qvcloud.net:1884</url><uri>/app/ust/json</uri><param>username=a&amp;password=b</param></server>` +
	`<server><server-type>natcheck</server-type><query-result>0</query-result><regionid>1</regionid>` +
	`<url>udp://8.211.5.8:8300</url></server>` +
	`</content></envelope>`

func TestParseUstServicesReadsEveryServer(t *testing.T) {
	entries, err := parseUstServices([]byte(discoveryFixture))
	if err != nil {
		t.Fatalf("parseUstServices: %v", err)
	}
	if len(entries) != 2 {
		t.Fatalf("entradas=%d, se esperaban 2", len(entries))
	}
	if entries[0].ServerType != "p2papp" || entries[0].URL != "mqtts://mqttsr1.qvcloud.net:1884" {
		t.Fatalf("entrada p2papp = %+v", entries[0])
	}
	if entries[0].URI != "/app/ust/json" || entries[0].Param != "username=a&password=b" {
		t.Fatalf("uri/param = %+v", entries[0])
	}
	if entries[1].ServerType != "natcheck" || entries[1].URL != "udp://8.211.5.8:8300" {
		t.Fatalf("entrada natcheck = %+v", entries[1])
	}
}

func TestParseUstServicesRejectsGarbage(t *testing.T) {
	if _, err := parseUstServices([]byte("no es xml")); err == nil {
		t.Fatal("esperaba error con texto plano")
	}
}

func TestServicesBuildURLs(t *testing.T) {
	services := &ustServices{
		P2PApp: &ustServiceEntry{
			ServerType: "p2papp",
			URL:        "mqtts://mqttsr1.qvcloud.net:1884",
			URI:        "/app/ust/json",
			Param:      "username=a&password=b",
		},
		Natcheck: &ustServiceEntry{ServerType: "natcheck", URL: "udp://8.211.5.8:8300"},
	}
	if got := services.mqttURL(); got != "mqtts://mqttsr1.qvcloud.net:1884/app/ust/json/?username=a&password=b" {
		t.Fatalf("mqttURL = %q", got)
	}
	if got := services.testAddress(); !strings.Contains(got, "8.211.5.8") {
		t.Fatalf("testAddress = %q", got)
	}

	// Sin param no se anade el separador.
	services.P2PApp.Param = ""
	if got := services.mqttURL(); got != "mqtts://mqttsr1.qvcloud.net:1884/app/ust/json" {
		t.Fatalf("mqttURL sin param = %q", got)
	}
	if services.mqttURL() == "" || services.testAddress() == "" {
		t.Fatal("las dos URL deberian salir")
	}
}

func TestPercentDecodeOnlyKeepsPlus(t *testing.T) {
	cases := map[string]string{
		"%2B":           "+",
		"a+b":           "a+b",
		"%26":           "&",
		"sin%20escape":  "sin escape",
		"texto%2":       "texto%2",
		"nada":          "nada",
		"JWT.abc%3D%3D": "JWT.abc==",
	}
	for in, want := range cases {
		if got := percentDecodeOnly(in); got != want {
			t.Fatalf("percentDecodeOnly(%q) = %q, se esperaba %q", in, got, want)
		}
	}
}
