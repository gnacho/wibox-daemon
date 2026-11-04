package main

import (
	"crypto/aes"
	"crypto/cipher"
	"encoding/base64"
	"testing"
)

// Vectores generados con la implementacion de referencia en Python
// (quii_helper/protocols/ust/key_derivation.py) sobre las mismas tablas.
func TestDeriveAESKeyMatchesReference(t *testing.T) {
	tables, err := loadUSTTables()
	if err != nil {
		t.Fatalf("tablas: %v", err)
	}
	cases := []struct {
		srcid   string
		seed    string
		sbox0   string
		sboxSha string
		key     string
	}{
		{
			srcid:   "test",
			seed:    "cb89b3ddd69cfd366cbfce8add5bf405a8768552e1114fe546a8768552e1114f",
			sbox0:   "3e965b4e311a8d9f1d65874c18dbc0d3",
			sboxSha: "9381dda7f1b0361e",
			key:     "6cbfce8add5bf405a8768552e1114fe546a8768552e1114faf222c5a79bed16c",
		},
		{
			srcid:   "003-2021-7d8e4f2a91c5b306",
			seed:    "87547396d89cff3769c60292125f2a07d97f8685e64352e54ca8768552e1114f",
			sbox0:   "eef38920d3c4ab8d9f1d65874c18dbc0",
			sboxSha: "aa8b51a11c109948",
			key:     "69c60292125f2a07d97f8685e64352e54ca8768552e1114f9b90fef3430c01c4",
		},
	}
	for _, c := range cases {
		seed, sbox := tables.generateSeedAndSbox(c.srcid)
		if got := hexOf(seed); got != c.seed {
			t.Fatalf("%s seed=%s", c.srcid, got)
		}
		if got := hexOf(sbox[:16]); got != c.sbox0 {
			t.Fatalf("%s sbox[0:16]=%s", c.srcid, got)
		}
		if got := sha256Hex(sbox)[:16]; got != c.sboxSha {
			t.Fatalf("%s sbox sha=%s", c.srcid, got)
		}
		if got := hexOf(tables.deriveAESKey(c.srcid)); got != c.key {
			t.Fatalf("%s key=%s", c.srcid, got)
		}
	}
}

func TestDecodeCiphertextRoundTrip(t *testing.T) {
	tables, err := loadUSTTables()
	if err != nil {
		t.Fatalf("tablas: %v", err)
	}
	srcid := "003-2021-7d8e4f2a91c5b306"
	secret := "B_003-2021-7d8e4f2a91c5b306"

	// Ciframos con la misma clave y IV que usa el broker y comprobamos que
	// decodeCiphertext lo devuelve tal cual, sin ceros de relleno.
	key := tables.deriveAESKey(srcid)
	plain := make([]byte, 32)
	copy(plain, secret)
	block, err := aes.NewCipher(key)
	if err != nil {
		t.Fatalf("aes: %v", err)
	}
	enc := make([]byte, len(plain))
	cipher.NewCBCEncrypter(block, ustIV).CryptBlocks(enc, plain)

	got, err := tables.decodeCiphertext(base64.StdEncoding.EncodeToString(enc), srcid)
	if err != nil {
		t.Fatalf("decode: %v", err)
	}
	if got != secret {
		t.Fatalf("decode=%q, esperaba %q", got, secret)
	}
}

func TestDecodeCiphertextRejectsBadInput(t *testing.T) {
	tables, err := loadUSTTables()
	if err != nil {
		t.Fatalf("tablas: %v", err)
	}
	if _, err := tables.decodeCiphertext("no-es-base64!!", "x"); err == nil {
		t.Fatal("esperaba error de base64")
	}
	if _, err := tables.decodeCiphertext(base64.StdEncoding.EncodeToString([]byte("corto")), "x"); err == nil {
		t.Fatal("esperaba error de tamano")
	}
}

func TestTablesAreTheOnesFromTheLibrary(t *testing.T) {
	tables, err := loadUSTTables()
	if err != nil {
		t.Fatalf("tablas: %v", err)
	}
	want := []string{
		"44d524b21bab5137d8f6f14d7206bcb0b3a06b8558906292efb6e6678acf6bea",
		"9cbefb328bd7954ae6ff15efe165bb411eef004bd4e31fe880881fc697d5b2a5",
		"66c652d1d1a41c5e72945c1d16eb9950584b7ea1354b7f85be68e263e8e9d391",
	}
	for i, table := range [][]byte{tables.d1, tables.d2, tables.d3} {
		if got := sha256Hex(table); got != want[i] {
			t.Fatalf("tabla %d sha=%s", i+1, got)
		}
	}
}

func hexOf(b []byte) string {
	const digits = "0123456789abcdef"
	out := make([]byte, 0, len(b)*2)
	for _, v := range b {
		out = append(out, digits[v>>4], digits[v&0xF])
	}
	return string(out)
}
