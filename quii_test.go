package main

import (
	"bytes"
	"crypto/sha256"
	"encoding/binary"
	"encoding/hex"
	"testing"
)

// Vector calculado aparte con openssl (AES-256-CBC, sin relleno, IV de 16
// bytes '0'), para no validar el cifrado contra si mismo:
//
//	printf '0123456789abcdef0123456789abcdef' |
//	  openssl enc -aes-256-cbc -nopad -K <key 00..1f> -iv 3030...30
func TestAESCBCOfficialVector(t *testing.T) {
	key := make([]byte, 32)
	for i := range key {
		key[i] = byte(i)
	}
	plain := []byte("0123456789abcdef0123456789abcdef")
	want := "6afd8633e91f149ff305980d1849eef9b8a436523d70e30c771cf6fd2f7948df"

	got, err := aesCBC(key, plain, true)
	if err != nil {
		t.Fatalf("cifrar: %v", err)
	}
	if hex.EncodeToString(got) != want {
		t.Fatalf("cifrado distinto:\n got %x\nwant %s", got, want)
	}
	back, err := aesCBC(key, got, false)
	if err != nil {
		t.Fatalf("descifrar: %v", err)
	}
	if !bytes.Equal(back, plain) {
		t.Fatalf("ida y vuelta rota: %x", back)
	}
}

// La cabecera replica QuiiEnvelope.buildHeader del cliente oficial: tipo en el
// byte 0, marca de tiempo uint64 little-endian desde el byte 1, tamanos uint16
// en 9 y 11, y los bytes de control en 13..17.
func TestQuiiHeaderLayout(t *testing.T) {
	h := quiiHeader(0x01, 0x1122334455667788, 48, 5, 1, 0, 1, 0, 0)
	if len(h) != 32 {
		t.Fatalf("cabecera de %d bytes", len(h))
	}
	if h[0] != 0x01 {
		t.Fatalf("tipo=%02x", h[0])
	}
	if ts := binary.LittleEndian.Uint64(h[1:9]); ts != 0x1122334455667788 {
		t.Fatalf("timestamp=%x", ts)
	}
	if n := binary.LittleEndian.Uint16(h[9:11]); n != 48 {
		t.Fatalf("commandPayloadSize=%d", n)
	}
	if n := binary.LittleEndian.Uint16(h[11:13]); n != 5 {
		t.Fatalf("rawSize=%d", n)
	}
	if h[13] != 1 || h[14] != 0 || h[15] != 1 || h[16] != 0 || h[17] != 0 {
		t.Fatalf("bytes de control = % x", h[13:18])
	}
	for i := 18; i < 32; i++ {
		if h[i] != 0 {
			t.Fatalf("byte %d no es cero: %02x", i, h[i])
		}
	}
}

// Un paquete cifrado es cabecera cifrada (32 B) seguida del cuerpo cifrado, que
// lleva pegado el SHA-256 de (cabecera clara + cuerpo). El cuerpo se rellena a
// multiplo de 16 anadiendo 32 bytes antes de calcular el relleno, igual que el
// cliente oficial.
func TestEncryptPacketMatchesReferenceLayout(t *testing.T) {
	key := make([]byte, 32)
	for i := range key {
		key[i] = byte(i)
	}
	body := []byte{1, 2, 3, 4, 5}
	const ts = 1700000000

	pkt, err := encryptPacketAt(key, 0xFE, body, 4, 0, 0, 0, 0, ts)
	if err != nil {
		t.Fatalf("cifrar paquete: %v", err)
	}
	wantLen := 32 + len(zeroPad(append(append([]byte{}, body...), make([]byte, 32)...)))
	if len(pkt) != wantLen {
		t.Fatalf("paquete de %d bytes, esperaba %d", len(pkt), wantLen)
	}

	clear, err := aesCBC(key, pkt[:32], false)
	if err != nil {
		t.Fatalf("descifrar cabecera: %v", err)
	}
	if clear[0] != 0xFE {
		t.Fatalf("tipo=%02x", clear[0])
	}
	if got := binary.LittleEndian.Uint64(clear[1:9]); got != ts {
		t.Fatalf("timestamp=%d", got)
	}
	if got := binary.LittleEndian.Uint16(clear[9:11]); got != uint16(wantLen-32) {
		t.Fatalf("commandPayloadSize=%d", got)
	}
	if got := binary.LittleEndian.Uint16(clear[11:13]); got != uint16(len(body)) {
		t.Fatalf("rawSize=%d", got)
	}
	if clear[13] != 4 {
		t.Fatalf("b13 (comando)=%d", clear[13])
	}

	plain, err := aesCBC(key, pkt[32:], false)
	if err != nil {
		t.Fatalf("descifrar cuerpo: %v", err)
	}
	if !bytes.Equal(plain[:len(body)], body) {
		t.Fatalf("cuerpo distinto: % x", plain[:len(body)])
	}
	digest := sha256.Sum256(append(append([]byte{}, clear...), body...))
	if !bytes.Equal(plain[len(body):len(body)+32], digest[:]) {
		t.Fatalf("cola sha256 distinta:\n got %x\nwant %x", plain[len(body):len(body)+32], digest)
	}
	for i := len(body) + 32; i < len(plain); i++ {
		if plain[i] != 0 {
			t.Fatalf("relleno no nulo en %d: %02x", i, plain[i])
		}
	}
}

