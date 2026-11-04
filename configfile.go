package main

import (
	"bufio"
	"fmt"
	"os"
	"strings"
)

// loadConfigFile aplica un fichero de estilo EnvironmentFile (KEY=VALOR, una por
// linea) al entorno del proceso. go2rtc lanza el daemon como hijo, asi que no
// hereda el entorno de systemd y necesita que lea su propia configuracion.
func loadConfigFile(path string) error {
	f, err := os.Open(path)
	if err != nil {
		return err
	}
	defer f.Close()

	sc := bufio.NewScanner(f)
	sc.Buffer(make([]byte, 0, 64*1024), 1024*1024)
	line := 0
	for sc.Scan() {
		line++
		raw := strings.TrimSpace(sc.Text())
		if raw == "" || strings.HasPrefix(raw, "#") {
			continue
		}
		raw = strings.TrimPrefix(raw, "export ")
		key, value, ok := strings.Cut(raw, "=")
		if !ok {
			return fmt.Errorf("%s:%d: falta el signo =", path, line)
		}
		key = strings.TrimSpace(key)
		if key == "" {
			return fmt.Errorf("%s:%d: clave vacia", path, line)
		}
		value = strings.TrimSpace(value)
		if len(value) >= 2 {
			if (value[0] == '"' && value[len(value)-1] == '"') || (value[0] == '\'' && value[len(value)-1] == '\'') {
				value = value[1 : len(value)-1]
			}
		}
		if err := os.Setenv(key, value); err != nil {
			return fmt.Errorf("%s:%d: %w", path, line, err)
		}
	}
	return sc.Err()
}
