package main

import (
	"os"
	"path/filepath"
	"testing"
)

func TestLoadConfigFileAppliesValues(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "daemon.conf")
	body := `# comentario
WIBOX_DEVICE_ID=abc123
export WIBOX_API_PORT=9999

WIBOX_ACCOUNT="cuenta con espacios"
WIBOX_PASSWORD='comillas simples'
WIBOX_EMPTY=
`
	if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
	t.Setenv("WIBOX_DEVICE_ID", "")
	t.Setenv("WIBOX_API_PORT", "")
	t.Setenv("WIBOX_ACCOUNT", "")
	t.Setenv("WIBOX_PASSWORD", "")

	if err := loadConfigFile(path); err != nil {
		t.Fatalf("loadConfigFile: %v", err)
	}
	cases := map[string]string{
		"WIBOX_DEVICE_ID": "abc123",
		"WIBOX_API_PORT":  "9999",
		"WIBOX_ACCOUNT":   "cuenta con espacios",
		"WIBOX_PASSWORD":  "comillas simples",
		"WIBOX_EMPTY":     "",
	}
	for key, want := range cases {
		if got := os.Getenv(key); got != want {
			t.Fatalf("%s = %q, se esperaba %q", key, got, want)
		}
	}
}

func TestLoadConfigFileRejectsBrokenLine(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "malo.conf")
	if err := os.WriteFile(path, []byte("WIBOX_DEVICE_ID\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	err := loadConfigFile(path)
	if err == nil {
		t.Fatal("una linea sin = deberia fallar")
	}
}

func TestLoadConfigFileMissingFile(t *testing.T) {
	if err := loadConfigFile(filepath.Join(t.TempDir(), "noexiste")); err == nil {
		t.Fatal("un fichero inexistente deberia dar error")
	}
}