// El cuerpo de apertura (orden transparente 4) lleva numero de cerradura, 0,
// canal, marca de contrasena hasheada y el SHA-256 hexadecimal del pin desde el
// byte 16.
func TestUnlockBodyLayout(t *testing.T) {
	body := unlockBody("1379", 1, 1)
	if len(body) != 16+64 {
		t.Fatalf("cuerpo de %d bytes, esperaba 80", len(body))
	}
	if body[0] != 1 || body[1] != 0 || body[2] != 1 || body[3] != 1 {
		t.Fatalf("cabecera del cuerpo = % x", body[:4])
	}
	sum := sha256.Sum256([]byte("1379"))
	if got, want := string(body[16:]), hex.EncodeToString(sum[:]); got != want {
		t.Fatalf("pin hasheado:\n got %s\nwant %s", got, want)
	}
}

// Un pin que ya son 64 caracteres (el auth-code del token) se copia tal cual.
func TestUnlockBodyKeepsLongPassword(t *testing.T) {
	long := make([]byte, 64)
	for i := range long {
		long[i] = 'a'
	}
	body := unlockBody(string(long), 2, 3)
	if body[0] != 2 || body[2] != 3 {
		t.Fatalf("cerradura/canal = %d/%d", body[0], body[2])
	}
	if !bytes.Equal(body[16:], long) {
		t.Fatalf("se transformo un secreto de 64 caracteres")
	}
}

func TestPlayBodyLayout(t *testing.T) {
	got := buildPlayBody("adminapp2", "secreto", "/mode=real&idc=1&ids=1&ap=2")
	want := []byte("adminapp2&&secreto\x00/mode=real&idc=1&ids=1&ap=2\x00")
	if !bytes.Equal(got, want) {
		t.Fatalf("cuerpo de play:\n got %q\nwant %q", got, want)
	}
}

func TestAESCBCRejectsUnalignedInput(t *testing.T) {
	key := make([]byte, 32)
	if _, err := aesCBC(key, make([]byte, 17), true); err == nil {
		t.Fatal("esperaba error con 17 bytes, no es multiplo de 16")
	}
	if _, err := aesCBC(key, nil, false); err == nil {
		t.Fatal("esperaba error con entrada vacia")
	}
	if _, err := aesCBC(key, make([]byte, 32), true); err != nil {
		t.Fatalf("32 bytes deberia valer: %v", err)
	}
}

func TestDecodeQuiiPacketRejectsUnalignedBody(t *testing.T) {
	key := make([]byte, 32)
	// Cabecera de tipo transparente (0xFE) con un tamano de cuerpo que no es
	// multiplo de 16: es lo que sale al descifrar basura de un flujo
	// desincronizado, y antes provocaba un panic en CryptBlocks.
	clear := quiiHeader(0xFE, 1700000000, 20, 20, 0, 0, 0, 0, 0)
	enc, err := aesCBC(key, clear, true)
	if err != nil {
		t.Fatalf("cabecera: %v", err)
	}
	buf := append(enc, make([]byte, 64)...)
	if _, used, err := decodeQuiiPacket(key, buf); err == nil {
		t.Fatalf("esperaba error, used=%d", used)
	}
}
