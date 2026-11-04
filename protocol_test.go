package main

import (
	"strings"
	"testing"
)

func TestBuildEnvelopeMatchesWireFormat(t *testing.T) {
	clientID = "003-2021-87dad215f30f1f7e"
	got := buildEnvelope(
		"login",
		"com.quvii.qvweb.userauth.bean.request.LoginReqContent",
		[][2]string{{"account", "A&B"}, {"password", "p"}},
		"",
		7,
	)
	want := `<?xml version="1.0" encoding="UTF-8"?><envelope>
   <content class="com.quvii.qvweb.userauth.bean.request.LoginReqContent">
      <account>A&amp;B</account>
      <password>p</password>
   </content>
   <header>
      <client>
         <app>2021</app>
         <id>003-2021-87dad215f30f1f7e</id>
         <oem>G0021</oem>
         <type>3</type>
      </client>
      <command>login</command>
      <flag>tdkcloud</flag>
      <seq>7</seq>
      <user-data></user-data>
      <version></version>
   </header>
</envelope>
`
	if got != want {
		t.Fatalf("envelope distinto:\n--- got ---\n%s\n--- want ---\n%s", got, want)
	}
}

func TestBuildEnvelopeIncludesSession(t *testing.T) {
	clientID = "c1"
	got := buildEnvelope("get-device-list", "cls", nil, "SESSION123", 2)
	if !strings.Contains(got, "<session>SESSION123</session>") {
		t.Fatalf("falta la sesion en el envelope:\n%s", got)
	}
	if !strings.Contains(got, "<seq>2</seq>") {
		t.Fatalf("seq incorrecta:\n%s", got)
	}
}

// Fixture real del gateway: respuesta de login capturada el 4-nov-2026.
const loginResponse = `<?xml version="1.0" encoding="UTF-8"?><envelope><header><flag>tdkcloud</flag><version>v1.24</version><command>login</command><seq>1</seq><session><id>cHRpZD0wMGI4ODU4OGU2JmFjaWQ9MDAwMDFmMmEzMiZYeE00cnpzWnZteThjWmFDMEpKZkRvYzdnMTAyODBF</id><timeout>0</timeout></session><result>0</result></header><content><account-id>2042418</account-id><nick>OX00000000001f2a32003-2021-87dad215f30f1f7e</nick><expire>0</expire><token></token><is-guest>0</is-guest><mfa-status>0</mfa-status><mfa-ttl>0</mfa-ttl></content></envelope>`

func TestParseLoginResponse(t *testing.T) {
	resp, err := parseResponse([]byte(loginResponse))
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	if resp.Command != "login" {
		t.Fatalf("command=%q", resp.Command)
	}
	if !resp.IsSuccess() {
		t.Fatalf("result=%d", resp.Result)
	}
	if resp.Session == "" {
		t.Fatal("sin sesion")
	}
	if resp.Content["account-id"] != "2042418" {
		t.Fatalf("account-id=%q", resp.Content["account-id"])
	}
}

func TestParseDeviceList(t *testing.T) {
	raw := `<envelope><header><command>get-device-list</command><result>0</result></header><content><devices><device><id>abc</id><name>Probe</name><model>IDS7938</model></device></devices></content></envelope>`
	resp, err := parseResponse([]byte(raw))
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	if len(resp.Devices) != 1 || resp.Devices[0]["id"] != "abc" {
		t.Fatalf("devices=%v", resp.Devices)
	}
}

func TestParseRejectsNonEnvelope(t *testing.T) {
	if _, err := parseResponse([]byte("CAUTHNETClient::Response(packet): BeginResponseGenerator return failed")); err == nil {
		t.Fatal("esperaba error con texto plano")
	}
}

func TestEncodeDevicePasswordKeepsLongSecrets(t *testing.T) {
	long := strings.Repeat("a", 64)
	if encodeDevicePassword(long) != long {
		t.Fatal("un secreto de 64 caracteres no se debe transformar")
	}
	short := encodeDevicePassword("0925")
	if len(short) != 64 {
		t.Fatalf("sha256 hex de 64 caracteres, obtuve %d", len(short))
	}
}
